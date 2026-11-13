package player

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/remux"
)

func TestSubtitleFallbackFromRemuxAppearsInMenu(t *testing.T) {
	files := httptest.NewServer(http.FileServer(http.Dir(filepath.Join("..", "remux", "testdata"))))
	defer files.Close()
	srv, err := remux.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	pb := &emby.Playback{
		ItemID: "good-place",
		Method: emby.DirectPlay,
		Source: emby.MediaSource{
			ID:           "ms-missing-subtitles",
			Container:    "mkv",
			RunTimeTicks: emby.DurationToTicks(10 * time.Second),
		},
		StreamURL:    files.URL + "/mkv_h264_aac_ass.mkv",
		RunTimeTicks: emby.DurationToTicks(10 * time.Second),
	}
	fallback := RemuxFallback(srv)
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return fallback(context.Background(), pb, r)
	})

	h.p.Play(movie("good-place", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(0, 10*time.Second)
	if len(h.p.subTracks) != 1 || h.p.subTracks[0].Language != "chi" {
		t.Fatalf("subtitle tracks: %+v", h.p.subTracks)
	}
	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if menu := h.tt.Menu(); !containsString(menu, "ASS") {
		t.Fatalf("subtitle menu %q has no local ASS track", menu)
	}
	if err := h.tt.ChooseMenuItem("ASS"); err != nil {
		t.Fatal(err)
	}
	h.until("the local ASS cue", func() bool {
		h.eng().pos = 2 * time.Second
		h.tt.Frame()
		return h.tt.HasText("ASS line")
	})
	if h.p.subIdx != -2 {
		t.Fatalf("selected subtitle %d, want the first local ID -2", h.p.subIdx)
	}
}

func TestRemuxFallbackTwoRealSubtitlesSwitchInMenu(t *testing.T) {
	ffmpeg := subtitleFFmpeg(t)
	dir := t.TempDir()
	sample := filepath.Join("..", "remux", "testdata", "mkv_h264_aac_ass.mkv")
	output := filepath.Join(dir, "two-languages.mkv")
	for name, text := range map[string]string{
		"chinese.srt": "1\n00:00:01,000 --> 00:00:04,000\n中文字幕\n",
		"english.srt": "1\n00:00:01,000 --> 00:00:04,000\nEnglish subtitle\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-i", sample,
		"-i", filepath.Join(dir, "chinese.srt"),
		"-i", filepath.Join(dir, "english.srt"),
		"-map", "0:v:0", "-map", "0:a:0", "-map", "1:0", "-map", "2:0",
		"-c", "copy",
		"-metadata:s:s:0", "language=chi",
		"-metadata:s:s:0", "title=Chinese",
		"-metadata:s:s:1", "language=eng",
		"-metadata:s:s:1", "title=English",
		output,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make a two-language MKV: %v\n%s", err, out)
	}

	files := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer files.Close()
	srv, err := remux.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	pb := &emby.Playback{
		ItemID:       "good-place-two-languages",
		Method:       emby.DirectPlay,
		Source:       emby.MediaSource{Container: "mkv", RunTimeTicks: emby.DurationToTicks(10 * time.Second)},
		StreamURL:    files.URL + "/two-languages.mkv",
		RunTimeTicks: emby.DurationToTicks(10 * time.Second),
	}
	fallback := RemuxFallback(srv)
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return fallback(context.Background(), pb, r)
	})

	h.p.Play(movie("good-place-two-languages", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(time.Second, 10*time.Second)
	if len(h.p.subTracks) != 2 ||
		h.p.subTracks[0].Language != "chi" || h.p.subTracks[1].Language != "eng" {
		t.Fatalf("MKV subtitle tracks: %+v", h.p.subTracks)
	}
	if h.p.subTracks[0].Index != -2 || h.p.subTracks[1].Index != -3 {
		t.Fatalf("unstable local IDs: %+v", h.p.subTracks)
	}

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Chinese"); err != nil {
		t.Fatal(err)
	}
	h.until("the Chinese MKV cue", func() bool {
		h.eng().pos = 2 * time.Second
		h.tt.Frame()
		return h.tt.HasText("中文字幕")
	})

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("English"); err != nil {
		t.Fatal(err)
	}
	h.until("the English MKV cue", func() bool {
		h.eng().pos = 2 * time.Second
		h.tt.Frame()
		return h.tt.HasText("English subtitle")
	})
	if h.tt.HasText("中文字幕") {
		t.Fatalf("old-language cue remained after switching: %q", h.tt.Texts())
	}

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem(str.SubtitlesOff); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.p.subIdx != -1 || h.tt.HasText("English subtitle") {
		t.Fatalf("subtitles off: index %d, texts %q", h.p.subIdx, h.tt.Texts())
	}
}

