package player

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

func TestSkipIntroFromMediaSegmentsAndChapterFallback(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.fetchSegments = func(ctx context.Context, itemID string) ([]emby.MediaSegment, error) {
		return []emby.MediaSegment{
			{
				ID:         "seg-intro",
				ItemID:     itemID,
				Type:       emby.SegmentIntro,
				StartTicks: emby.DurationToTicks(30 * time.Second),
				EndTicks:   emby.DurationToTicks(2 * time.Minute),
			},
		}, nil
	}

	h.p.Play(movie("ep1", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(45*time.Second, 24*time.Minute)
	h.until("skip intro button shown", func() bool { return h.tt.HasText(str.SkipIntro) })

	// Dismissing hides the skip prompt for the current stay inside the segment.
	if err := h.tt.Click(str.Dismiss); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if h.tt.HasText(str.SkipIntro) {
		t.Fatal("skip intro remained visible after dismiss")
	}

	// Leaving the segment and seeking back into it re-arms the prompt, and 'S' skips to End.
	e.pos = 3 * time.Minute
	h.tt.Advance(300 * time.Millisecond)
	e.pos = 50 * time.Second
	h.tt.Advance(300 * time.Millisecond)
	if !h.tt.HasText(str.SkipIntro) {
		t.Fatal("skip intro did not re-appear on re-entering intro")
	}
	h.tt.Key(0, ui.KeyS)
	h.tt.Frame()
	if e.pos != 2*time.Minute {
		t.Fatalf("KeyS seeked to %v, want 2m", e.pos)
	}

	// Chapter fallback when server returns no MediaSegments.
	h.p.fetchSegments = func(ctx context.Context, itemID string) ([]emby.MediaSegment, error) {
		return nil, nil
	}
	ep2 := movie("ep2", 0)
	ep2.RunTimeTicks = emby.DurationToTicks(24 * time.Minute)
	ep2.Chapters = []emby.Chapter{
		{Name: "Opening", StartPositionTicks: emby.DurationToTicks(10 * time.Second)},
		{Name: "Main Story", StartPositionTicks: emby.DurationToTicks(100 * time.Second)},
		{Name: "Ending Credits", StartPositionTicks: emby.DurationToTicks(22 * time.Minute)},
	}
	h.p.Play(ep2, true)
	h.tt.Frame()
	h.settle()
	e2 := h.ready(22*time.Minute+15*time.Second, 24*time.Minute)
	if !h.tt.HasText(str.SkipOutro) {
		t.Fatalf("chapter fallback did not show SkipOutro: %q", h.tt.Texts())
	}
	if err := h.tt.Click(str.SkipOutro); err != nil {
		t.Fatal(err)
	}
	h.tt.Frame()
	if e2.pos < 23*time.Minute+50*time.Second {
		t.Fatalf("SkipOutro seeked to %v, want near 24m", e2.pos)
	}
}

func TestTrickplayScrubbingThumbnailPreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for y := 0; y < 90; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{R: 40, G: 120, B: 220, A: 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	sheetBytes := pngBuf.Bytes()

	it := movie("tp1", 0)
	it.Trickplay = map[string]map[string]emby.TrickplayInfo{
		"ms-1": {
			"160": {
				Width: 160, Height: 90,
				TileWidth: 2, TileHeight: 2,
				ThumbnailCount: 360, Interval: 10000,
			},
		},
	}

	var fetchedSheets int
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Playback.MediaSourceID = "ms-1"
		return st, nil
	})
	h.p.fetchTrickSheet = func(ctx context.Context, itemID, msID string, width, sheet int) ([]byte, error) {
		fetchedSheets++
		return sheetBytes, nil
	}

	h.p.Play(it, true)
	h.tt.Frame()
	h.settle()
	h.ready(time.Minute, time.Hour)

	r, ok := h.tt.Find(str.Progress)
	if !ok {
		t.Fatal("progress slider missing")
	}
	// First hover triggers async sheet decode; once settled, next hover/frame renders the tile.
	h.tt.Move(r.X+r.W*0.25, r.Y+r.H/2)
	h.until("trickplay sheet decoded", func() bool {
		h.tt.Move(r.X+r.W*0.25, r.Y+r.H/2)
		return h.p.seekThumbShown
	})
	if fetchedSheets == 0 {
		t.Fatal("expected trickplay sheet fetch")
	}
	if _, ok := h.tt.Find(str.ProgressPreview); !ok {
		t.Fatal("seek preview missing with trickplay thumbnail")
	}
}

func TestBitmapSubtitleAndTopBarExternalHandoff(t *testing.T) {
	var handoffID, handoffSource string
	var handoffPos time.Duration
	h := newHarness(t, Config{
		ExternalPlayerLabel: "IINA",
		OnOpenExternal: func(it *emby.Item, msID string, pos time.Duration) {
			if it != nil {
				handoffID = it.ID
			}
			handoffSource = msID
			handoffPos = pos
		},
	}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Playback.MediaSourceID = "ms-pgs"
		st.Playback.Source.SupportsTranscoding = false
		st.Playback.SubtitleTracks = []emby.Track{
			{Index: 3, Language: "chi", Title: "简中蓝光原盘", Codec: "pgssub", IsText: false},
		}
		return st, nil
	})

	h.p.Play(movie("bd1", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(8*time.Minute, time.Hour)

	// Top bar exposes direct external player handoff button during playback.
	if _, ok := h.tt.Find("用 IINA 打开"); !ok {
		t.Fatal("top-bar external player button missing")
	}

	// Subtitle menu exposes 1-click external handoff when PGS subtitles cannot be burned in.
	if err := h.tt.Click(str.Subtitles); err != nil {
		t.Fatal(err)
	}
	if err := h.tt.ChooseMenuItem("用 IINA 打开以渲染图形字幕"); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || handoffID != "bd1" || handoffSource != "ms-pgs" || handoffPos != 8*time.Minute {
		t.Fatalf("unexpected PGS external handoff: active=%v id=%q src=%q pos=%v", h.p.Active(), handoffID, handoffSource, handoffPos)
	}
}
