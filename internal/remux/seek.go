package remux

import (
	"context"
	"sort"
	"sync"
	"time"
)

// seekPoint is a byte position whose stream time is known.
type seekPoint struct {
	pos int64
	t   time.Duration
}

// seekMemo remembers what reads and probes learned about where times are
// in a file without an index, so that later seeks need fewer probes and
// sequential segments start exactly where the one before ended.
type seekMemo struct {
	mu      sync.Mutex
	points  []seekPoint // sorted by pos
	starts  map[int]int64
	reading map[int]chan struct{} // segments being read, closed when done
}

func newSeekMemo() *seekMemo {
	return &seekMemo{starts: map[int]int64{}, reading: map[int]chan struct{}{}}
}

// begin marks segment seg as being read; the returned func marks it done.
func (m *seekMemo) begin(seg int) func() {
	ch := make(chan struct{})
	m.mu.Lock()
	m.reading[seg] = ch
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if m.reading[seg] == ch {
			delete(m.reading, seg)
		}
		m.mu.Unlock()
		close(ch)
	}
}

// startAfter returns where segment seg starts. When the segment before it
// is being read (the usual case: AVPlayer reads on while it plays), it
// waits for that read to find the exact position rather than search.
func (m *seekMemo) startAfter(ctx context.Context, seg int) (int64, bool) {
	m.mu.Lock()
	p, ok := m.starts[seg]
	prev := m.reading[seg-1]
	m.mu.Unlock()
	if ok || prev == nil {
		return p, ok
	}
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	select {
	case <-prev:
	case <-ctx.Done():
	case <-t.C:
	}
	return m.start(seg)
}

func (m *seekMemo) add(p seekPoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := sort.Search(len(m.points), func(i int) bool { return m.points[i].pos >= p.pos })
	if i < len(m.points) && m.points[i].pos == p.pos {
		return
	}
	m.points = append(m.points, seekPoint{})
	copy(m.points[i+1:], m.points[i:])
	m.points[i] = p
	if len(m.points) > 4096 {
		m.points = append(m.points[:0], m.points[len(m.points)-4096:]...)
	}
}

// bracket narrows lo and hi with remembered points around t.
func (m *seekMemo) bracket(t time.Duration, lo, hi seekPoint) (seekPoint, seekPoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.points {
		if p.pos <= lo.pos || p.pos >= hi.pos {
			continue
		}
		if p.t <= t && p.t >= lo.t {
			lo = p
		} else if p.t > t && p.t <= hi.t {
			hi = p
		}
	}
	return lo, hi
}

// setStart records where segment seg starts, found by reading the one
// before it to its end.
func (m *seekMemo) setStart(seg int, pos int64) {
	m.mu.Lock()
	m.starts[seg] = pos
	m.mu.Unlock()
}

func (m *seekMemo) start(seg int) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.starts[seg]
	return p, ok
}

const (
	// seekMargin is how far before a segment's start reading begins
	// without an index, for B-frame reordering and loose interleaving.
	seekMargin = 1500 * time.Millisecond
	// seekCloseEnough ends the search once a point is this little before
	// the target: reading on is cheaper than another round trip.
	seekCloseEnough = 2 * time.Second
	seekMinSpan     = 1 << 20
	seekMaxProbes   = 12
)

// probeFunc finds the first sync point at or after off: a Matroska
// cluster or an MPEG-TS video PES. ok is false when there is none before
// the end of the probe window.
type probeFunc func(ctx context.Context, off int64) (p seekPoint, ok bool, err error)

// locate returns a byte position from which reading reaches every packet
// at or after target, by interpolation search between lo and hi.
func locate(ctx context.Context, m *seekMemo, lo, hi seekPoint, target time.Duration, probe probeFunc) (int64, error) {
	target -= seekMargin
	if target <= lo.t {
		return lo.pos, nil
	}
	lo, hi = m.bracket(target, lo, hi)
	bisect := false
	for i := 0; i < seekMaxProbes; i++ {
		if target-lo.t < seekCloseEnough || hi.pos-lo.pos < seekMinSpan {
			break
		}
		span := hi.t - lo.t
		if span <= 0 {
			break
		}
		var guess int64
		if bisect {
			// Interpolation stalls when the bitrate is uneven: halve.
			guess = lo.pos + (hi.pos-lo.pos)/2
		} else {
			frac := float64(target-lo.t) / float64(span)
			guess = lo.pos + int64(frac*float64(hi.pos-lo.pos))
			// Aim a little low: landing just before the target is what we want.
			guess -= (hi.pos - lo.pos) / 32
		}
		guess = min(max(guess, lo.pos+1), hi.pos-1)
		p, ok, err := probe(ctx, guess)
		if err != nil {
			return 0, err
		}
		if !ok || p.pos >= hi.pos {
			// Nothing between guess and hi: the target is before guess.
			hi = seekPoint{pos: guess, t: hi.t}
			bisect = true
			continue
		}
		// Alternate: an interpolation step that moved a bound only a
		// little is followed by a bisection.
		bisect = !bisect && (p.t <= target && p.pos-lo.pos < (hi.pos-lo.pos)/8 || p.t > target && hi.pos-p.pos < (hi.pos-lo.pos)/8)
		m.add(p)
		if p.t <= target {
			lo = p
		} else {
			hi = p
		}
	}
	return lo.pos, nil
}
