package remux

import (
	"container/heap"
	"time"
)

const (
	// fragmentBytes is how much sample data a fragment holds before it is
	// written out, so a segment streams to the player while it is read.
	fragmentBytes = 512 << 10
	// reorderLookahead is how many video frames the fragmenter waits for
	// before it fixes a decode time: enough for B-frame pyramids of 16.
	reorderLookahead = 18
)

// fragmenter turns the packets of one segment into moof+mdat fragments.
//
// Containers give presentation times only (Matroska) or decode times that
// may start below zero (MP4 edit lists, MPEG-TS). Decode times are made
// from the sorted presentation times instead, as CMAF does: the n-th
// decoded frame of a segment gets the n-th smallest presentation time,
// so the first keyframe has a composition offset of 0, tfdt is the
// segment's earliest presentation time, and B-frames get signed offsets
// (trun version 1). A small lookahead finds the order while streaming.
type fragmenter struct {
	video, audio *outTrack
	emit         func([]byte) error
	seq          uint32

	vq       []vsample
	pending  ptsHeap // presentation times without a decode time yet
	assigned int     // vq[:assigned] have decode times
	lastDTS  int64
	haveDTS  bool
	minDelta int64 // smallest presentation time step seen

	aq       []asample
	audioEnd int64 // end of the last audio sample, -1 before the first

	bytes int
}

type vsample struct {
	pts, dts int64
	key      bool
	data     []byte
}

type asample struct {
	t    int64
	data []byte
}

func newFragmenter(seg int, video, audio *outTrack, emit func([]byte) error) *fragmenter {
	return &fragmenter{video: video, audio: audio, emit: emit, seq: uint32(seg)*1024 + 1, audioEnd: -1}
}

func (f *fragmenter) pushVideo(p *packet) error {
	if f.video == nil {
		return nil
	}
	pts := toTimescale(p.pts, f.video.timescale)
	f.vq = append(f.vq, vsample{pts: pts, key: p.key, data: p.data})
	heap.Push(&f.pending, pts)
	f.bytes += len(p.data)
	for len(f.vq)-f.assigned > reorderLookahead {
		f.assignOne()
	}
	if f.bytes >= fragmentBytes && f.assigned > 1 {
		return f.flush(false)
	}
	return nil
}

func (f *fragmenter) assignOne() {
	d := heap.Pop(&f.pending).(int64)
	if f.haveDTS {
		if step := d - f.lastDTS; step > 0 && (f.minDelta == 0 || step < f.minDelta) {
			f.minDelta = step
		}
		if d <= f.lastDTS {
			d = f.lastDTS + 1
		}
	}
	f.vq[f.assigned].dts = d
	f.lastDTS, f.haveDTS = d, true
	f.assigned++
}

func (f *fragmenter) pushAudio(p *packet) error {
	if f.audio == nil {
		return nil
	}
	t := f.audioTime(p.pts)
	if f.audioEnd >= 0 && t < f.audioEnd {
		// Overlapping frame (rounding in the container): butt it
		// against the one before.
		t = f.audioEnd
	}
	f.aq = append(f.aq, asample{t: t, data: p.data})
	f.audioEnd = t + f.audio.frameSamples
	f.bytes += len(p.data)
	if f.bytes >= fragmentBytes && (f.video == nil || f.assigned > 1) {
		return f.flush(false)
	}
	return nil
}

// audioTime snaps a container time onto the track's grid of frames, so
// that millisecond-rounded Matroska times give gapless audio across
// segments.
func (f *fragmenter) audioTime(pts time.Duration) int64 {
	a := f.audio
	x := toTimescale(pts, a.timescale)
	if a.frameSamples <= 0 {
		return x
	}
	anchor := toTimescale(a.anchor, a.timescale)
	n := x - anchor
	q := n / a.frameSamples
	r := n - q*a.frameSamples
	if r < 0 {
		q--
		r += a.frameSamples
	}
	if 2*r >= a.frameSamples {
		q++
	}
	return anchor + q*a.frameSamples
}

// finish writes what is left at the end of the segment.
func (f *fragmenter) finish() error {
	for f.assigned < len(f.vq) {
		f.assignOne()
	}
	return f.flush(true)
}

// flush writes a fragment with every video sample whose duration is known
// (all of them at the end) and all audio samples.
func (f *fragmenter) flush(final bool) error {
	n := f.assigned
	if !final && n > 0 {
		n-- // its duration needs the next decode time
	}
	var runs []fragTrack
	if n > 0 {
		run := fragTrack{t: f.video, baseDTS: uint64(max(f.vq[0].dts, 0)), samples: make([]fragSample, n)}
		for i := 0; i < n; i++ {
			s := &f.vq[i]
			var dur int64
			if i+1 < len(f.vq) && i+1 < f.assigned {
				dur = f.vq[i+1].dts - s.dts
			} else {
				dur = f.minDelta
				if dur <= 0 {
					dur = defaultFrameDur(f.video)
				}
			}
			flags := uint32(sampleFlagsNonSync)
			if s.key {
				flags = sampleFlagsSync
			}
			run.samples[i] = fragSample{dur: uint32(max(dur, 1)), cto: int32(s.pts - s.dts), flags: flags, data: s.data}
			f.bytes -= len(s.data)
		}
		runs = append(runs, run)
		f.vq = append(f.vq[:0], f.vq[n:]...)
		f.assigned -= n
	}
	if len(f.aq) > 0 {
		run := fragTrack{t: f.audio, baseDTS: uint64(max(f.aq[0].t, 0)), samples: make([]fragSample, len(f.aq))}
		for i := range f.aq {
			s := &f.aq[i]
			dur := f.audio.frameSamples
			if i+1 < len(f.aq) {
				dur = f.aq[i+1].t - s.t
			}
			run.samples[i] = fragSample{dur: uint32(max(dur, 1)), data: s.data}
			f.bytes -= len(s.data)
		}
		runs = append(runs, run)
		f.aq = f.aq[:0]
	}
	if len(runs) == 0 {
		return nil
	}
	frag := writeFragment(f.seq, runs)
	f.seq++
	return f.emit(frag)
}

func defaultFrameDur(t *outTrack) int64 {
	fps := t.frameRate
	if fps <= 0 {
		fps = 24
	}
	return int64(float64(t.timescale) / fps)
}

// ptsHeap is a min-heap of presentation times.
type ptsHeap []int64

func (h ptsHeap) Len() int           { return len(h) }
func (h ptsHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h ptsHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *ptsHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *ptsHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
