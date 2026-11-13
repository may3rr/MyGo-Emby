package player

import (
	"context"
	"image/png"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// fakeEngine stands for AVPlayer: the test says when it is ready and where
// it stands; a seek lands at once.
type fakeEngine struct {
	url                             string
	pos, dur                        time.Duration
	ready, ended, buffering, paused bool
	err                             error
	vol, rate                       float64
	seeks                           []time.Duration
	closed                          bool
	afterClose                      int
	built                           int
	// stays: a seek does not land until the test moves pos.
	stays bool
}

func (f *fakeEngine) Play()                   { f.paused = false }
func (f *fakeEngine) Pause()                  { f.paused = true }
func (f *fakeEngine) Playing() bool           { return !f.paused && !f.ended && !f.closed }
func (f *fakeEngine) Ended() bool             { return f.ended }
func (f *fakeEngine) Ready() bool             { return f.ready }
func (f *fakeEngine) Buffering() bool         { return f.buffering }
func (f *fakeEngine) Err() error              { return f.err }
func (f *fakeEngine) Position() time.Duration { return f.pos }
func (f *fakeEngine) Duration() time.Duration {
	if f.closed {
		f.afterClose++
	}
	return f.dur
}
func (f *fakeEngine) Seek(d time.Duration) {
	f.seeks = append(f.seeks, d)
	if !f.stays {
		f.pos = d
	}
}
func (f *fakeEngine) SetRate(r float64)   { f.rate = r }
func (f *fakeEngine) SetVolume(v float64) { f.vol = v }
func (f *fakeEngine) Close()              { f.closed = true }
func (f *fakeEngine) Video(c *ui.Context) ui.Element {
	if f.closed {
		f.afterClose++
	}
	f.built++
	return ui.Box(c).Label("video")
}

// fakeSource answers as told, recording what it was asked.
type fakeSource struct {
	mu     sync.Mutex
	reqs   []Request
	answer func(r Request) (*Stream, error)
}

func (s *fakeSource) Open(_ context.Context, r Request) (*Stream, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
	return s.answer(r)
}

func (s *fakeSource) requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

func directStream(r Request, audio ...emby.Track) *Stream {
	if len(audio) == 0 {
		audio = []emby.Track{{Index: 1, Type: emby.StreamAudio, Codec: "aac", Language: "eng", IsDefault: true}}
	}
	idx := audio[0].Index
	if r.AudioIndex != nil {
		idx = *r.AudioIndex
	}
	pb := &emby.Playback{ItemID: r.Item.ID, MediaSourceID: "ms-" + r.Item.ID, PlaySessionID: "ps-" + r.Item.ID,
		Method: emby.DirectPlay, AudioTracks: audio, AudioStreamIndex: idx, SubtitleStreamIndex: -1,
		RunTimeTicks: r.Item.RunTimeTicks}
	return &Stream{URL: "fake://" + r.Item.ID, Method: emby.DirectPlay, Playback: pb, AudioIndex: idx, BurnedIn: -1}
}

type harness struct {
	t       *testing.T
	p       *Player
	tt      *ui.Tester
	src     *fakeSource
	reports *fakeReports
	engines []*fakeEngine
	exits   []Result

	mu     sync.Mutex
	posted []func()
}

func newHarness(t *testing.T, cfg Config, answer func(r Request) (*Stream, error)) *harness {
	t.Helper()
	h := &harness{t: t, src: &fakeSource{answer: answer}, reports: &fakeReports{}}
	cfg.Post = func(fn func()) {
		h.mu.Lock()
		h.posted = append(h.posted, fn)
		h.mu.Unlock()
	}
	cfg.OnExit = func(r Result) { h.exits = append(h.exits, r) }
	h.p = New(cfg)
	h.p.src = h.src
	h.p.reportTo = h.reports
	h.p.open = func(url string) (engine, error) {
		e := &fakeEngine{url: url, rate: 1}
		h.engines = append(h.engines, e)
		return e, nil
	}
	h.tt = ui.NewTester(h.p.View, 1280, 820)
	return h
}

// settle runs what the player's goroutines posted, waiting for at least
// one, then builds a frame.
func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		fns := h.posted
		h.posted = nil
		h.mu.Unlock()
		if len(fns) > 0 {
			for _, fn := range fns {
				fn()
			}
			h.tt.Frame()
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal("nothing was posted")
		}
		time.Sleep(time.Millisecond)
	}
}

