package remux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mygo-emby/internal/emby"
)

// Errors of Open.
var (
	// ErrUnsupportedFormat: the file is not Matroska, MP4/MOV or MPEG-TS
	// (a disc image, AVI…).
	ErrUnsupportedFormat = errors.New("remux: unsupported container")
	// ErrUnsupportedVideo: the video codec cannot be remuxed for AVPlayer.
	ErrUnsupportedVideo = errors.New("remux: unsupported video")
	// ErrUnsupportedAudio: the chosen audio track cannot be copied and the
	// caller did not permit the optional local audio transcoder.
	ErrUnsupportedAudio = errors.New("remux: unsupported audio")
)

const (
	headSize = 256 << 10
	// readAheadLimit bounds the memory of segments read ahead or kept for
	// a repeated request.
	readAheadLimit = 96 << 20
	keepDone       = 3
)

// fullDemuxer is what a session needs of a container reader.
type fullDemuxer interface {
	demuxer
	selectTracks(audio *track, subs []*track)
	prepare(ctx context.Context) error
}

func (d *tsDemuxer) prepare(context.Context) error { return nil }

// Session is one remuxed file. Its exported fields are set by Open and do
// not change.
type Session struct {
	// PlaylistURL is the master playlist for video.OpenURL.
	PlaylistURL string
	// Duration is the length of the stream.
	Duration time.Duration
	// Tracks lists the file's video, audio and subtitle tracks.
	Tracks []Track
	// AudioIndex is the Emby index of the audio track playing, -1 for none.
	AudioIndex int

	srv      *Server
	token    string
	logf     func(string, ...any)
	up       *upstream
	dmx      fullDemuxer
	info     *mediaInfo
	video    *track
	audio    *track
	audioOut *outTrack
	// demuxAudio is the container audio track read with video. It is nil
	// when audio comes from the optional FFmpeg segment pipeline.
	demuxAudio     *track
	transcodeAudio bool
	mediaStart     time.Duration
	audioStart     time.Duration
	subs           []*track
	subIdx         map[*track]int
	outs           []*outTrack
	segs           []segment
	init           []byte
	master         []byte
	media          []byte
	start          time.Duration
	startSeg       int
	onCues         func(int, []emby.Cue)

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	jobs     map[int]*segJob
	cuesSeen map[cueKey]struct{}
	closed   bool

	segments atomic.Int64
	served   atomic.Int64
}

func openSession(ctx context.Context, srv *Server, src Source) (*Session, error) {
	if src.URL == "" {
		return nil, errors.New("remux: no URL")
	}
	up := newUpstream(src.Client, src.URL, src.Header, srv.logf)
	return newSession(ctx, srv, src, newHeadCache(up))
}

