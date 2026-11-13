package player

import (
	"context"
	"sync"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

func TestFormatClock(t *testing.T) {
	for _, c := range []struct {
		sec  int64
		neg  bool
		want string
	}{
		{0, false, "0:00"}, {5, false, "0:05"}, {65, false, "1:05"}, {600, false, "10:00"},
		{3599, false, "59:59"}, {3600, false, "1:00:00"}, {3725, false, "1:02:05"},
		{36000 + 61, false, "10:01:01"}, {65, true, "-1:05"}, {7231, true, "-2:00:31"},
	} {
		if got := formatClock(c.sec, c.neg); got != c.want {
			t.Errorf("formatClock(%d, %v) = %q, want %q", c.sec, c.neg, got, c.want)
		}
	}
}

// The time text formats once a second: frames within the same second
// allocate nothing. Remaining time rounds up, so the two add up.
func TestClockTextCaches(t *testing.T) {
	var c clockText
	if got := c.at(61500*time.Millisecond, false); got != "1:01" {
		t.Errorf("elapsed = %q", got)
	}
	if got := c.at(61900*time.Millisecond, false); got != "1:01" {
		t.Errorf("elapsed later in the second = %q", got)
	}
	var r clockText
	if got := r.at(58500*time.Millisecond, true); got != "-0:59" {
		t.Errorf("remaining = %q", got)
	}
	d := 61 * time.Second
	if n := testing.AllocsPerRun(100, func() { d += time.Millisecond; c.at(d, false) }); n > 0.1 {
		t.Errorf("%v allocations a frame within a second", n)
	}
}

func TestSeekerMergesSteps(t *testing.T) {
	var s seeker
	t0 := time.Unix(1000, 0)
	s.step(t0, 100*time.Second, 10*time.Second)
	s.step(t0.Add(150*time.Millisecond), 100*time.Second, 10*time.Second)
	s.step(t0.Add(300*time.Millisecond), 100*time.Second, 10*time.Second)
	if pv, ok := s.preview(0); !ok || pv != 130*time.Second {
		t.Errorf("preview %v %v, want 2m10s", pv, ok)
	}
	if _, ok, wait := s.take(t0.Add(500*time.Millisecond), 100*time.Second); ok || wait != 200*time.Millisecond {
		t.Errorf("due early: ok %v wait %v", ok, wait)
	}
	// Made 400 ms after the last press, from where playback stands then.
	target, ok, _ := s.take(t0.Add(700*time.Millisecond), 101*time.Second)
	if !ok || target != 131*time.Second {
		t.Errorf("take = %v %v, want one seek to 2m11s", target, ok)
	}
	if _, ok, _ := s.take(t0.Add(time.Second), 0); ok {
		t.Error("a second seek")
	}
}

func TestSeekerGapForServerWork(t *testing.T) {
	s := seeker{minGap: 800 * time.Millisecond}
	t0 := time.Unix(1000, 0)
	s.seekTo(t0, 10*time.Second)
	if _, ok, _ := s.take(t0, 0); !ok {
		t.Fatal("the first seek waits")
	}
	s.seekTo(t0.Add(100*time.Millisecond), 20*time.Second)
	if _, ok, wait := s.take(t0.Add(100*time.Millisecond), 0); ok || wait != 700*time.Millisecond {
		t.Errorf("second seek: ok %v wait %v, want a wait of 700ms", ok, wait)
	}
	if target, ok, _ := s.take(t0.Add(800*time.Millisecond), 0); !ok || target != 20*time.Second {
		t.Errorf("second seek = %v %v", target, ok)
	}
}

func TestClampSeek(t *testing.T) {
	dur := time.Hour
	for in, want := range map[time.Duration]time.Duration{-time.Second: 0, time.Minute: time.Minute, 2 * time.Hour: dur - time.Second} {
		if got := clampSeek(in, dur); got != want {
			t.Errorf("clampSeek(%v) = %v, want %v", in, got, want)
		}
	}
}

func cue(from, to float64, text string) emby.Cue {
	return emby.Cue{Start: time.Duration(from * float64(time.Second)), End: time.Duration(to * float64(time.Second)), Text: text}
}

func sec(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func TestSubtitleTrackPlaysOn(t *testing.T) {
	var s subtitleTrack
	s.load(emby.NewSubtitles([]emby.Cue{
		cue(1, 3, "one"), cue(4, 6, "two"), cue(5, 7, "overlap"), cue(10, 12, "three"),
	}))
	for _, c := range []struct {
		at   float64
		want string
	}{
		{0, ""}, {1, "one"}, {2.9, "one"}, {3, ""}, {4.5, "two"}, {5.5, "two\noverlap"},
		{6.5, "overlap"}, {11, "three"}, {20, ""},
		{2, "one"}, // a seek back
		{11.5, "three"},
	} {
		if got := s.at(sec(c.at)); got != c.want {
			t.Errorf("at %vs: %q, want %q", c.at, got, c.want)
		}
	}
	// Frames within one cue allocate nothing.
	d := sec(10.1)
	if n := testing.AllocsPerRun(100, func() { d += time.Millisecond; s.at(d) }); n > 0.1 {
		t.Errorf("%v allocations a frame on the same cue", n)
	}
}

// A remux adds the cues of each piece it reads, ahead of playback and out
// of order, the same piece again after a seek: the track merges them in
// order, without duplicates, and shows them as playback reaches them.
func TestSubtitleTrackFollowsFeed(t *testing.T) {
	feed := NewCueFeed()
	var s subtitleTrack
	s.follow(feed)
	if !s.active() || s.at(sec(1)) != "" {
		t.Fatal("an empty feed shows something")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // from the remux's goroutine
		defer wg.Done()
		feed.Add(cue(20, 22, "later"), cue(24, 26, "last"))
	}()
	wg.Wait()
	feed.Add(cue(1, 3, "first"), cue(5, 7, "second"))
	feed.Add(cue(20, 22, "later"), cue(1, 3, "first")) // read again
	if got := s.at(sec(2)); got != "first" {
		t.Errorf("at 2s: %q", got)
	}
	if got := s.at(sec(21)); got != "later" {
		t.Errorf("at 21s: %q", got)
	}
	if len(s.cues) != 4 {
		t.Errorf("%d cues after duplicates, want 4", len(s.cues))
	}
	for i := 1; i < len(s.cues); i++ {
		if s.cues[i].Start < s.cues[i-1].Start {
			t.Fatalf("out of order: %v", s.cues)
		}
	}
	feed.Add(cue(10, 12, "middle"))
	if got := s.at(sec(11)); got != "middle" {
		t.Errorf("a cue added before the cursor: %q", got)
	}
	feed.Close()
	feed.Add(cue(30, 31, "after close"))
	if got := s.at(sec(30.5)); got != "" {
		t.Errorf("a cue added after Close shows: %q", got)
	}
}

func TestLanguageKeys(t *testing.T) {
	for in, want := range map[string]string{"chi": "zh", "zho": "zh", "zh-Hans": "zh", "ZH-TW": "zh", "eng": "en", "en": "en", "jpn": "ja", "und": "", "": "", "fin": "fin"} {
		if got := langKey(in); got != want {
			t.Errorf("langKey(%q) = %q, want %q", in, got, want)
		}
	}
	tr := emby.Track{Language: "und", Title: "简体中文"}
	if got := trackLang(&tr); got != "zh" {
		t.Errorf("a Chinese title: %q", got)
	}
}

func TestPickAudio(t *testing.T) {
	tracks := []emby.Track{
		{Index: 1, Language: "eng", IsDefault: true},
		{Index: 2, Language: "chi", Title: "国语"},
		{Index: 3, Language: "chi", Title: "粤语", IsDefault: true},
	}
	for pref, want := range map[string]int{"": 1, "en": 1, "zh": 3, "chi": 3, "fr": 1} {
		if got := pickAudio(tracks, pref, 1); got != want {
			t.Errorf("pickAudio(%q) = %d, want %d", pref, got, want)
		}
	}
	if got := pickAudio(tracks, "zh", 2); got != 2 {
		t.Errorf("the default track speaks it already: %d", got)
	}
}

func TestPickSubtitle(t *testing.T) {
	tracks := []emby.Track{
		{Index: 3, Language: "chi", IsText: false, Codec: "pgssub"},
		{Index: 4, Language: "chi", IsText: true, IsForced: true},
		{Index: 5, Language: "chi", IsText: true},
		{Index: 6, Language: "eng", IsText: true, IsForced: true, IsDefault: true},
	}
	for _, c := range []struct {
		pref     string
		def      int
		burn     bool
		want     int
		describe string
	}{
		{"", 6, false, 6, "the server's default"},
		{"", 3, false, -1, "a bitmap default where nothing burns it in"},
		{"", 3, true, 3, "a bitmap default the server burns in"},
		{SubtitlesOff, 6, false, -1, "off"},
		{"zh", -1, false, 5, "full text over forced and bitmap"},
		{"ko", 6, false, 6, "no Korean: the forced default"},
		{"ko", -1, false, -1, "no Korean, no default"},
	} {
		if got := pickSubtitle(tracks, c.pref, c.def, c.burn); got != c.want {
			t.Errorf("%s: pickSubtitle(%q, %d) = %d, want %d", c.describe, c.pref, c.def, got, c.want)
		}
	}
}

// fakeReports records reports in the order they reach the server.
type fakeReports struct {
	mu    sync.Mutex
	kinds []string
	last  map[string]emby.PlaybackReport
	stops []string
	delay time.Duration
}

func (f *fakeReports) add(kind string, r emby.PlaybackReport) error {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	if f.last == nil {
		f.last = map[string]emby.PlaybackReport{}
	}
	f.last[kind] = r
	return nil
}

func (f *fakeReports) ReportPlaybackStart(_ context.Context, r emby.PlaybackReport) error {
	return f.add("start", r)
}
func (f *fakeReports) ReportPlaybackProgress(_ context.Context, r emby.PlaybackReport) error {
	return f.add("progress:"+r.EventName, r)
}
func (f *fakeReports) ReportPlaybackStopped(_ context.Context, r emby.PlaybackReport) error {
	return f.add("stopped", r)
}
func (f *fakeReports) StopTranscoding(_ context.Context, session string) error {
	f.mu.Lock()
	f.stops = append(f.stops, session)
	f.mu.Unlock()
	return f.add("stopTranscoding", emby.PlaybackReport{})
}

func (f *fakeReports) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.kinds...)
}