// until runs what the goroutines post, building frames, until cond holds.
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		h.mu.Lock()
		fns := h.posted
		h.posted = nil
		h.mu.Unlock()
		for _, fn := range fns {
			fn()
		}
		h.tt.Frame()
		if time.Now().After(deadline) {
			h.t.Fatalf("waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) eng() *fakeEngine {
	h.t.Helper()
	if len(h.engines) == 0 {
		h.t.Fatal("no engine opened")
	}
	return h.engines[len(h.engines)-1]
}

// ready makes the latest engine show its first picture at pos.
func (h *harness) ready(pos, dur time.Duration) *fakeEngine {
	e := h.eng()
	e.ready, e.pos, e.dur = true, pos, dur
	h.tt.Frame()
	return e
}

// reportsSoFar waits for the reports queued so far to reach the server.
func (h *harness) reportsSoFar(want int) []string {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := h.reports.got()
		if len(got) >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(time.Millisecond)
	}
}

func movie(id string, resume time.Duration) *emby.Item {
	return &emby.Item{ID: id, Name: "Movie " + id, Type: emby.TypeMovie, RunTimeTicks: emby.DurationToTicks(2 * time.Hour),
		UserData: &emby.UserData{PlaybackPositionTicks: emby.DurationToTicks(resume)}}
}

func TestPlayReportsAndExits(t *testing.T) {
	h := newHarness(t, Config{Volume: 0.6}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	if !h.p.Active() || !h.tt.HasText("Movie m1") || !h.p.Status().Loading {
		t.Fatalf("loading: active %v, texts %q", h.p.Active(), h.tt.Texts())
	}
	h.settle()
	e := h.eng()
	if e.url != "fake://m1" || e.vol != 0 {
		t.Errorf("engine %q at volume %v before its first picture, want silent", e.url, e.vol)
	}
	h.ready(0, 2*time.Hour)
	if s := h.p.Status(); !s.Ready || e.vol != 0.6 {
		t.Fatalf("ready %v, volume %v", s.Ready, e.vol)
	}
	if got := h.reportsSoFar(1); len(got) != 1 || got[0] != "start" {
		t.Fatalf("reports %v, want start", got)
	}

	e.pos = 11 * time.Second
	h.tt.Advance(11 * time.Second)
	h.tt.Frame()
	h.reportsSoFar(2) // progress queued together merge: let this one go first
	h.tt.Key(0, ui.KeySpace)
	if !e.paused {
		t.Fatal("Space did not pause")
	}
	got := h.reportsSoFar(3)
	if !slices.Contains(got, "progress:TimeUpdate") || got[len(got)-1] != "progress:Pause" {
		t.Errorf("reports %v, want a timed progress then a pause", got)
	}
	if r := h.reports.last["progress:Pause"]; !r.IsPaused || r.VolumeLevel != 60 || r.ItemID != "m1" || r.PlaySessionID != "ps-m1" {
		t.Errorf("pause report %+v", r)
	}

	h.tt.Key(0, ui.KeyEscape)
	if h.p.Active() || !e.closed || len(h.exits) != 1 {
		t.Fatalf("Escape: active %v, closed %v, exits %v", h.p.Active(), e.closed, h.exits)
	}
	if r := h.exits[0]; r.ItemID != "m1" || r.Position != 11*time.Second || r.Volume != 0.6 || r.Finished {
		t.Errorf("result %+v", r)
	}
	if !h.p.Flush(2 * time.Second) {
		t.Fatal("reports still pending")
	}
	if r := h.reports.last["stopped"]; r.PositionTicks != emby.DurationToTicks(11*time.Second) {
		t.Errorf("stopped at %v", emby.TicksToDuration(r.PositionTicks))
	}
}

// A resume plays from the start, silent and covered, seeks on the first
// picture, and shows once there; the volume set meanwhile waits.
func TestResumeSeeksAfterFirstPicture(t *testing.T) {
	h := newHarness(t, Config{Volume: 0.5}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 30*time.Minute), false)
	h.tt.Frame()
	h.settle()
	e := h.eng()
	e.dur = 2 * time.Hour
	h.tt.Frame()
	if len(e.seeks) != 0 {
		t.Fatalf("seeked before the first picture: %v", e.seeks)
	}
	e.ready, e.pos, e.stays = true, 2*time.Second, true
	h.tt.Frame()
	if !slices.Equal(e.seeks, []time.Duration{30 * time.Minute}) {
		t.Fatalf("seeks %v, want one to 30m", e.seeks)
	}
	// The seek takes a while: playback stands short of it meanwhile.
	e.pos = 3 * time.Second
	h.tt.Key(0, ui.KeyUp)
	h.tt.Frame()
	if h.p.Status().Ready || e.vol != 0 {
		t.Fatalf("before landing: ready %v, volume %v", h.p.Status().Ready, e.vol)
	}
	e.pos = 30 * time.Minute
	h.tt.Frame()
	if !h.p.Status().Ready || e.vol != 0.55 {
		t.Fatalf("after landing: ready %v, volume %v, want 0.55", h.p.Status().Ready, e.vol)
	}
	h.reportsSoFar(1)
	if r := h.reports.last["start"]; r.PositionTicks != emby.DurationToTicks(30*time.Minute) {
		t.Errorf("start reported at %v", emby.TicksToDuration(r.PositionTicks))
	}
}

