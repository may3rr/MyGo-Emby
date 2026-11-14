package emby

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaSegmentsAPIAndGraceful404(t *testing.T) {
	var hasPlugin bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasPlugin {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/MediaSegments/ep-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Items":[
			{"Id":"s2","ItemId":"ep-1","Type":"Outro","StartTicks":13200000000,"EndTicks":14100000000},
			{"Id":"s1","ItemId":"ep-1","Type":"Intro","StartTicks":600000000,"EndTicks":1500000000},
			{"Id":"tiny","ItemId":"ep-1","Type":"Intro","StartTicks":0,"EndTicks":5000000}
		]}`)
	}))
	defer srv.Close()

	c, err := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "tok"}, testDevice)
	if err != nil {
		t.Fatal(err)
	}

	// 404 returns nil, nil gracefully.
	segs, err := c.MediaSegments(context.Background(), "ep-1")
	if err != nil || len(segs) != 0 {
		t.Fatalf("expected graceful empty segments on 404, got %v, err=%v", segs, err)
	}

	// 200 returns sorted, filtered segments.
	hasPlugin = true
	segs, err = c.MediaSegments(context.Background(), "ep-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || segs[0].Type != SegmentIntro || segs[1].Type != SegmentOutro {
		t.Fatalf("unexpected segments: %+v", segs)
	}
	if !segs[0].Contains(90 * time.Second) || segs[0].Contains(150*time.Second) {
		t.Fatalf("Contains mismatch on %+v", segs[0])
	}
}

func TestSegmentsFromChapters(t *testing.T) {
	chapters := []Chapter{
		{Name: "Cold Open", StartPositionTicks: 0},
		{Name: "Opening", StartPositionTicks: DurationToTicks(45 * time.Second)},
		{Name: "Part A", StartPositionTicks: DurationToTicks(2*time.Minute + 15*time.Second)},
		{Name: "Part B", StartPositionTicks: DurationToTicks(12 * time.Minute)},
		{Name: "片尾 ED", StartPositionTicks: DurationToTicks(22 * time.Minute)},
		{Name: "次回予告", StartPositionTicks: DurationToTicks(23*time.Minute + 30*time.Second)},
	}
	segs := SegmentsFromChapters(chapters, 24*time.Minute)
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %+v", segs)
	}
	if segs[0].Type != SegmentIntro || segs[0].Start() != 45*time.Second || segs[0].End() != 2*time.Minute+15*time.Second {
		t.Fatalf("intro segment mismatch: %+v", segs[0])
	}
	if segs[1].Type != SegmentOutro || segs[1].Start() != 22*time.Minute || segs[1].End() != 23*time.Minute+30*time.Second {
		t.Fatalf("outro segment mismatch: %+v", segs[1])
	}
	if segs[2].Type != SegmentPreview || segs[2].End() != 24*time.Minute {
		t.Fatalf("preview segment mismatch: %+v", segs[2])
	}
}

func TestTrickplaySelectionAndTileGeometry(t *testing.T) {
	it := &Item{
		ID: "vid-1",
		Trickplay: map[string]map[string]TrickplayInfo{
			"ms-1": {
				"160": {Width: 160, Height: 90, TileWidth: 10, TileHeight: 10, ThumbnailCount: 125, Interval: 10000},
				"320": {Width: 320, Height: 180, TileWidth: 5, TileHeight: 5, ThumbnailCount: 125, Interval: 10000},
			},
		},
	}
	msID, width, info, ok := it.BestTrickplay("ms-1", 320)
	if !ok || msID != "ms-1" || width != 320 || info.Width != 320 {
		t.Fatalf("BestTrickplay = (%q, %d, %+v, %v)", msID, width, info, ok)
	}

	// At 260s -> index 26 -> 5x5=25 per sheet -> sheet 1, index 1 within sheet -> col 1, row 0.
	sheet, x, y, w, h, ok := info.TileAt(260 * time.Second)
	if !ok || sheet != 1 || x != 320 || y != 0 || w != 320 || h != 180 {
		t.Fatalf("TileAt(260s) = sheet=%d (%d,%d %dx%d) ok=%v", sheet, x, y, w, h, ok)
	}

	var gotHeader, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Emby-Token")
		gotQuery = r.URL.RawQuery
		w.Write([]byte("fake-jpeg"))
	}))
	defer srv.Close()

	c, err := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "secret-tok"}, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.FetchTrickplaySheet(context.Background(), "vid-1", "ms-1", 320, 1)
	if err != nil || string(data) != "fake-jpeg" {
		t.Fatalf("FetchTrickplaySheet = %q, err=%v", data, err)
	}
	if gotHeader != "secret-tok" || gotQuery != "MediaSourceId=ms-1" {
		t.Fatalf("unexpected auth/query: header=%q query=%q", gotHeader, gotQuery)
	}
}