// newSession opens a session over any byte source (tests use local files).
func newSession(ctx context.Context, srv *Server, src Source, bs byteSource) (*Session, error) {
	t0 := time.Now()
	head := make([]byte, headSize)
	n, err := bs.readAt(ctx, head, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	head = head[:n]
	if len(head) < 16 {
		return nil, ErrUnsupportedFormat
	}
	var dmx fullDemuxer
	switch {
	case bytes.HasPrefix(head, []byte{0x1a, 0x45, 0xdf, 0xa3}):
		dmx, err = openMatroska(ctx, bs, head)
	case isMP4(head):
		dmx, err = openMP4(ctx, bs, head)
	default:
		if _, _, _, ok := detectTS(head); ok {
			dmx, err = openTS(ctx, bs, head)
		} else {
			return nil, ErrUnsupportedFormat
		}
	}
	if err != nil {
		return nil, err
	}
	hc, _ := bs.(*headCache)
	if hc != nil {
		hc.freeze()
		bs = hc.byteSource
	}
	x := &Session{srv: srv, logf: srv.logf, dmx: dmx, onCues: src.OnCues, start: src.Start,
		jobs: map[int]*segJob{}, cuesSeen: map[cueKey]struct{}{}, subIdx: map[*track]int{}, AudioIndex: -1}
	if u, ok := bs.(*upstream); ok {
		x.up = u
	}
	switch d := dmx.(type) {
	case *mkvDemuxer:
		x.info = d.info
	case *mp4Demuxer:
		x.info = d.info
	case *tsDemuxer:
		x.info = d.info
	}
	info := x.info
	if info.video == nil || info.video.out == nil {
		why := "no video track"
		if info.video != nil {
			why = info.video.why
		}
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedVideo, why)
	}
	x.video = info.video
	idx := embyIndexes(src.MediaSource, info)

	// The audio track. Prefer one that can be copied when the caller left
	// the choice open. An explicitly selected or only track may use the
	// audio-only transcoder; it must never become a silent video stream.
	if !src.NoAudio && len(info.audio) > 0 {
		var a *track
		if src.AudioIndex >= 0 {
			for _, t := range info.audio {
				if i, ok := idx[t]; ok && i == src.AudioIndex || !ok && src.MediaSource == nil && t.order == src.AudioIndex {
					a = t
				}
			}
		}
		if a == nil {
			for _, t := range info.audio {
				if t.isDefault && t.out != nil {
					a = t
					break
				}
			}
		}
		if a == nil {
			for _, t := range info.audio {
				if t.out != nil {
					a = t
					break
				}
			}
		}
		if a == nil {
			for _, t := range info.audio {
				if t.isDefault {
					a = t
					break
				}
			}
		}
		if a == nil {
			a = info.audio[0]
		}
		x.audio = a
		if a.out != nil {
			x.audioOut, x.demuxAudio = a.out, a
		} else {
			if !src.AudioTranscode {
				why := a.why
				if why == "" {
					why = "audio codec " + a.codec + " cannot be copied"
				}
				return nil, fmt.Errorf("%w: %s", ErrUnsupportedAudio, why)
			}
			if x.up == nil || srv.ffmpeg == "" {
				return nil, fmt.Errorf("%w: %s", ErrAudioTranscoderUnavailable, a.why)
			}
			x.audioOut = transcodeAACOutput()
			x.transcodeAudio = true
			if probe, ok := dmx.(startProber); ok {
				x.mediaStart, x.audioStart, err = probe.probeStarts(ctx, a)
				if err != nil {
					return nil, fmt.Errorf("remux: probing audio origin: %w", err)
				}
			}
		}
	}
	for _, t := range info.subs {
		if isTextSubtitle(t.codec) && t.why == "" {
			x.subs = append(x.subs, t)
		}
		if i, ok := idx[t]; ok {
			x.subIdx[t] = i
		} else {
			x.subIdx[t] = -1 - t.order
		}
	}
	dmx.selectTracks(x.demuxAudio, x.subs)
	if err := dmx.prepare(ctx); err != nil {
		return nil, err
	}
	if x.video.out == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedVideo, x.video.why)
	}

	vo := x.video.out
	vo.id = 1
	vo.language = iso639(x.video.language)
	x.outs = []*outTrack{vo}
	if x.audio != nil {
		ao := x.audioOut
		ao.id = 2
		ao.language = iso639(x.audio.language)
		x.outs = append(x.outs, ao)
		if i, ok := idx[x.audio]; ok {
			x.AudioIndex = i
		} else if src.MediaSource == nil {
			x.AudioIndex = x.audio.order
		}
	}
	x.Duration = info.duration
	if info.durationIncludesStart && x.mediaStart != 0 {
		x.Duration -= x.mediaStart
	}
	if x.Duration <= 0 {
		x.Duration = src.Duration
	}
	if x.Duration <= 0 {
		return nil, errors.New("remux: the length of the file is unknown")
	}
	gop := time.Duration(0)
	if g, ok := dmx.(interface{ gop() time.Duration }); ok {
		gop = g.gop()
	}
	keyframes := dmx.keyframes()
	if x.mediaStart != 0 {
		keyframes = append([]keyframe(nil), keyframes...)
		for i := range keyframes {
			keyframes[i].t -= x.mediaStart
		}
	}
	x.segs = planSegments(x.Duration, keyframes, gop)
	for i := range x.segs {
		x.segs[i].offset = x.mediaStart
	}
	x.init = writeInit(x.outs, x.Duration)
	x.Tracks = x.trackList(idx)
	x.token = newToken()
	x.PlaylistURL = srv.base + "/" + x.token + "/master.m3u8"
	x.master = x.masterPlaylist(src)
	x.media = x.mediaPlaylist()
	// Reads of the session outlive the context of Open.
	x.ctx, x.cancel = context.WithCancel(context.Background())
	indexed := "indexed"
	if dmx.keyframes() == nil {
		indexed = "no index"
	}
	x.logf("remux: opened %s (%s, %s) %v, %d segments, %s %s, in %v", info.format, x.video.out.codecs,
		audioCodecs(x.audioOut), x.Duration.Round(time.Second), len(x.segs), indexed, vo.colour.videoRange(), time.Since(t0).Round(time.Millisecond))
	return x, nil
}

func audioCodecs(t *outTrack) string {
	if t == nil {
		return "no audio"
	}
	return t.codecs
}

