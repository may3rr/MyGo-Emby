package player

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"mygo-emby/internal/emby"
)

// DiagnosticsInfo summarizes the active playback pipeline and streams with
// sensitive tokens redacted from URLs.
type DiagnosticsInfo struct {
	Pipeline   string
	Method     string
	Container  string
	Video      string
	VideoRange string
	Audio      string
	Subtitle   string
	SubOffset  string
	URL        string
}

// Status is how playback stands, for logs and tests.
type Status struct {
	Active, Loading, Ready, Playing, Buffering, Error bool
	ErrorText                                         string
	Position, Duration                                time.Duration
	Method                                            emby.PlayMethod
	URL                                               string
	AudioIndex, SubtitleIndex                         int
	ControlsShown                                     bool
	// EnginePosition is AVPlayer's own position, also before its first
	// picture.
	EnginePosition time.Duration
	// EngineReady is AVPlayer's first picture come; VideoBuilds counts
	// the frames that built the stream's Video.
	EngineReady bool
	VideoBuilds int
}

// Status returns how playback stands. Main thread.
func (p *Player) Status() Status {
	s := Status{
		Active:        p.active,
		Loading:       p.phase == phaseLoading,
		Error:         p.phase == phaseError,
		ErrorText:     p.errText,
		AudioIndex:    p.audioIdx,
		SubtitleIndex: p.subIdx,
		ControlsShown: p.controlsShown,
		Position:      p.position(),
		Duration:      p.runtime,
	}
	if pl := p.cur; pl != nil {
		s.Ready = pl.ready
		s.Playing = pl.eng.Playing() && !p.userPaused
		s.Buffering = pl.eng.Buffering()
		if d := pl.eng.Duration(); d > 0 {
			s.Duration = d
		}
		s.Method = pl.stream.Method
		s.EnginePosition = pl.eng.Position()
		s.EngineReady = pl.eng.Ready()
		s.VideoBuilds = pl.builds
		s.URL = pl.stream.URL
	}
	return s
}

// Debug acts as a user would, for the demo's screenshots and tests:
// "pause", "play", "hide-controls", "show-controls", "menu-speed",
// "menu-audio", "menu-subtitles", "close-menus", "seek:<seconds>",
// "sample-subtitle" (a line on screen now), "sample-tracks" (a choice of
// audio tracks and subtitles to show the menus with), "up-next" (the
// countdown card, with a sample next episode). Main thread.
func (p *Player) Debug(action string) error {
	now := p.now
	if now.IsZero() {
		now = time.Now()
	}
	switch action {
	case "pause":
		if !p.userPaused {
			p.togglePlay(now)
		}
	case "play":
		if p.userPaused {
			p.togglePlay(now)
		}
	case "hide-controls":
		p.activity = now.Add(-time.Hour)
		p.speedOpen, p.audioOpen, p.subOpen, p.chapterOpen = false, false, false, false
	case "show-controls":
		p.activity = now
	case "menu-speed":
		p.activity, p.speedOpen = now, true
	case "menu-audio":
		p.activity, p.audioOpen = now, true
	case "menu-subtitles":
		p.activity, p.subOpen = now, true
	case "menu-chapters":
		p.activity, p.chapterOpen = now, true
	case "close-menus":
		p.speedOpen, p.audioOpen, p.subOpen, p.chapterOpen = false, false, false, false
	case "sample-subtitle":
		// Two minutes of lines, a new one every two seconds, the first
		// showing now.
		pos := p.position()
		cues := make([]emby.Cue, 0, 60)
		for i := range 60 {
			text := "这是一行字幕示例，用来检查字号与位置\nA second line of the sample subtitle"
			if i%2 == 1 {
				text = "第 " + strconv.Itoa(i) + " 句字幕，换行时只重排这一段文字"
			}
			start := pos - time.Second + time.Duration(i)*2*time.Second
			cues = append(cues, emby.Cue{Start: start, End: start + 1900*time.Millisecond, Text: text})
		}
		p.subGen++
		p.subs.load(emby.NewSubtitles(cues))
		if p.subIdx < 0 {
			p.subIdx = 0
		}
	case "sample-tracks":
		p.audioTracks = []emby.Track{
			{Index: 1, Type: emby.StreamAudio, Codec: "aac", Language: "chi", DisplayTitle: "国语 AAC 立体声", IsDefault: true},
			{Index: 2, Type: emby.StreamAudio, Codec: "eac3", Language: "eng", DisplayTitle: "English E-AC-3 5.1"},
		}
		p.subTracks = []emby.Track{
			{Index: 3, Type: emby.StreamSubtitle, Codec: "subrip", Language: "chi", DisplayTitle: "简体中文 (SRT)", IsText: true},
			{Index: 4, Type: emby.StreamSubtitle, Codec: "ass", Language: "chi", Title: "简英双语", DisplayTitle: "ASS", IsText: true},
			{Index: 5, Type: emby.StreamSubtitle, Codec: "pgssub", Language: "eng", DisplayTitle: "English (PGS)", NeedsBurnIn: true},
		}
		p.audioLabels, p.subLabels = labels(p.audioTracks), labels(p.subTracks)
		p.audioIdx = 1
	case "up-next":
		n, s := 2, 1
		p.nextItem = &emby.Item{ID: "sample-next", Type: emby.TypeEpisode, Name: "示例下一集", IndexNumber: &n, ParentIndexNumber: &s, SeriesName: "示例剧集"}
		p.nextTitle, _ = titles(p.nextItem)
		p.upNext = upNextState{shown: true}
		p.upNextSecs, p.upNextText = 12, "12 "+str.UpNextIn
		p.upNextFrozen = true
	default:
		var sec float64
		if _, err := fmt.Sscanf(action, "seek:%g", &sec); err == nil && p.cur != nil {
			p.seek.seekTo(now, time.Duration(sec*float64(time.Second)))
			return nil
		}
		return fmt.Errorf("player: unknown debug action %q", action)
	}
	return nil
}