func TestReporterOrderAndMerging(t *testing.T) {
	f := &fakeReports{delay: 20 * time.Millisecond}
	var wg sync.WaitGroup
	r := newReporter(f, &wg)
	r.send(reportJob{kind: reportStart})
	for _, e := range []string{"a", "b", "c", "d"} {
		r.send(reportJob{kind: reportProgress, report: emby.PlaybackReport{EventName: e}})
	}
	r.finish(emby.PlaybackReport{PositionTicks: 42}, "session-1")
	r.send(reportJob{kind: reportProgress}) // after the end: dropped
	if !waitGroup(&wg, 2*time.Second) {
		t.Fatal("the reporter did not finish")
	}
	got := f.got()
	if len(got) < 4 || got[0] != "start" || got[len(got)-2] != "stopped" || got[len(got)-1] != "stopTranscoding" {
		t.Fatalf("reports %v", got)
	}
	// Progress that piled up behind a slow request went as its latest.
	if n := len(got) - 3; n > 2 || got[len(got)-3] != "progress:d" {
		t.Errorf("progress reports %v, want the latest after at most one other", got[1:len(got)-2])
	}
	if f.last["stopped"].PositionTicks != 42 || f.stops[0] != "session-1" {
		t.Errorf("stopped %+v, transcode stops %v", f.last["stopped"], f.stops)
	}
}

func TestReporterEndWithoutStop(t *testing.T) {
	f := &fakeReports{}
	var wg sync.WaitGroup
	r := newReporter(f, &wg)
	r.end()
	if !waitGroup(&wg, time.Second) {
		t.Fatal("the reporter did not end")
	}
	if got := f.got(); len(got) != 0 {
		t.Errorf("reports %v, want none", got)
	}
}