func TestRemuxFallbackMissingMetadataDTSReachesSubtitles(t *testing.T) {
	ffmpeg := subtitleFFmpeg(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "english.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:04,000\nEnglish from DTS fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "missing-metadata-dts.mkv")
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-i", filepath.Join("..", "remux", "testdata", "mkv_hevc_dts.mkv"),
		"-i", srt,
		"-map", "0:0", "-map", "0:1", "-map", "1:0",
		"-c", "copy",
		"-metadata:s:s:0", "language=eng",
		"-metadata:s:s:0", "title=English",
		output,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make a missing-metadata DTS/SRT MKV: %v\n%s", err, out)
	}

	files := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer files.Close()
	srv, err := remux.NewServer(remux.WithFFmpeg(ffmpeg))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	pb := &emby.Playback{
		ItemID:       "good-place-dts",
		Method:       emby.DirectPlay,
		Source:       emby.MediaSource{Container: "mkv", RunTimeTicks: emby.DurationToTicks(10 * time.Second)},
		StreamURL:    files.URL + "/missing-metadata-dts.mkv",
		RunTimeTicks: emby.DurationToTicks(10 * time.Second),
	}
	fallback := RemuxFallback(srv)
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return fallback(context.Background(), pb, r)
	})

	h.p.Play(movie("good-place-dts", 0), true)
	h.tt.Frame()
	h.settle()
	if status := h.p.Status(); status.Error {
		t.Fatalf("missing metadata plus DTS failed before the subtitle menu: %s", status.ErrorText)
	}
	h.ready(time.Second, 10*time.Second)
	if len(h.p.subTracks) != 1 || h.p.subTracks[0].Language != "eng" {
		t.Fatalf("DTS fixture subtitle tracks: %+v", h.p.subTracks)
	}
	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("English"); err != nil {
		t.Fatal(err)
	}
	h.until("the subtitle from the DTS fixture", func() bool {
		h.eng().pos = 2 * time.Second
		h.tt.Frame()
		return h.tt.HasText("English from DTS fixture")
	})

	segment := filepath.Join(t.TempDir(), "first-segment.mp4")
	if err := os.WriteFile(segment, compatFetchHLSSegment(t, h.p.Status().URL, "0"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := compatProbe(t, segment)
	foundAAC := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "audio" && stream.CodecName == "aac" {
			foundAAC = true
		}
	}
	if !foundAAC {
		t.Fatalf("first fallback segment has no AAC audio: %+v", probe.Streams)
	}
}