// Diagnostics returns the active playback pipeline, stream details and
// token-redacted stream URL. Main thread.
func (p *Player) Diagnostics() DiagnosticsInfo {
	return p.buildDiagnostics()
}

func (p *Player) refreshDiagnostics() {
	p.diag = p.buildDiagnostics()
}

func (p *Player) buildDiagnostics() DiagnosticsInfo {
	d := DiagnosticsInfo{SubOffset: formatSubOffset(p.subOffset)}
	pl := p.cur
	if pl == nil || pl.stream == nil {
		return d
	}
	st := pl.stream
	d.Method = string(st.Method)
	d.Pipeline = st.Pipeline
	if d.Pipeline == "" {
		d.Pipeline = d.Method
	}
	d.URL = emby.RedactURL(st.URL)
	if pb := st.Playback; pb != nil {
		src := &pb.Source
		var cparts []string
		if src.Container != "" {
			cparts = append(cparts, strings.ToUpper(src.Container))
		}
		if src.Bitrate > 0 {
			cparts = append(cparts, fmt.Sprintf("%.1f Mbps", float64(src.Bitrate)/1e6))
		}
		if src.Size > 0 {
			cparts = append(cparts, formatBytes(src.Size))
		}
		d.Container = strings.Join(cparts, " · ")

		if v := src.VideoStream(); v != nil {
			var vparts []string
			if v.Codec != "" {
				vparts = append(vparts, strings.ToUpper(v.Codec))
			}
			if v.Width > 0 && v.Height > 0 {
				vparts = append(vparts, fmt.Sprintf("%dx%d", v.Width, v.Height))
			}
			fps := v.RealFrameRate
			if fps <= 0 {
				fps = v.AverageFrameRate
			}
			if fps > 0 {
				vparts = append(vparts, fmt.Sprintf("%.3g fps", fps))
			}
			d.Video = strings.Join(vparts, " · ")
			d.VideoRange = videoRangeLabel(v)
		}
	}
	for i := range p.audioTracks {
		if p.audioTracks[i].Index == p.audioIdx {
			d.Audio = p.audioLabels[i]
			break
		}
	}
	switch {
	case p.subIdx == -1:
		d.Subtitle = str.SubtitlesNone
	default:
		for i := range p.subTracks {
			if p.subTracks[i].Index == p.subIdx {
				d.Subtitle = p.subLabels[i]
				break
			}
		}
		if st.BurnedIn == p.subIdx && st.BurnedIn >= 0 {
			if d.Subtitle != "" {
				d.Subtitle += " · " + str.SubtitlesBurned
			} else {
				d.Subtitle = str.SubtitlesBurned
			}
		} else if p.subOffset != 0 {
			if d.Subtitle != "" {
				d.Subtitle += " (" + d.SubOffset + ")"
			} else {
				d.Subtitle = d.SubOffset
			}
		}
	}
	return d
}

func formatSubOffset(d time.Duration) string {
	if d == 0 {
		return "0.0s"
	}
	return fmt.Sprintf("%+.1fs", d.Seconds())
}

func videoRangeLabel(v *emby.MediaStream) string {
	switch strings.ToLower(strings.TrimSpace(v.VideoRangeType)) {
	case "dovi", "dolbyvision", "dolby vision":
		return "Dolby Vision"
	case "hdr10plus", "hdr10+":
		return "HDR10+"
	case "hdr10":
		return "HDR10"
	case "hlg":
		return "HLG"
	}
	switch strings.ToLower(strings.TrimSpace(v.VideoRange)) {
	case "dovi", "dolbyvision", "dolby vision":
		return "Dolby Vision"
	case "hdr", "hdr10":
		return "HDR"
	case "hlg":
		return "HLG"
	case "sdr":
		return "SDR"
	}
	return ""
}

func formatBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}
