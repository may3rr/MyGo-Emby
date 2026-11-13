package remux

import (
	"context"
	"math"
	"sort"
	"time"
)

// TrackKind is the kind of a track.
type TrackKind int

// Track kinds.
const (
	KindVideo TrackKind = iota
	KindAudio
	KindSubtitle
)

func (k TrackKind) String() string {
	switch k {
	case KindVideo:
		return "video"
	case KindAudio:
		return "audio"
	}
	return "subtitle"
}

// track is a container track as a demuxer found it.
type track struct {
	kind              TrackKind
	id                int64 // MKV track number, MP4 track_ID, TS PID
	order             int   // position among the container's tracks of the same kind
	codec             string
	language          string
	title             string
	isDefault, forced bool

	// out is the fMP4 description, nil when the track cannot be remuxed.
	out *outTrack
	// why tells why out is nil.
	why string

	// mkv: per-frame data to prepend (header stripping), and whether
	// frames are zlib compressed.
	stripped []byte
	zlib     bool
	// defaultDur is the container's frame duration, 0 if unknown.
	defaultDur time.Duration
	// codecDelay is Matroska's CodecDelay, subtracted from audio times.
	codecDelay time.Duration
	// lengthSize is the NAL length size of video samples (1-4); 0 means
	// Annex B (MPEG-TS).
	lengthSize int
	// in-band parameter sets are still missing from the config (hev1,
	// avc3, MPEG-TS): the demuxer must find them in the first keyframe.
	needParams bool
}

// packet is one access unit or subtitle cue in presentation time.
type packet struct {
	t    *track
	pts  time.Duration
	dur  time.Duration // subtitles and audio; 0 for video
	key  bool
	data []byte
	// pos is the byte offset where reading must start to get this packet
	// (the MKV cluster or TS packet that holds its start).
	pos int64
}

// segment is one HLS media segment.
type segment struct {
	index int
	// start and end are the boundary times. With an exact index start is
	// the presentation time of the segment's first keyframe; otherwise
	// they are grid times and the keyframes follow them.
	start, end time.Duration
	first      bool // no lower bound: from the beginning of the stream
	last       bool // no upper bound: to the end of the stream
	// pos is where reading starts and endPos where the next segment's
	// keyframe is, when the index knows; -1 otherwise.
	pos, endPos int64
	// offset converts this content-relative segment back to the container's
	// packet timeline while demuxers seek by byte.
	offset time.Duration
}

// extinf is the segment duration written in the playlist.
func (s *segment) duration() time.Duration { return s.end - s.start }

// demuxer reads one container format.
type demuxer interface {
	// keyframes returns the presentation times of the video keyframes
	// with their byte positions when the container has an exact index,
	// else nil.
	keyframes() []keyframe
	// read delivers the packets of seg, in file order, to f until f
	// reports it complete or the stream ends.
	read(ctx context.Context, seg *segment, f *segFilter) error
	// subtitlesBefore fetches subtitle cues of the given tracks that start
	// before t and are still showing at t, when the index allows it
	// cheaply. It may do nothing.
	subtitlesBefore(ctx context.Context, t time.Duration, emit func(*packet)) error
}

// startProber reports the media origin and selected audio origin on the
// same timeline used by packet PTS. The local audio converter needs both:
// FFmpeg's input -ss is relative to the media origin, while the generated
// AAC packets must retain the selected audio track's position.
type startProber interface {
	probeStarts(ctx context.Context, audio *track) (mediaStart, audioStart time.Duration, err error)
}

type keyframe struct {
	t   time.Duration
	pos int64
}

// mediaInfo is a parsed container header.
type mediaInfo struct {
	format   string // matroska, mp4, mpegts
	duration time.Duration
	// durationIncludesStart marks containers whose stored duration is an end
	// time on the packet timeline. Matroska muxers, including FFmpeg, use this
	// representation when the first packet has a non-zero timestamp. MP4 and
	// MPEG-TS readers already calculate content-relative durations here.
	durationIncludesStart bool
	size                  int64
	video                 *track
	audio                 []*track
	subs                  []*track
}

const (
	firstSegment  = 2 * time.Second
	targetSegment = 4 * time.Second
)

// gridTimes returns the nominal boundaries 0, first, first+step… below d.
func gridTimes(d, first, step time.Duration) []time.Duration {
	g := []time.Duration{0}
	for t := first; t < d; t += step {
		g = append(g, t)
	}
	return g
}