func TestGoodPlaceMissingMetadataDTSMenuLive(t *testing.T) {
	if os.Getenv("MYGO_GOODPLACE_LIVE_DTS") != "1" {
		t.Skip("set MYGO_GOODPLACE_LIVE_DTS=1 to read the live Good Place DTS source")
	}
	settingsPath := os.Getenv("MYGO_SETTINGS_PATH")
	if settingsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		settingsPath = filepath.Join(home, "Library", "Application Support", "MyGo Emby", "settings.json")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		DeviceID string          `json:"deviceId"`
		Servers  json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	var accounts []struct {
		ServerURL     string `json:"serverUrl"`
		ServerID      string `json:"serverId"`
		ServerName    string `json:"serverName"`
		ServerVersion string `json:"serverVersion"`
		ServerKind    string `json:"serverKind"`
		UserID        string `json:"userId"`
		UserName      string `json:"userName"`
		UserImageTag  string `json:"userImageTag"`
		TokenRef      string `json:"tokenRef"`
		Token         string `json:"token"`
	}
	if err := json.Unmarshal(settings.Servers, &accounts); err != nil {
		t.Fatalf("parse saved servers: %v", err)
	}
	var account *struct {
		ServerURL     string `json:"serverUrl"`
		ServerID      string `json:"serverId"`
		ServerName    string `json:"serverName"`
		ServerVersion string `json:"serverVersion"`
		ServerKind    string `json:"serverKind"`
		UserID        string `json:"userId"`
		UserName      string `json:"userName"`
		UserImageTag  string `json:"userImageTag"`
		TokenRef      string `json:"tokenRef"`
		Token         string `json:"token"`
	}
	for i := range accounts {
		if accounts[i].ServerURL != "" {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		t.Fatal("no saved server")
	}
	if account.Token == "" && account.TokenRef != "" {
		service := os.Getenv("MYGO_SECRET_SERVICE")
		if service == "" {
			service = "dev.jackie.mygo-emby"
		}
		token, err := mygo.Secrets.Service(service).Get(account.TokenRef)
		if err != nil {
			t.Fatalf("read saved session token: %v", err)
		}
		account.Token = string(token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := emby.RestoreClient(emby.Session{
		ServerURL: account.ServerURL, ServerID: account.ServerID, ServerName: account.ServerName,
		ServerVersion: account.ServerVersion, ServerKind: account.ServerKind,
		UserID: account.UserID, UserName: account.UserName, UserImageTag: account.UserImageTag,
		Token: account.Token,
	}, emby.DeviceInfo{Client: "MyGo Emby subtitle probe", Device: "Mac", DeviceID: settings.DeviceID, Version: "0.1.0"})
	if err != nil {
		t.Fatalf("restore client: %v", err)
	}

	pb, err := client.PlaybackInfo(ctx, "itm_f236a609def697a7e343cc06",
		emby.PlaybackOptions{SubtitleStreamIndex: emby.Index(-1)})
	if err != nil {
		t.Fatalf("playback info: %v", err)
	}
	hasDTS := false
	for _, audio := range pb.Source.Streams(emby.StreamAudio) {
		hasDTS = hasDTS || strings.EqualFold(audio.Codec, "dts")
	}
	if !hasDTS {
		t.Fatalf("default Good Place source is not the expected DTS file: %+v", pb.Source.Streams(emby.StreamAudio))
	}
	var empty *emby.MediaSource
	for i := range pb.MediaSources {
		if len(pb.MediaSources[i].MediaStreams) == 0 {
			empty = &pb.MediaSources[i]
			break
		}
	}
	if empty == nil {
		t.Fatal("the live Good Place item no longer has a source with missing MediaStreams")
	}
	explicit, err := client.PlaybackInfo(ctx, pb.ItemID, emby.PlaybackOptions{
		MediaSourceID:       empty.ID,
		SubtitleStreamIndex: emby.Index(-1),
	})
	if err != nil {
		t.Fatalf("playback info for missing-metadata source: %v", err)
	}
	if len(explicit.Source.MediaStreams) != 0 {
		t.Fatalf("live source no longer has missing MediaStreams: %d", len(explicit.Source.MediaStreams))
	}
	srv, err := remux.NewServer(remux.WithFFmpeg(subtitleFFmpeg(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	fallback := RemuxFallback(srv)
	stream, err := fallback(ctx, explicit, Request{})
	if err != nil {
		t.Fatal("live missing-metadata DTS source failed before the subtitle menu")
	}

	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(Request) (*Stream, error) {
		return stream, nil
	})
	defer h.p.Close()
	h.p.Play(movie(pb.ItemID, 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(time.Second, explicit.Runtime())
	if len(h.p.subTracks) != 2 ||
		h.p.subTracks[0].Language != "en" || h.p.subTracks[1].Language != "en" {
		t.Fatalf("live missing-metadata subtitle tracks: %+v", h.p.subTracks)
	}
	for _, track := range h.p.subTracks {
		if strings.Contains(strings.ToLower(track.Language), "zh") ||
			strings.Contains(strings.ToLower(track.Title), "chinese") {
			t.Fatalf("invented a Chinese track: %+v", h.p.subTracks)
		}
	}
	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	menu := h.tt.Menu()
	if !containsString(menu, "English") || !containsString(menu, "SDH") {
		t.Fatalf("live missing-metadata menu %q", menu)
	}
	if err := h.tt.ChooseMenuItem("English"); err != nil {
		t.Fatal(err)
	}
	if h.p.subIdx != -2 {
		t.Fatalf("live local subtitle selection = %d, want -2", h.p.subIdx)
	}

	segment := filepath.Join(t.TempDir(), "good-place-first-segment.mp4")
	if err := os.WriteFile(segment, compatFetchHLSSegment(t, h.p.Status().URL, "0"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := compatProbe(t, segment)
	foundAAC := false
	for _, stream := range probe.Streams {
		if stream.CodecType == "audio" && stream.CodecName == "aac" {
			foundAAC = true
		}
	}
	if !foundAAC {
		t.Fatalf("live first segment has no AAC audio: %+v", probe.Streams)
	}
}

func TestLocalSubtitleMenuSwitchOffAndReports(t *testing.T) {
	chinese, english := NewCueFeed(), NewCueFeed()
	tracks := localSubtitles([]remux.Track{
		{Kind: remux.KindSubtitle, Index: -1, Codec: "subrip", Language: "chi", Title: "Chinese", Supported: true},
		{Kind: remux.KindSubtitle, Index: -1, Codec: "ass", Language: "eng", Title: "English", Supported: true},
	})
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.localSubtitles = tracks
		st.Embedded = func(index int) *CueFeed {
			local, ok := st.localSubtitle(index)
			if !ok {
				return nil
			}
			switch local.FeedIndex {
			case -1:
				return chinese
			case -2:
				return english
			}
			return nil
		}
		return st, nil
	})
	log := &subtitleReports{}
	h.p.reportTo = log

	h.p.Play(movie("good-place", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(2*time.Second, 10*time.Second)
	if len(h.p.subTracks) != 2 || h.p.subTracks[0].Index != -2 || h.p.subTracks[1].Index != -3 {
		t.Fatalf("local tracks: %+v", h.p.subTracks)
	}
	log.wait(t, 1) // start, with subtitles off

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("Chinese"); err != nil {
		t.Fatal(err)
	}
	chinese.Add(cue(0, 5, "中文字幕"))
	h.tt.Frame()
	if !h.tt.HasText("中文字幕") {
		t.Fatalf("Chinese cue: %q", h.tt.Texts())
	}
	log.wait(t, 2)

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("English"); err != nil {
		t.Fatal(err)
	}
	english.Add(cue(0, 5, "English subtitle"))
	e.pos = 3 * time.Second
	h.tt.Frame()
	if !h.tt.HasText("English subtitle") || h.tt.HasText("中文字幕") {
		t.Fatalf("switched cue: %q", h.tt.Texts())
	}
	log.wait(t, 3)

	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem(str.SubtitlesOff); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.tt.HasText("English subtitle") || h.p.subIdx != -1 {
		t.Fatalf("subtitles off: index %d, texts %q", h.p.subIdx, h.tt.Texts())
	}
	log.wait(t, 4)

	reports := log.all()
	for _, report := range reports {
		if report.SubtitleStreamIndex != nil && *report.SubtitleStreamIndex < -1 {
			t.Fatalf("synthetic subtitle ID sent to the server: %+v", reports)
		}
	}
	last := reports[len(reports)-1]
	if last.SubtitleStreamIndex == nil || *last.SubtitleStreamIndex != -1 {
		t.Fatalf("subtitles off report: %+v", last)
	}
}

func TestCompleteServerSubtitlesReplaceLocalFallback(t *testing.T) {
	const externalURL = "https://server.example/external.vtt"
	locals := localSubtitles([]remux.Track{
		{Kind: remux.KindSubtitle, Index: 2, Codec: "subrip", Language: "chi", Title: "Chinese", Supported: true},
		{Kind: remux.KindSubtitle, Index: 3, Codec: "hdmv_pgs_subtitle", Language: "eng", Title: "English PGS"},
	})
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Playback.SubtitleTracks = []emby.Track{
			{Index: 2, Type: emby.StreamSubtitle, Codec: "subrip", Language: "chi", Title: "Chinese", IsText: true, URL: "https://server.example/chinese.vtt"},
			{Index: 9, Type: emby.StreamSubtitle, Codec: "subrip", Language: "eng", Title: "External", IsText: true, IsExternal: true, URL: externalURL},
		}
		st.localSubtitles = locals
		return st, nil
	})

	h.p.Play(movie("complete-metadata", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(0, 10*time.Second)
	if len(h.p.subTracks) != 2 {
		t.Fatalf("server-complete tracks were duplicated: %+v", h.p.subTracks)
	}
	if h.p.subTracks[0].Index != 2 || h.p.subTracks[1].Index != 9 ||
		h.p.subTracks[1].URL != externalURL || !h.p.subTracks[1].IsExternal {
		t.Fatalf("external server track changed: %+v", h.p.subTracks)
	}
	for _, track := range h.p.subTracks {
		if track.Index < -1 {
			t.Fatalf("local fallback duplicated with server tracks: %+v", h.p.subTracks)
		}
	}
}

func TestLocalPGSStaysListedDisabledAndNeverBurnsIn(t *testing.T) {
	tracks := localSubtitles([]remux.Track{
		{Kind: remux.KindSubtitle, Index: -1, Codec: "hdmv_pgs_subtitle", Language: "eng", Title: "English PGS", Supported: false},
	})
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Playback.Source.SupportsTranscoding = true
		st.localSubtitles = tracks
		return st, nil
	})

	h.p.Play(movie("local-pgs", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(2*time.Second, 10*time.Second)
	if len(h.p.subTracks) != 1 || h.p.subTracks[0].Index != -2 || h.p.subTracks[0].IsText || h.p.canBurnIn {
		t.Fatalf("local PGS: tracks %+v, can burn in %v", h.p.subTracks, h.p.canBurnIn)
	}
	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if menu := h.tt.Menu(); !containsString(menu, "English PGS") {
		t.Fatalf("local PGS disappeared from menu: %q", menu)
	}
	if err := h.tt.ChooseMenuItem("English PGS"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("choosing local PGS = %v, want a disabled item", err)
	}
	h.tt.CloseMenu()
	requests := len(h.src.requests())
	h.p.selectSubtitle(-2)
	if h.p.subIdx != -1 || h.p.cur.stream.BurnedIn != -1 || len(h.src.requests()) != requests {
		t.Fatalf("local PGS selected: subtitle %d, burned %d, requests %d -> %d",
			h.p.subIdx, h.p.cur.stream.BurnedIn, requests, len(h.src.requests()))
	}
	if _, ok := h.p.cur.stream.localSubtitle(-1); ok {
		t.Fatal("-1 resolved as a local subtitle instead of off")
	}
	if _, ok := h.p.cur.stream.localSubtitle(-9); ok {
		t.Fatal("an unregistered negative ID resolved as a local subtitle")
	}
}

func TestPickSubtitleUsesStableLocalIDs(t *testing.T) {
	tracks := localTrackList(localSubtitles([]remux.Track{
		{Kind: remux.KindSubtitle, Index: -1, Codec: "subrip", Language: "chi", Title: "Chinese", Supported: true},
		{Kind: remux.KindSubtitle, Index: -1, Codec: "ass", Language: "eng", Title: "English", Supported: true},
	}))
	if got := pickSubtitle(tracks, "zh", -1, false); got != -2 {
		t.Fatalf("Chinese local selection = %d, want -2", got)
	}
	if got := pickSubtitle(tracks, "en", -1, false); got != -3 {
		t.Fatalf("English local selection = %d, want -3", got)
	}
	if got := pickSubtitle(tracks, SubtitlesOff, -1, false); got != -1 {
		t.Fatalf("subtitles off = %d, want -1", got)
	}
}

type subtitleReports struct {
	mu      sync.Mutex
	reports []emby.PlaybackReport
}

func (r *subtitleReports) add(report emby.PlaybackReport) error {
	r.mu.Lock()
	r.reports = append(r.reports, report)
	r.mu.Unlock()
	return nil
}

func (r *subtitleReports) ReportPlaybackStart(_ context.Context, report emby.PlaybackReport) error {
	return r.add(report)
}

func (r *subtitleReports) ReportPlaybackProgress(_ context.Context, report emby.PlaybackReport) error {
	return r.add(report)
}

func (r *subtitleReports) ReportPlaybackStopped(_ context.Context, report emby.PlaybackReport) error {
	return r.add(report)
}

func (r *subtitleReports) StopTranscoding(context.Context, string) error { return nil }

func (r *subtitleReports) all() []emby.PlaybackReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]emby.PlaybackReport(nil), r.reports...)
}

func (r *subtitleReports) wait(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(r.all()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("reports: got %d, want %d", len(r.all()), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func subtitleFFmpeg(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"ffmpeg", "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("ffmpeg is not installed; subtitle fallback media test skipped")
	return ""
}
