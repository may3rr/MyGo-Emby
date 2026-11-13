package player

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mygo-emby/internal/emby"
)

// CueFeed takes subtitle cues as they come, from any goroutine: a stream
// that extracts the subtitles of the file itself (a local remux) adds the
// cues of each piece it reads, ahead of playback and in any order, again
// for a piece read twice. The player merges them into the track it shows
// at its next frame. Add never blocks on the view and asks for no frame;
// the cues show as playback reaches them.
type CueFeed struct {
	mu      sync.Mutex
	pending []emby.Cue
	dirty   atomic.Bool
	closed  atomic.Bool
}

// NewCueFeed returns an empty feed.
func NewCueFeed() *CueFeed { return &CueFeed{} }

// Add adds cues; duplicates of cues already added are dropped when merged.
// Safe for concurrent use. Cues added after Close are dropped.
func (f *CueFeed) Add(cues ...emby.Cue) {
	if f == nil || len(cues) == 0 || f.closed.Load() {
		return
	}
	f.mu.Lock()
	f.pending = append(f.pending, cues...)
	f.mu.Unlock()
	f.dirty.Store(true)
}

// Close drops what is pending and ignores what comes later.
func (f *CueFeed) Close() {
	if f == nil {
		return
	}
	f.closed.Store(true)
	f.mu.Lock()
	f.pending = nil
	f.mu.Unlock()
}

// take hands over the cues added since the last take, or nil. It costs an
// atomic load when nothing came.
func (f *CueFeed) take(buf []emby.Cue) []emby.Cue {
	if f == nil || !f.dirty.Load() {
		return nil
	}
	f.mu.Lock()
	f.dirty.Store(false)
	out := append(buf[:0], f.pending...)
	f.pending = f.pending[:0]
	f.mu.Unlock()
	return out
}

// subtitleTrack is the subtitle the view shows: cues sorted by start, read
// through a cursor that moves on as playback does. Only the main thread
// touches it.
//
// Finding the text at a time costs O(1) while playback runs on (the cursor
// steps past the cues that started), O(log n) after a seek, and walks back
// only over the cues still showing, at most a few. The text is joined
// again only when the cues showing change, so a frame showing the same
// line allocates nothing.
type subtitleTrack struct {
	cues   []emby.Cue
	maxEnd []time.Duration // maxEnd[i] = max(cues[0..i].End)
	feed   *CueFeed
	buf    []emby.Cue

	// next is the cursor: the first cue starting after the last time asked.
	next int
	// shown tells the cues of text: first and last index and how many,
	// or count 0 for none.
	first, last, count int
	text               string
}

// reset empties the track and drops its feed.
func (s *subtitleTrack) reset() {
	*s = subtitleTrack{buf: s.buf[:0]}
}

// load replaces the cues with a parsed subtitle (sorted already).
func (s *subtitleTrack) load(sub *emby.Subtitles) {
	s.reset()
	if sub == nil {
		return
	}
	s.cues = sub.Cues
	s.rebuildMaxEnd(0)
}

// follow has the track show what feed brings, as it brings it.
func (s *subtitleTrack) follow(feed *CueFeed) {
	s.reset()
	s.feed = feed
}

// active reports whether a subtitle is chosen (cues, or a feed for them).
func (s *subtitleTrack) active() bool { return len(s.cues) > 0 || s.feed != nil }

// merge takes the feed's new cues into the sorted track, dropping
// duplicates. It returns whether anything changed.
func (s *subtitleTrack) merge() bool {
	in := s.feed.take(s.buf)
	s.buf = in[:0]
	if len(in) == 0 {
		return false
	}
	from := len(s.cues)
	for _, c := range in {
		if c.End <= c.Start || c.Text == "" {
			continue
		}
		i := sort.Search(len(s.cues), func(i int) bool {
			a := s.cues[i]
			return a.Start > c.Start || a.Start == c.Start && a.End >= c.End
		})
		dup := false
		for j := i; j < len(s.cues) && s.cues[j].Start == c.Start; j++ {
			if s.cues[j].End == c.End && s.cues[j].Text == c.Text {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		s.cues = append(s.cues, emby.Cue{})
		copy(s.cues[i+1:], s.cues[i:])
		s.cues[i] = c
		from = min(from, i)
	}
	if from == len(s.cues) {
		return false
	}
	s.rebuildMaxEnd(from)
	// Indexes moved: the cursor and the text are found again.
	s.next, s.count, s.first, s.last = 0, -1, 0, 0
	return true
}

func (s *subtitleTrack) rebuildMaxEnd(from int) {
	if cap(s.maxEnd) < len(s.cues) {
		m := make([]time.Duration, len(s.cues), len(s.cues)+len(s.cues)/2+16)
		copy(m, s.maxEnd)
		s.maxEnd = m
	}
	s.maxEnd = s.maxEnd[:len(s.cues)]
	var m time.Duration
	if from > 0 {
		m = s.maxEnd[from-1]
	}
	for i := from; i < len(s.cues); i++ {
		m = max(m, s.cues[i].End)
		s.maxEnd[i] = m
	}
}

// at returns the text showing at t: the cues with Start <= t < End, in
// start order, one line each, or "".
func (s *subtitleTrack) at(t time.Duration) string {
	if s.feed != nil {
		s.merge()
	}
	n := len(s.cues)
	if n == 0 {
		return ""
	}
	// Move the cursor: on while cues start, back after a seek backward.
	switch {
	case s.next < n && s.cues[s.next].Start <= t:
		steps := 0
		for s.next < n && s.cues[s.next].Start <= t {
			s.next++
			if steps++; steps > 8 {
				s.next = sort.Search(n, func(i int) bool { return s.cues[i].Start > t })
				break
			}
		}
	case s.next > 0 && s.cues[s.next-1].Start > t:
		s.next = sort.Search(n, func(i int) bool { return s.cues[i].Start > t })
	}
	// The cues showing are before the cursor and end after t.
	first, last, count := 0, 0, 0
	for i := s.next - 1; i >= 0 && s.maxEnd[i] > t; i-- {
		if s.cues[i].End > t {
			if count == 0 {
				last = i
			}
			first = i
			count++
		}
	}
	if count == s.count && first == s.first && last == s.last {
		return s.text
	}
	s.first, s.last, s.count = first, last, count
	switch count {
	case 0:
		s.text = ""
	case 1:
		s.text = s.cues[first].Text
	default:
		var b strings.Builder
		for i := first; i <= last; i++ {
			if c := s.cues[i]; c.Start <= t && c.End > t {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(c.Text)
			}
		}
		s.text = b.String()
	}
	return s.text
}
