package player

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestZZBackWhilePlaying(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(time.Minute, time.Hour)
	if err := h.tt.Click(str.Back); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || h.p.cur != nil || !e.closed {
		t.Fatalf("back left active=%v current=%p engine closed=%v", h.p.Active(), h.p.cur, e.closed)
	}
	h.tt.Frame()
	if e.afterClose != 0 {
		t.Fatal("the frame continued into a closed engine after back")
	}
}

func TestZZBackWhileLoading(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("loading", 10*time.Minute), false)
	h.tt.Frame()
	if err := h.tt.Click(str.Back); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || h.p.cur != nil || len(h.engines) != 0 {
		t.Fatalf("loading back left active=%v current=%p engines=%d", h.p.Active(), h.p.cur, len(h.engines))
	}
	h.settle()
	if h.p.Active() || h.p.cur != nil || len(h.engines) != 0 {
		t.Fatal("a late stream answer restarted the player")
	}
}

func TestZZRepeatedPlayAndBackReleaseEachEngine(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	ids := []string{"repeat-a", "repeat-b", "repeat-c"}
	for i := range 3 {
		h.p.Play(movie(ids[i], 0), true)
		h.tt.Frame()
		h.settle()
		e := h.ready(0, time.Hour)
		if err := h.tt.Click(str.Back); err != nil {
			t.Fatal(err)
		}
		if h.p.Active() || h.p.cur != nil || !e.closed {
			t.Fatalf("round %d: active=%v current=%p engine closed=%v", i, h.p.Active(), h.p.cur, e.closed)
		}
	}
	if len(h.engines) != 3 {
		t.Fatalf("opened %d engines, want one per session", len(h.engines))
	}
}

func TestZZBackWhileFullscreen(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("fullscreen", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(0, time.Hour)
	h.tt.Key(0, ui.KeyF)
	if !h.p.fullscreen {
		t.Fatal("did not enter fullscreen")
	}
	h.tt.Advance(time.Second)
	h.tt.Move(640, 400)
	h.tt.Frame()
	if err := h.tt.Click(str.Back); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || h.p.cur != nil || !e.closed {
		t.Fatalf("fullscreen back left active=%v current=%p engine closed=%v", h.p.Active(), h.p.cur, e.closed)
	}
}