// planSegments lays out the segments of a stream of duration d. With
// keyframes (an exact index), each grid time moves to the first keyframe at
// or after it; otherwise the grid is used as is.
//
// gop is the keyframe interval of a stream without an index, 0 if
// unknown: grid segments are made at least that long, so that each holds a
// keyframe.
func planSegments(d time.Duration, kfs []keyframe, gop time.Duration) []segment {
	if d <= 0 {
		d = time.Second
	}
	grid := gridTimes(d, firstSegment, targetSegment)
	if len(kfs) == 0 && gop > targetSegment {
		step := (gop + time.Second - 1).Truncate(time.Second)
		grid = gridTimes(d, step, step)
	}
	var bounds []time.Duration
	var pos []int64
	if len(kfs) > 0 {
		sort.Slice(kfs, func(i, j int) bool { return kfs[i].t < kfs[j].t })
		bounds = append(bounds, 0)
		pos = append(pos, kfs[0].pos)
		k := 0
		for _, g := range grid[1:] {
			for k < len(kfs) && kfs[k].t < g {
				k++
			}
			if k == len(kfs) {
				break
			}
			if kfs[k].t <= bounds[len(bounds)-1] {
				continue
			}
			// Do not end on a sliver: a last segment under a second
			// joins the one before.
			if d-kfs[k].t < time.Second {
				break
			}
			bounds = append(bounds, kfs[k].t)
			pos = append(pos, kfs[k].pos)
		}
	} else {
		bounds = grid
		if n := len(bounds); n > 1 && d-bounds[n-1] < time.Second {
			bounds = bounds[:n-1]
		}
		for range bounds {
			pos = append(pos, -1)
		}
	}
	segs := make([]segment, len(bounds))
	for i := range bounds {
		s := &segs[i]
		s.index = i
		s.start = bounds[i]
		s.pos = pos[i]
		s.endPos = -1
		if i+1 < len(bounds) {
			s.end = bounds[i+1]
			s.endPos = pos[i+1]
		} else {
			s.end = d
			s.last = true
		}
	}
	segs[0].first = true
	if len(kfs) > 0 {
		segs[0].start = 0
	}
	return segs
}

// segFilter decides which packets belong to a segment: video from the
// first keyframe at or after the start until the first keyframe at or
// after the end, in decode order; audio and subtitles by presentation
// time. It forwards them to the fragmenter and the cue sink.
type segFilter struct {
	seg   *segment
	video *track
	audio *track
	subs  map[*track]bool

	frag *fragmenter
	cues func(*packet)

	videoStarted, videoDone bool
	audioDone               bool
	// nextPos is the smallest position of a packet that belongs to the
	// next segment, for sequential reads without an index.
	nextPos int64
	// seen tells whether any packet was looked at.
	seen bool
	// lastPTS is the presentation time of the last video or audio packet
	// looked at, to stop reading past the end of a corrupt range.
	lastPTS time.Duration
	offset  time.Duration
}

const startSlack = 500 * time.Microsecond

func newSegFilter(seg *segment, video, audio *track, subs []*track, frag *fragmenter, cues func(*packet)) *segFilter {
	f := &segFilter{seg: seg, video: video, audio: audio, frag: frag, cues: cues, nextPos: -1, offset: seg.offset}
	f.subs = make(map[*track]bool, len(subs))
	for _, s := range subs {
		f.subs[s] = true
	}
	if video == nil {
		f.videoStarted, f.videoDone = true, true
	}
	if audio == nil {
		f.audioDone = true
	}
	return f
}

func (f *segFilter) notePos(pos int64) {
	if pos >= 0 && (f.nextPos < 0 || pos < f.nextPos) {
		f.nextPos = pos
	}
}

// push takes one packet and reports whether the segment is complete.
func (f *segFilter) push(p *packet) (done bool, err error) {
	f.seen = true
	// Subtitle packets keep their container timestamp here. The cue sink
	// applies the same single media-origin normalization that
	// subtitlesBefore already requires for cues found while seeking.
	original := p
	if f.offset != 0 {
		copy := *p
		copy.pts -= f.offset
		p = &copy
	}
	s := f.seg
	switch {
	case p.t == f.video:
		f.lastPTS = p.pts
		if f.videoDone {
			break
		}
		if !f.videoStarted {
			if !p.key || (!s.first && p.pts < s.start-startSlack) {
				break
			}
			if !s.last && p.pts >= s.end-startSlack && !s.first {
				// The first keyframe after the start is already the
				// next segment's: this segment has no video.
				f.videoStarted, f.videoDone = true, true
				f.notePos(p.pos)
				break
			}
			f.videoStarted = true
		} else if p.key && !s.last && p.pts >= s.end-startSlack {
			f.videoDone = true
			f.notePos(p.pos)
			break
		}
		if err := f.frag.pushVideo(p); err != nil {
			return false, err
		}
	case p.t == f.audio:
		f.lastPTS = p.pts
		if f.audioDone {
			break
		}
		if !s.last && p.pts >= s.end {
			f.audioDone = true
			f.notePos(p.pos)
			break
		}
		if (!s.first && p.pts < s.start) || p.pts < -f.audio.out.frameDuration()/2 {
			// Before the segment, or priming before presentation zero
			// (frames within half a frame of zero snap onto it).
			break
		}
		if err := f.frag.pushAudio(p); err != nil {
			return false, err
		}
	case f.subs[p.t]:
		if f.cues != nil {
			f.cues(original)
		}
	}
	return f.videoDone && f.audioDone, nil
}

// complete reports whether both tracks passed the end.
func (f *segFilter) complete() bool { return f.videoDone && f.audioDone }

// toTimescale converts a time to units of ts, rounding to nearest.
func toTimescale(d time.Duration, ts uint32) int64 {
	return int64(math.Round(float64(d) * float64(ts) / 1e9))
}

func fromTimescale(v int64, ts uint32) time.Duration {
	if ts == 0 {
		return 0
	}
	return time.Duration(math.Round(float64(v) * 1e9 / float64(ts)))
}

// isTextSubtitle reports subtitle codecs whose cues are extracted as text.
func isTextSubtitle(codec string) bool {
	switch codec {
	case "subrip", "ass", "ssa", "webvtt", "mov_text":
		return true
	}
	return false
}