func TestControlsHideAndPausedIsIdle(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(0, time.Hour)
	if _, ok := h.tt.Find(str.Pause); !h.p.Status().ControlsShown || !ok {
		t.Fatalf("controls at start: %v", h.p.Status().ControlsShown)
	}
	h.tt.Advance(3200 * time.Millisecond)
	if h.p.Status().ControlsShown {
		t.Fatal("controls still shown after 3 s")
	}
	if _, ok := h.tt.Find(str.Pause); ok {
		t.Error("the pause button is still there")
	}
	h.tt.Move(600, 400)
	if !h.p.Status().ControlsShown {
		t.Fatal("moving the pointer did not show the controls")
	}
	h.tt.Key(0, ui.KeySpace)
	h.tt.Advance(10 * time.Second)
	if !h.p.Status().ControlsShown {
		t.Error("controls hid while paused")
	}
	if h.tt.FramePending() {
		t.Error("a paused player asks for frames")
	}
}

func TestFullscreenStageMovementAndControlClicks(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	h.ready(0, time.Hour)
	h.tt.Key(0, ui.KeyF)
	if !h.p.fullscreen {
		t.Fatal("fullscreen shortcut did not enter fullscreen")
	}
	for _, point := range [][2]float32{{200, 200}, {900, 400}, {600, 300}} {
		h.tt.Advance(4 * time.Second)
		if h.p.Status().ControlsShown {
			t.Fatal("controls did not hide")
		}
		h.tt.Move(point[0], point[1])
		if !h.p.Status().ControlsShown {
			t.Fatal("fullscreen motion did not wake controls")
		}
		if _, ok := h.tt.Find(str.Pause); !ok {
			t.Fatal("pause control missing after motion")
		}
	}
	// Clicking a control must not also trigger the stage's play/pause action.
	if err := h.tt.Click(str.Pause); err != nil {
		t.Fatal(err)
	}
	if !h.p.userPaused {
		t.Fatal("pause button did not pause exactly once")
	}
	h.tt.Advance(10 * time.Second)
	if !h.p.Status().ControlsShown {
		t.Fatal("paused controls disappeared")
	}
	if err := h.tt.Click(str.Play); err != nil {
		t.Fatal(err)
	}
	if h.p.userPaused {
		t.Fatal("play button did not resume exactly once")
	}
}