func isMP4(b []byte) bool {
	if len(b) < 8 {
		return false
	}
	switch string(b[4:8]) {
	case "ftyp", "moov", "mdat", "free", "wide", "skip", "pnot":
		return true
	}
	return false
}

// iso639 keeps three-letter language codes for mdhd.
func iso639(l string) string {
	l = strings.ToLower(l)
	if i := strings.IndexByte(l, '-'); i >= 0 {
		l = l[:i]
	}
	if len(l) == 3 {
		return l
	}
	return "und"
}

// embyIndexes maps container tracks to Emby MediaStream indexes: the n-th
// embedded stream of a type is the container's n-th track of that type.
func embyIndexes(ms *emby.MediaSource, info *mediaInfo) map[*track]int {
	m := map[*track]int{}
	if ms == nil {
		return m
	}
	byType := map[string][]int{}
	streams := append([]emby.MediaStream(nil), ms.MediaStreams...)
	sort.SliceStable(streams, func(i, j int) bool { return streams[i].Index < streams[j].Index })
	for _, s := range streams {
		if s.IsExternal {
			continue
		}
		byType[s.Type] = append(byType[s.Type], s.Index)
	}
	put := func(t *track, typ string) {
		if t == nil {
			return
		}
		if l := byType[typ]; t.order < len(l) {
			m[t] = l[t.order]
		}
	}
	put(info.video, emby.StreamVideo)
	for _, t := range info.audio {
		put(t, emby.StreamAudio)
	}
	for _, t := range info.subs {
		put(t, emby.StreamSubtitle)
	}
	return m
}

func (x *Session) trackList(idx map[*track]int) []Track {
	var out []Track
	add := func(t *track) {
		i, ok := idx[t]
		if !ok {
			i = -1
		}
		tr := Track{Kind: t.kind, Index: i, Codec: t.codec, Language: t.language, Title: t.title,
			Default: t.isDefault, Forced: t.forced, Playing: t == x.video || t == x.audio}
		switch t.kind {
		case KindSubtitle:
			tr.Supported = isTextSubtitle(t.codec) && t.why == ""
		case KindAudio:
			tr.Supported = t == x.audio && x.audioOut != nil
		default:
			tr.Supported = t.out != nil
		}
		if !tr.Supported {
			tr.Reason = t.why
		}
		out = append(out, tr)
	}
	add(x.video)
	for _, t := range x.info.audio {
		add(t)
	}
	for _, t := range x.info.subs {
		add(t)
	}
	return out
}

// Stats returns the session's counters.
func (x *Session) Stats() Stats {
	s := Stats{Segments: x.segments.Load(), Served: x.served.Load()}
	if x.up != nil {
		s.UpstreamBytes = x.up.bytesRead.Load()
		s.UpstreamRequests = x.up.requests.Load()
	}
	return s
}

// Close stops every read of the session and forgets it. AVPlayer's
// requests for it then get 404. Close does not block on the network.
func (x *Session) Close() error {
	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return nil
	}
	x.closed = true
	jobs := x.jobs
	x.jobs = map[int]*segJob{}
	x.mu.Unlock()
	for _, j := range jobs {
		j.cancel()
	}
	if x.cancel != nil {
		x.cancel()
	}
	if x.srv != nil {
		x.srv.remove(x.token)
	}
	return nil
}

func (x *Session) serve(w http.ResponseWriter, r *http.Request, name string) {
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	switch {
	case name == "master.m3u8":
		h.Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(x.master))
	case name == "media.m3u8":
		h.Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(x.media))
	case name == "init.mp4":
		h.Set("Content-Type", "video/mp4")
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(x.init))
	case strings.HasSuffix(name, ".m4s"):
		k, err := strconv.Atoi(strings.TrimSuffix(name, ".m4s"))
		if err != nil || k < 0 || k >= len(x.segs) {
			http.NotFound(w, r)
			return
		}
		x.serveSegment(w, r, k)
	default:
		http.NotFound(w, r)
	}
}

// segJob produces one segment into memory while any number of requests
// stream it out.
type segJob struct {
	k      int
	ctx    context.Context
	cancel context.CancelFunc
	start  time.Time

	mu      sync.Mutex
	chunks  [][]byte
	size    int
	done    bool
	err     error
	notify  chan struct{}
	readers int
	asked   bool // a request wanted it (not only read ahead)
	used    time.Time
}

func (j *segJob) append(b []byte) error {
	j.mu.Lock()
	j.chunks = append(j.chunks, b)
	j.size += len(b)
	close(j.notify)
	j.notify = make(chan struct{})
	j.mu.Unlock()
	return nil
}

