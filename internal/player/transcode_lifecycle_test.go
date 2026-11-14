package player

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

func transcodeStream(r Request, session string) *Stream {
	st := selectedStream(r)
	st.Method = emby.Transcode
	st.Playback.Method = emby.Transcode
	st.Playback.PlaySessionID = session
	return st
}

func TestTranscodeStoppedAfterEngineFailure(t *testing.T) {
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return transcodeStream(r, "failed-session"), nil
	})
	h.p.Play(movie("item", 0), true)
	h.settle()
	e := h.ready(time.Second, time.Minute)
	e.err = errors.New("engine failed")
	h.tt.Frame()
	h.p.Close()
	if !h.p.Flush(time.Second) {
		t.Fatal("cleanup did not finish")
	}
	h.reports.mu.Lock()
	defer h.reports.mu.Unlock()
	if len(h.reports.stops) != 1 || h.reports.stops[0] != "failed-session" {
		t.Fatalf("transcode leaked after engine error: stops=%v", h.reports.stops)
	}
	if report := h.reports.last["stopped"]; report.MediaSourceID != "mkv" || report.PlaySessionID != "failed-session" {
		t.Fatalf("failure lost stopped report identity: %+v", report)
	}
}

func TestTranscodesStoppedWhenSwitchInterrupted(t *testing.T) {
	calls := 0
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		calls++
		return transcodeStream(r, fmt.Sprintf("session-%d", calls)), nil
	})
	h.p.Play(movie("item", 0), true)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.selectAudio(7)
	h.settle() // replacement has no picture yet; both streams exist
	h.p.Close()
	if !h.p.Flush(time.Second) {
		t.Fatal("cleanup did not finish")
	}
	h.reports.mu.Lock()
	defer h.reports.mu.Unlock()
	if len(h.reports.stops) != 2 {
		t.Fatalf("interrupted switch leaked a transcode: stops=%v", h.reports.stops)
	}
}

func TestSharedTranscodeSessionSurvivesPreferenceReplacement(t *testing.T) {
	h := newHarness(t, Config{AudioLang: "zh", SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return transcodeStream(r, "shared"), nil
	})
	h.p.Play(movie("item", 0), true)
	h.settle()
	h.ready(time.Second, time.Minute)
	// Flush includes cleanup. Replacing the first Stream must not stop a
	// PlaySessionID that the final Stream still uses.
	h.reportsSoFar(1)
	h.reports.mu.Lock()
	premature := append([]string(nil), h.reports.stops...)
	h.reports.mu.Unlock()
	h.p.Close()
	if !h.p.Flush(time.Second) {
		t.Fatal("cleanup did not finish")
	}
	h.reports.mu.Lock()
	defer h.reports.mu.Unlock()
	if len(premature) != 0 || len(h.reports.stops) != 1 {
		t.Fatalf("shared session stopped prematurely or twice: before=%v, after=%v", premature, h.reports.stops)
	}
}

func TestSharedTranscodeSessionWaitsForCancelledOpen(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		if r.AudioIndex != nil {
			close(entered)
			<-finish // a server that answers even after its request was cancelled
		}
		return transcodeStream(r, "shared"), nil
	})
	h.p.Play(movie("item", 0), true)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.selectAudio(7)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("switch did not request a stream")
	}
	h.p.Close()
	finishedEarly := h.p.Flush(20 * time.Millisecond)
	h.reports.mu.Lock()
	premature := append([]string(nil), h.reports.stops...)
	h.reports.mu.Unlock()
	close(finish)
	if !h.p.Flush(time.Second) {
		t.Fatal("cancelled open cleanup did not finish")
	}
	h.reports.mu.Lock()
	defer h.reports.mu.Unlock()
	if finishedEarly || len(premature) != 0 || len(h.reports.stops) != 1 {
		t.Fatalf("in-flight shared session cleanup: early=%v, before=%v, after=%v", finishedEarly, premature, h.reports.stops)
	}
}