func TestProgressPreviewDoesNotSeekUntilReleased(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("preview", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(0, time.Hour)
	h.tt.Advance(300 * time.Millisecond) // let the control bar's entrance finish
	r, ok := h.tt.Find(str.Progress)
	if !ok {
		t.Fatal("progress slider missing")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	h.tt.Move(x, y)
	if _, ok := h.tt.Find(str.ProgressPreview); !ok || h.p.seekText.text != "30:00" {
		t.Fatalf("midpoint time preview=%q", h.p.seekText.text)
	}
	if len(e.seeks) != 0 {
		t.Fatal("hovering caused a seek")
	}
	h.tt.Press(x, y)
	x = r.X + 8 + (r.W-16)*.75
	h.tt.Move(x, y)
	if _, ok := h.tt.Find(str.ProgressPreview); !ok || h.p.seekText.text != "45:00" {
		t.Fatalf("drag destination=%q", h.p.seekText.text)
	}
	if len(e.seeks) != 0 {
		t.Fatal("dragging caused seeks before release")
	}
	if path := os.Getenv("MYGO_PLAYER_PREVIEW_SHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, h.tt.Image())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	h.tt.Release(x, y)
	h.tt.Frame()
	if len(e.seeks) != 1 || e.seeks[0] < 2699*time.Second || e.seeks[0] > 2701*time.Second {
		t.Fatalf("release seeks=%v, want one near 45 minutes", e.seeks)
	}
	h.tt.Move(200, 200)
	if _, ok := h.tt.Find(str.ProgressPreview); ok {
		t.Fatal("preview stayed after leaving slider")
	}
}

func TestCancellingProgressDragDoesNotJump(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("cancel-seek", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(2*time.Minute, time.Hour)
	h.tt.Advance(300 * time.Millisecond)
	r, ok := h.tt.Find(str.Progress)
	if !ok {
		t.Fatal("progress slider missing")
	}
	h.tt.Press(r.X+r.W/2, r.Y+r.H/2)
	h.tt.Move(r.X+r.W*.75, r.Y+r.H/2)
	h.tt.CancelPress()
	h.tt.Frame()
	if len(e.seeks) != 0 || e.pos != 2*time.Minute {
		t.Fatalf("cancelled drag jumped: seeks=%v pos=%v", e.seeks, e.pos)
	}
}

func TestArrowKeysMergeIntoOneSeek(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(100*time.Second, time.Hour)
	for range 3 {
		h.tt.Key(0, ui.KeyRight)
		h.tt.Advance(100 * time.Millisecond)
	}
	if len(e.seeks) != 0 {
		t.Fatalf("seeked while keys came: %v", e.seeks)
	}
	h.tt.Advance(450 * time.Millisecond)
	if !slices.Equal(e.seeks, []time.Duration{130 * time.Second}) {
		t.Errorf("seeks %v, want one to 2m10s", e.seeks)
	}
	h.tt.Key(0, ui.KeyLeft)
	h.tt.Advance(time.Second)
	if len(e.seeks) != 2 || e.seeks[1] != 120*time.Second {
		t.Errorf("seeks %v, want then 2m0s", e.seeks)
	}
}

func TestUnplayableShowsErrorPanel(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) {
		return nil, &UnplayableError{Reason: str.ErrNotPlayable}
	})
	h.p.Play(movie("mkv", 10*time.Minute), false)
	h.tt.Frame()
	h.settle()
	if !h.tt.HasText(str.ErrTitle) || !h.tt.HasText(str.ErrNotPlayable) || h.tt.HasText(str.Retry) {
		t.Fatalf("texts %q", h.tt.Texts())
	}
	if len(h.engines) != 0 {
		t.Error("an engine opened")
	}
	if err := h.tt.Click(str.Close); err != nil {
		t.Fatal(err)
	}
	if h.p.Active() || len(h.exits) != 1 || h.exits[0].Position != 10*time.Minute {
		t.Errorf("after close: active %v, exits %+v", h.p.Active(), h.exits)
	}
	h.p.Flush(time.Second)
	if got := h.reports.got(); len(got) != 0 {
		t.Errorf("reports %v for an item that never played", got)
	}
}

// Leaving before the first picture reports nothing, so the server keeps
// the resume point.
func TestLeavingBeforeStartReportsNothing(t *testing.T) {
	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 20*time.Minute), false)
	h.tt.Frame()
	h.settle()
	h.p.Close()
	if !h.p.Flush(time.Second) || len(h.reports.got()) != 0 || !h.eng().closed {
		t.Errorf("reports %v, engine closed %v", h.reports.got(), h.eng().closed)
	}
	if r := h.p.Result(); r.Position != 0 && r.ItemID != "" {
		t.Errorf("result %+v", r)
	}
}

