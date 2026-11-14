package player

import (
	"errors"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

func selectedStream(r Request) *Stream {
	st := directStream(r,
		emby.Track{Index: 1, Type: emby.StreamAudio, Language: "eng"},
		emby.Track{Index: 7, Type: emby.StreamAudio, Language: "chi"})
	st.Playback.MediaSourceID = "mkv"
	st.Playback.Source.SupportsTranscoding = true
	st.Playback.SubtitleTracks = []emby.Track{{Index: 9, Type: emby.StreamSubtitle, Language: "chi", NeedsBurnIn: true}}
	if r.BurnIn != nil && *r.BurnIn >= 0 {
		st.BurnedIn = *r.BurnIn
	}
	return st
}

func TestAutoSelectedVersionPersistsThroughPreferences(t *testing.T) {
	h := newHarness(t, Config{AudioLang: "zh", SubtitleLang: "zh"}, func(r Request) (*Stream, error) {
		return selectedStream(r), nil
	})
	defer h.p.Close()
	h.p.Play(movie("item", 0), true)
	h.settle()
	reqs := h.src.requests()
	if len(reqs) != 2 || reqs[0].MediaSourceID != "" || reqs[1].MediaSourceID != "mkv" ||
		reqs[1].AudioIndex == nil || *reqs[1].AudioIndex != 7 || reqs[1].BurnIn == nil || *reqs[1].BurnIn != 9 {
		t.Fatalf("preferences lost the selected version or tracks: %+v", reqs)
	}
}

func TestAutoSelectedVersionPersistsThroughSwitchesAndRetry(t *testing.T) {
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return selectedStream(r), nil
	})
	defer h.p.Close()
	h.p.Play(movie("item", 0), true)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.selectAudio(7)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.selectSubtitle(9)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.selectSubtitle(-1)
	h.settle()
	h.ready(time.Second, time.Minute)
	h.p.fail(errors.New("engine failure"), true)
	h.p.retry()
	h.settle()
	reqs := h.src.requests()
	if len(reqs) != 5 {
		t.Fatalf("requests: %+v", reqs)
	}
	for _, req := range reqs[1:] {
		if req.MediaSourceID != "mkv" {
			t.Fatalf("reopened default source: %+v", reqs)
		}
	}
}

func TestAutoSelectedVersionPersistsAfterEngineOpenFailure(t *testing.T) {
	h := newHarness(t, Config{SubtitleLang: SubtitlesOff}, func(r Request) (*Stream, error) {
		return selectedStream(r), nil
	})
	defer h.p.Close()
	open := h.p.open
	h.p.open = func(string) (engine, error) { return nil, errors.New("engine open failed") }
	h.p.Play(movie("item", 0), true)
	h.settle()
	h.p.open = open
	h.p.retry()
	h.settle()
	if reqs := h.src.requests(); len(reqs) != 2 || reqs[1].MediaSourceID != "mkv" {
		t.Fatalf("retry lost the selected source: %+v", reqs)
	}
}