func (j *segJob) finish(err error) {
	j.mu.Lock()
	j.done, j.err = true, err
	close(j.notify)
	j.notify = make(chan struct{})
	j.mu.Unlock()
}

// job returns the job of segment k, starting it if needed. ask tells
// whether a request wants it (else it is read ahead).
func (x *Session) job(k int, ask bool) *segJob {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.closed {
		return nil
	}
	if j := x.jobs[k]; j != nil {
		j.mu.Lock()
		failed := j.done && j.err != nil
		j.mu.Unlock()
		if !failed {
			if ask {
				j.mu.Lock()
				j.readers++
				j.asked = true
				j.used = time.Now()
				j.mu.Unlock()
			}
			return j
		}
		delete(x.jobs, k)
	}
	if !ask && x.liveBytesLocked() > readAheadLimit {
		return nil
	}
	ctx, cancel := context.WithCancel(x.ctx)
	j := &segJob{k: k, ctx: ctx, cancel: cancel, notify: make(chan struct{}), start: time.Now(), used: time.Now()}
	if ask {
		j.readers, j.asked = 1, true
	}
	seek := true
	if p := x.jobs[k-1]; p != nil || k == 0 {
		seek = false
	}
	x.jobs[k] = j
	x.segments.Add(1)
	go x.produce(j, seek)
	return j
}

func (x *Session) liveBytesLocked() int {
	n := 0
	for _, j := range x.jobs {
		j.mu.Lock()
		n += j.size
		j.mu.Unlock()
	}
	return n
}

// release ends a request's interest in a job: a job nobody wants any more
// stops reading.
func (x *Session) release(j *segJob) {
	j.mu.Lock()
	j.readers--
	idle := j.readers == 0 && !j.done
	j.mu.Unlock()
	if idle {
		j.cancel()
		x.mu.Lock()
		if x.jobs[j.k] == j {
			delete(x.jobs, j.k)
		}
		x.mu.Unlock()
	}
}

// trim drops finished jobs beyond the few kept for repeated requests, and
// read-ahead jobs that a seek made useless.
func (x *Session) trim(current int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	var done []*segJob
	for k, j := range x.jobs {
		j.mu.Lock()
		switch {
		case j.done:
			if j.readers == 0 {
				done = append(done, j)
			}
		case j.readers == 0 && k != current && k != current+1 && !(k == x.startSeg && !j.asked):
			j.cancel()
			delete(x.jobs, k)
		}
		j.mu.Unlock()
	}
	sort.Slice(done, func(a, b int) bool { return done[a].used.After(done[b].used) })
	for i, j := range done {
		// Keep the most recent few, and a read-ahead nobody asked for yet.
		if i < keepDone || (!j.asked && j.k == current+1) {
			continue
		}
		delete(x.jobs, j.k)
	}
}

func (x *Session) produce(j *segJob, seek bool) {
	seg := &x.segs[j.k]
	var cues []emby.Cue
	var cueTrack []int
	sink := func(p *packet) {
		idx, ok := x.subIdx[p.t]
		if !ok {
			return
		}
		copy := *p
		copy.pts -= seg.offset
		if c, ok := makeCue(&copy); ok {
			cues = append(cues, c)
			cueTrack = append(cueTrack, idx)
		}
	}
	var vo, ao *outTrack
	if x.video != nil {
		vo = x.video.out
	}
	if x.audio != nil {
		ao = x.audioOut
	}
	frag := newFragmenter(j.k, vo, ao, func(b []byte) error {
		if err := j.ctx.Err(); err != nil {
			return err
		}
		return j.append(b)
	})
	f := newSegFilter(seg, x.video, x.demuxAudio, x.subs, frag, sink)
	err := x.dmx.read(j.ctx, seg, f)
	if err == nil && x.transcodeAudio {
		err = x.addTranscodedAudio(j.ctx, seg, frag)
	}
	if err == nil {
		err = frag.finish()
	}
	if err == nil && !f.seen {
		err = fmt.Errorf("remux: segment %d is empty", j.k)
	}
	j.finish(err)
	elapsed := time.Since(j.start)
	if err != nil && j.ctx.Err() == nil {
		x.logf("remux: segment %d failed after %v: %v", j.k, elapsed.Round(time.Millisecond), err)
	} else if err == nil {
		x.logf("remux: segment %d (%.1fs at %v) %d KB in %v", j.k, seg.duration().Seconds(), seg.start.Round(time.Millisecond), j.size>>10, elapsed.Round(time.Millisecond))
	}
	if seek && len(x.subs) > 0 && j.ctx.Err() == nil {
		// After a seek, cues that began before the segment and still show.
		_ = x.dmx.subtitlesBefore(j.ctx, seg.start+seg.offset, sink)
	}
	x.deliver(cueTrack, cues)
}