func TestUpNextPlaysNextEpisode(t *testing.T) {
	one, two := 1, 2
	ep1 := &emby.Item{ID: "e1", Name: "Pilot", Type: emby.TypeEpisode, SeriesID: "s", SeriesName: "Show", IndexNumber: &one, ParentIndexNumber: &one, RunTimeTicks: emby.DurationToTicks(30 * time.Minute)}
	ep2 := &emby.Item{ID: "e2", Name: "Second", Type: emby.TypeEpisode, SeriesID: "s", SeriesName: "Show", IndexNumber: &two, ParentIndexNumber: &one, RunTimeTicks: emby.DurationToTicks(30 * time.Minute)}
	h := newHarness(t, Config{AutoNext: true}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.nextEpisode = func(_ context.Context, it *emby.Item) (*emby.Item, error) {
		if it.ID == "e1" {
			return ep2, nil
		}
		return nil, nil
	}
	h.p.Play(ep1, true)
	h.tt.Frame()
	if !h.tt.HasText("S1E1 · Pilot") || !h.tt.HasText("Show") {
		t.Fatalf("title: %q", h.tt.Texts())
	}
	h.until("the stream and the next episode", func() bool { return len(h.engines) == 1 && h.p.nextItem != nil })
	e := h.ready(0, 30*time.Minute)
	if _, ok := h.tt.Find(str.NextEpisode); !ok {
		t.Error("no next episode button")
	}
	e.pos = 30*time.Minute - 10*time.Second
	h.tt.Frame()
	if !h.tt.HasText(str.UpNext) || !h.tt.HasText("S1E2 · Second") || !h.tt.HasText("10 "+str.UpNextIn) {
		t.Fatalf("countdown: %q", h.tt.Texts())
	}
	h.until("the prefetched stream", func() bool { return h.p.upNext.stream != nil })
	reqs := h.src.requests()
	if len(reqs) != 2 || reqs[1].Item.ID != "e2" {
		t.Fatalf("requests %+v, want the next episode prefetched", reqs)
	}
	e.ended = true
	h.tt.Frame()
	if !e.closed || h.eng().url != "fake://e2" || len(h.src.requests()) != 2 {
		t.Fatalf("after the end: old closed %v, playing %q, requests %d", e.closed, h.eng().url, len(h.src.requests()))
	}
	if !h.tt.HasText("S1E2 · Second") || len(h.exits) != 0 {
		t.Errorf("texts %q, exits %v", h.tt.Texts(), h.exits)
	}
	h.p.Flush(50 * time.Millisecond) // e1's reports went; e2's reporter runs on
	if r := h.reports.last["stopped"]; r.ItemID != "e1" || r.PositionTicks < emby.DurationToTicks(30*time.Minute) {
		t.Errorf("e1 stopped %+v", r)
	}
}

// Another audio track is another stream; the old one stays on screen,
// paused, until the new one shows.
func TestAudioSwitchKeepsPictureUntilReady(t *testing.T) {
	tracks := []emby.Track{
		{Index: 1, Type: emby.StreamAudio, Codec: "aac", Language: "chi", IsDefault: true, DisplayTitle: "国语"},
		{Index: 2, Type: emby.StreamAudio, Codec: "ac3", Language: "eng", DisplayTitle: "English"},
	}
	h := newHarness(t, Config{Volume: 0.8}, func(r Request) (*Stream, error) { return directStream(r, tracks...), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	old := h.ready(5*time.Minute, time.Hour)
	if _, ok := h.tt.Find(str.Audio); !ok {
		t.Fatal("no audio button with two tracks")
	}
	h.p.selectAudio(2)
	h.tt.Frame()
	if !old.paused || old.closed {
		t.Fatalf("old stream: paused %v, closed %v", old.paused, old.closed)
	}
	h.settle()
	reqs := h.src.requests()
	if r := reqs[len(reqs)-1]; r.AudioIndex == nil || *r.AudioIndex != 2 || r.Start != 5*time.Minute {
		t.Fatalf("request %+v", r)
	}
	nu := h.eng()
	if nu == old || nu.vol != 0 || old.closed || old.built == 0 {
		t.Fatalf("while loading: new volume %v, old closed %v", nu.vol, old.closed)
	}
	builtOld := old.built
	h.tt.Frame()
	if old.built == builtOld {
		t.Error("the old picture is not shown while the new stream loads")
	}
	nu.ready, nu.dur = true, time.Hour
	nu.pos = 5 * time.Minute
	h.tt.Frame()
	if !old.closed || nu.vol != 0.8 || nu.paused || h.p.audioIdx != 2 {
		t.Errorf("after: old closed %v, new volume %v paused %v, audio %d", old.closed, nu.vol, nu.paused, h.p.audioIdx)
	}
	if got := h.reportsSoFar(2); got[len(got)-1] != "progress:AudioTrackChange" {
		t.Errorf("reports %v", got)
	}
}

func TestSubtitlesFromServerAndFeed(t *testing.T) {
	subs := []emby.Track{
		{Index: 3, Type: emby.StreamSubtitle, Codec: "subrip", Language: "chi", IsText: true, URL: "https://server/sub3.vtt"},
		{Index: 4, Type: emby.StreamSubtitle, Codec: "ass", Language: "eng", IsText: true},
	}
	feed := NewCueFeed()
	h := newHarness(t, Config{SubtitleLang: "zh"}, func(r Request) (*Stream, error) {
		st := directStream(r)
		st.Playback.SubtitleTracks = subs
		st.Embedded = func(index int) *CueFeed {
			if index == 4 {
				return feed
			}
			return nil
		}
		return st, nil
	})
	h.p.fetchSubs = func(_ context.Context, url string) (*emby.Subtitles, error) {
		if url != "https://server/sub3.vtt" {
			t.Errorf("fetched %q", url)
		}
		return emby.NewSubtitles([]emby.Cue{cue(60, 64, "第一句")}), nil
	}
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.until("the stream and its subtitle", func() bool { return len(h.engines) == 1 && len(h.p.subs.cues) > 0 })
	e := h.ready(61*time.Second, time.Hour)
	if !h.tt.HasText("第一句") || h.p.subIdx != 3 {
		t.Fatalf("subtitle %d, texts %q", h.p.subIdx, h.tt.Texts())
	}
	e.pos = 65 * time.Second
	h.tt.Frame()
	if h.tt.HasText("第一句") {
		t.Error("the line stays after its end")
	}
	// The embedded track, which the stream fills as it reads.
	h.p.selectSubtitle(4)
	feed.Add(cue(66, 70, "from the file"))
	e.pos = 67 * time.Second
	h.tt.Frame()
	if !h.tt.HasText("from the file") {
		t.Errorf("fed cue: %q", h.tt.Texts())
	}
	h.p.selectSubtitle(-1)
	h.tt.Frame()
	if h.tt.HasText("from the file") {
		t.Error("subtitles off still shows a line")
	}
}

// The steady frame of a playing video, controls hidden: how many
// allocations the player's view adds to an empty view's.
func TestSteadyFrameAllocations(t *testing.T) {
	base := ui.NewTester(func(c *ui.Context) { ui.Box(c).Fill() }, 1280, 820)
	baseline := testing.AllocsPerRun(50, base.Frame)

	h := newHarness(t, Config{}, func(r Request) (*Stream, error) { return directStream(r), nil })
	h.p.Play(movie("m1", 0), true)
	h.tt.Frame()
	h.settle()
	e := h.ready(time.Minute, time.Hour)
	h.tt.Advance(4 * time.Second) // the controls hide
	if h.p.Status().ControlsShown {
		t.Fatal("controls shown")
	}
	frame := func() { e.pos += 20 * time.Millisecond; h.tt.Advance(20 * time.Millisecond); h.tt.Frame() }
	hidden := testing.AllocsPerRun(50, frame)
	h.p.subs.load(emby.NewSubtitles([]emby.Cue{cue(0, 7200, "一行字幕")}))
	withSub := testing.AllocsPerRun(50, frame)
	h.tt.Move(640, 400)
	shown := testing.AllocsPerRun(50, func() { h.p.activity = h.tt.Now(); frame() })
	t.Logf("allocations a frame: empty view %.0f; player, controls hidden %.0f, with a subtitle %.0f, controls shown %.0f",
		baseline, hidden, withSub, shown)
	if hidden-baseline > 40 {
		t.Errorf("the hidden player adds %.0f allocations a frame", hidden-baseline)
	}
}

func TestTitles(t *testing.T) {
	three, one := 3, 1
	ep := &emby.Item{Type: emby.TypeEpisode, Name: "集名", IndexNumber: &three, ParentIndexNumber: &one, SeriesName: "剧"}
	if title, series := titles(ep); title != "S1E3 · 集名" || series != "剧" {
		t.Errorf("episode: %q %q", title, series)
	}
	if title, series := titles(&emby.Item{Type: emby.TypeMovie, Name: "电影"}); title != "电影" || series != "" {
		t.Errorf("movie: %q %q", title, series)
	}
	if !strings.HasPrefix(speedLabels[0], "0.5") || speedLabels[2] != str.NormalSpeed {
		t.Errorf("speed labels %q", speedLabels)
	}
}
