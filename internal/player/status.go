package player

import (
	"fmt"
	"strconv"
	"time"

	"mygo-emby/internal/emby"
)

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
		p.speedOpen, p.audioOpen, p.subOpen = false, false, false
	case "show-controls":
		p.activity = now
	case "menu-speed":
		p.activity, p.speedOpen = now, true
	case "menu-audio":
		p.activity, p.audioOpen = now, true
	case "menu-subtitles":
		p.activity, p.subOpen = now, true
	case "close-menus":
		p.speedOpen, p.audioOpen, p.subOpen = false, false, false
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
