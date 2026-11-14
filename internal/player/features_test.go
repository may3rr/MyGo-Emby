package player

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

func TestSubtitleDelayAndScale(t *testing.T) {
	h := newHarness(t, Config{SubtitleScale: 1.45}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(10*time.Second, time.Hour)
	h.p.subs.load(emby.NewSubtitles([]emby.Cue{cue(10, 12, "延迟测试")}))
	h.tt.Frame()
	if !h.tt.HasText("延迟测试") {
		t.Fatalf("expected cue at 10s, got %q", h.tt.Texts())
	}

	// Delaying subtitle by +1s hides the 10s..12s cue at pos=10s and shows it at pos=11.5s.
	h.tt.Key(0, ui.KeyBracketRight)
	h.tt.Key(0, ui.KeyBracketRight)
	h.tt.Frame()
	if h.p.subOffset != time.Second {
		t.Fatalf("subOffset = %v, want 1s", h.p.subOffset)
	}
	if h.tt.HasText("延迟测试") {
		t.Fatal("cue still visible after +1s delay at 10s")
	}
	e.pos = 11500 * time.Millisecond
	h.tt.Frame()
	if !h.tt.HasText("延迟测试") {
		t.Fatalf("cue not shown at 11.5s with +1s delay: %q", h.tt.Texts())
	}

	h.tt.Key(0, ui.KeyBackslash)
	h.tt.Frame()
	if h.p.subOffset != 0 {
		t.Fatalf("subOffset after reset = %v", h.p.subOffset)
	}
	if got := h.p.Result().SubtitleScale; got != 1.45 {
		t.Fatalf("SubtitleScale result = %v, want 1.45", got)
	}
}

func TestLocalSubtitleFileLoadingAndReporting(t *testing.T) {
	subPath := filepath.Join(t.TempDir(), "custom.ass")
	ass := "[Script Info]\nTitle: Local\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\nDialogue: 0,0:00:05.00,0:00:09.00,Default,,0,0,0,,本地ASS字幕\n"
	if err := os.WriteFile(subPath, []byte(ass), 0o600); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, Config{
		PickSubtitleFile: func() (string, error) { return subPath, nil },
	}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(6*time.Second, time.Hour)

	if _, ok := h.tt.Find(str.Subtitles); !ok {
		t.Fatal("subtitle button missing when PickSubtitleFile is configured")
	}
	h.p.loadLocalSubtitleFile()
	h.until("local ASS subtitle cue visible", func() bool {
		return h.tt.HasText("本地ASS字幕")
	})
	if h.p.subIdx > -1000 {
		t.Fatalf("expected negative user subtitle ID <= -1000, got %d", h.p.subIdx)
	}

	h.p.Close()
	if !h.p.Flush(time.Second) {
		t.Fatal("reports did not flush")
	}
	stopped := h.reports.last["stopped"]
	if stopped.SubtitleStreamIndex != nil && *stopped.SubtitleStreamIndex < -1 {
		t.Fatalf("server report leaked local SubtitleStreamIndex = %d", *stopped.SubtitleStreamIndex)
	}
}

func TestChaptersNavigationAndPreview(t *testing.T) {
	it := movie("chap", 0)
	it.Chapters = []emby.Chapter{
		{StartPositionTicks: 0, Name: "序幕"},
		{StartPositionTicks: emby.DurationToTicks(20 * time.Minute), Name: "正片开始"},
	}
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })

	// PlayWith explicit StartAt starts directly at chapter timestamp.
	h.p.PlayWith(it, Options{StartAt: 20 * time.Minute, HasStartAt: true})
	h.tt.Frame()
	h.settle()
	reqs := h.src.requests()
	if len(reqs) != 1 || reqs[0].Start != 20*time.Minute {
		t.Fatalf("StartAt request = %+v, want 20m", reqs)
	}
	e := h.ready(20*time.Minute, time.Hour)
	if _, ok := h.tt.Find(str.Chapters); !ok {
		t.Fatal("chapters button missing")
	}

	// Hover progress slider at 50% (30m) and verify chapter name + clock without mutating seekText.
	h.tt.Advance(300 * time.Millisecond)
	r, ok := h.tt.Find(str.Progress)
	if !ok {
		t.Fatal("progress slider missing")
	}
	h.tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if _, ok := h.tt.Find(str.ProgressPreview); !ok || h.p.seekPreviewText != "正片开始 · 30:00" || h.p.seekText.text != "30:00" {
		t.Fatalf("seek preview = %q, seekText = %q", h.p.seekPreviewText, h.p.seekText.text)
	}

	// Choose chapter 0 from the chapters menu to seek back to 0.
	if err := h.tt.Click(str.Chapters); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("序幕"); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if len(e.seeks) == 0 || e.pos != 0 {
		t.Fatalf("seek to chapter 0 failed: seeks=%v pos=%v", e.seeks, e.pos)
	}
}

func TestDiagnosticsHUDAndTokenRedaction(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Pipeline = str.PipeLocalRemux
		st.URL = "http://127.0.0.1:9999/stream.m3u8?Static=true&api_key=super-secret-token"
		st.Playback.Source = emby.MediaSource{
			ID:        "ms1",
			Container: "mkv",
			Bitrate:   12_000_000,
			Size:      4 << 30,
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "hevc", Width: 3840, Height: 2160, RealFrameRate: 23.976, VideoRangeType: "HDR10"},
				{Index: 1, Type: emby.StreamAudio, Codec: "aac", Language: "chi", DisplayTitle: "国语 AAC 5.1", IsDefault: true},
			},
		}
		return st, nil
	})
	h.p.Play(movie("diag", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(time.Minute, time.Hour)

	d := h.p.Diagnostics()
	if d.Pipeline != str.PipeLocalRemux || d.VideoRange != "HDR10" {
		t.Fatalf("Diagnostics = %+v", d)
	}
	if strings.Contains(d.URL, "super-secret-token") || !strings.Contains(d.URL, "REDACTED") {
		t.Fatalf("Diagnostics URL leaked secret or lost redaction: %q", d.URL)
	}

	h.tt.Key(0, ui.KeyI)
	h.tt.Frame()
	if !h.p.statsOpen || !h.tt.HasText(str.PipeLocalRemux) || !h.tt.HasText(str.DiagMode) {
		t.Fatalf("HUD not rendered on I key: statsOpen=%v texts=%q", h.p.statsOpen, h.tt.Texts())
	}
}

func TestErrorPanelExternalPlayerFallback(t *testing.T) {
	var gotID, gotSource string
	var gotPos time.Duration
	h := newHarness(t, Config{
		ExternalPlayerLabel: "IINA",
		OnOpenExternal: func(it *emby.Item, msID string, pos time.Duration) {
			if it != nil {
				gotID = it.ID
			}
			gotSource = msID
			gotPos = pos
		},
	}, func(r Request) (*Stream, error) {
		return nil, &UnplayableError{Reason: str.ErrNotPlayable}
	})
	h.p.PlayWith(movie("iso", 12*time.Minute), Options{MediaSourceID: "ms-bd"})
	h.tt.Frame()
	h.settle()
	if err := h.tt.Click("用 IINA 打开"); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || gotID != "iso" || gotSource != "ms-bd" || gotPos != 12*time.Minute {
		t.Fatalf("external callback active=%v id=%q src=%q pos=%v", h.p.Active(), gotID, gotSource, gotPos)
	}
}