func (x *Session) addTranscodedAudio(ctx context.Context, seg *segment, frag *fragmenter) error {
	input := x.srv.base + "/" + x.token + "/source"
	start := max(seg.start, x.audioStart-x.mediaStart)
	start = transcodeFrameStart(start)
	duration := seg.end - start
	if duration <= 0 {
		return fmt.Errorf("%w: empty audio range for segment %d (audio start %v, segment %v-%v)",
			ErrAudioTranscodeFailed, seg.index, x.audioStart, seg.start, seg.end)
	}
	seek := max(start, 0)
	frames, err := x.srv.transcodeAudio(ctx, input, x.audio, seek, start, duration)
	if err != nil {
		return err
	}
	frameDuration := time.Second * time.Duration(x.audioOut.frameSamples) /
		time.Duration(x.audioOut.timescale)
	for i, data := range frames {
		pts := start + time.Duration(i)*frameDuration
		if pts >= seg.end {
			break
		}
		if err := frag.pushAudio(&packet{t: x.audio, pts: pts, data: data}); err != nil {
			return err
		}
	}
	if frag.audioEnd < 0 {
		return fmt.Errorf("%w: no audio in segment %d", ErrAudioTranscodeFailed, seg.index)
	}
	return nil
}

type cueKey struct {
	track      int
	start, end time.Duration
	text       string
}

// deliver passes new cues to OnCues, grouped by track.
func (x *Session) deliver(tracks []int, cues []emby.Cue) {
	if x.onCues == nil || len(cues) == 0 {
		return
	}
	by := map[int][]emby.Cue{}
	var order []int
	x.mu.Lock()
	for i, c := range cues {
		k := cueKey{tracks[i], c.Start, c.End, c.Text}
		if _, ok := x.cuesSeen[k]; ok {
			continue
		}
		x.cuesSeen[k] = struct{}{}
		if _, ok := by[tracks[i]]; !ok {
			order = append(order, tracks[i])
		}
		by[tracks[i]] = append(by[tracks[i]], c)
	}
	closed := x.closed
	x.mu.Unlock()
	if closed {
		return
	}
	for _, t := range order {
		x.onCues(t, by[t])
	}
}

func (x *Session) serveSegment(w http.ResponseWriter, r *http.Request, k int) {
	j := x.job(k, true)
	if j == nil {
		http.Error(w, "session closed", http.StatusGone)
		return
	}
	defer x.release(j)
	x.trim(k)
	if k+1 < len(x.segs) {
		x.job(k+1, false)
	}
	h := w.Header()
	h.Set("Content-Type", "video/mp4")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	flusher, _ := w.(http.Flusher)
	sent, wrote := 0, false
	for {
		j.mu.Lock()
		chunks := j.chunks[sent:]
		done, err, notify := j.done, j.err, j.notify
		if done && err == nil {
			h.Set("Content-Length", strconv.Itoa(j.size-sizeOf(j.chunks[:sent])))
		}
		j.mu.Unlock()
		if done && err != nil && !wrote && len(chunks) == 0 {
			http.Error(w, "upstream read failed", http.StatusBadGateway)
			return
		}
		if done && err != nil {
			// Part of the segment went out: break the connection so
			// AVPlayer retries instead of taking a short segment.
			panic(http.ErrAbortHandler)
		}
		for _, c := range chunks {
			if _, err := w.Write(c); err != nil {
				return
			}
			x.served.Add(int64(len(c)))
			wrote = true
		}
		sent += len(chunks)
		if done {
			return
		}
		if flusher != nil && len(chunks) > 0 {
			flusher.Flush()
		}
		select {
		case <-notify:
		case <-r.Context().Done():
			return
		}
	}
}

func sizeOf(chunks [][]byte) int {
	n := 0
	for _, c := range chunks {
		n += len(c)
	}
	return n
}

// startReadAhead reads the segment where playback starts while AVPlayer
// loads the playlists.
func (x *Session) startReadAhead() {
	x.startSeg = x.segmentAt(x.start)
	x.job(x.startSeg, false)
}

func (x *Session) segmentAt(t time.Duration) int {
	i := sort.Search(len(x.segs), func(i int) bool { return x.segs[i].start > t })
	return max(i-1, 0)
}
