package remux

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Matroska element IDs.
const (
	mkvEBML         = 0x1A45DFA3
	mkvDocType      = 0x4282
	mkvSegment      = 0x18538067
	mkvSeekHead     = 0x114D9B74
	mkvSeek         = 0x4DBB
	mkvSeekID       = 0x53AB
	mkvSeekPosition = 0x53AC
	mkvInfo         = 0x1549A966
	mkvTimeScale    = 0x2AD7B1
	mkvDuration     = 0x4489
	mkvTracks       = 0x1654AE6B
	mkvTrackEntry   = 0xAE
	mkvTrackNumber  = 0xD7
	mkvTrackType    = 0x83
	mkvFlagEnabled  = 0xB9
	mkvFlagDefault  = 0x88
	mkvFlagForced   = 0x55AA
	mkvDefaultDur   = 0x23E383
	mkvCodecDelay   = 0x56AA
	mkvName         = 0x536E
	mkvLanguage     = 0x22B59C
	mkvLanguageBCP  = 0x22B59D
	mkvCodecID      = 0x86
	mkvCodecPrivate = 0x63A2
	mkvVideo        = 0xE0
	mkvPixelWidth   = 0xB0
	mkvPixelHeight  = 0xBA
	mkvColour       = 0x55B0
	mkvMatrix       = 0x55B1
	mkvRange        = 0x55B9
	mkvTransfer     = 0x55BA
	mkvPrimaries    = 0x55BB
	mkvAudio        = 0xE1
	mkvSampleRate   = 0xB5
	mkvChannels     = 0x9F
	mkvEncodings    = 0x6D80
	mkvEncoding     = 0x6240
	mkvEncType      = 0x5033
	mkvCompression  = 0x5034
	mkvCompAlgo     = 0x4254
	mkvCompSettings = 0x4255
	mkvBlockAddMap  = 0x41E4
	mkvBlockAddType = 0x41E7
	mkvBlockAddData = 0x41ED
	mkvCluster      = 0x1F43B675
	mkvTimestamp    = 0xE7
	mkvSimpleBlock  = 0xA3
	mkvBlockGroup   = 0xA0
	mkvBlock        = 0xA1
	mkvBlockDur     = 0x9B
	mkvRefBlock     = 0xFB
	mkvCues         = 0x1C53BB6B
	mkvCuePoint     = 0xBB
	mkvCueTime      = 0xB3
	mkvCueTrackPos  = 0xB7
	mkvCueTrack     = 0xF7
	mkvCueCluster   = 0xF1
	mkvCueRelPos    = 0xF0
	mkvCueDuration  = 0xB2
	mkvTags         = 0x1254C367
	mkvChapters     = 0x1043A770
	mkvAttachments  = 0x1941A469
)

// isTopLevel reports Segment children, which end a cluster of unknown size.
func isTopLevel(id uint32) bool {
	switch id {
	case mkvCluster, mkvCues, mkvTags, mkvChapters, mkvAttachments, mkvSeekHead, mkvInfo, mkvTracks, mkvSegment, mkvEBML:
		return true
	}
	return false
}

// readID reads an element ID (marker bits kept).
func readID(b []byte) (uint32, int) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0
	}
	n := 1
	for m := byte(0x80); b[0]&m == 0; m >>= 1 {
		n++
	}
	if n > 4 || n > len(b) {
		return 0, 0
	}
	var id uint32
	for i := 0; i < n; i++ {
		id = id<<8 | uint32(b[i])
	}
	return id, n
}

// readVint reads an element size; -1 means unknown size.
func readVint(b []byte) (int64, int) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0
	}
	n := 1
	for m := byte(0x80); b[0]&m == 0; m >>= 1 {
		n++
	}
	if n > 8 || n > len(b) {
		return 0, 0
	}
	v := uint64(b[0]) & (0xff >> uint(n))
	allOnes := v == uint64(0xff>>uint(n))
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
		if b[i] != 0xff {
			allOnes = false
		}
	}
	if allOnes {
		return -1, n
	}
	if v > math.MaxInt64/2 {
		return 0, 0
	}
	return int64(v), n
}

// ebmlElem is one element parsed from memory.
type ebmlElem struct {
	id   uint32
	data []byte
}

// ebmlChildren parses the children of a master element in memory. A child
// cut off at the end is dropped.
func ebmlChildren(b []byte) []ebmlElem {
	var out []ebmlElem
	for len(b) > 0 {
		id, n := readID(b)
		if n == 0 {
			break
		}
		size, m := readVint(b[n:])
		if m == 0 || size < 0 || int64(len(b)-n-m) < size {
			break
		}
		out = append(out, ebmlElem{id, b[n+m : n+m+int(size)]})
		b = b[n+m+int(size):]
	}
	return out
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func ebmlFloat(b []byte) float64 {
	switch len(b) {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(b))
	}
	return 0
}

func ebmlString(b []byte) string { return strings.TrimRight(string(b), "\x00") }

// mkvCuePoint is one index entry.
type mkvCueEntry struct {
	t       time.Duration
	track   uint64
	cluster int64 // absolute
	rel     int64 // block offset within the cluster data, -1 if unknown
	dur     time.Duration
}

type mkvDemuxer struct {
	src      byteSource
	info     *mediaInfo
	segStart int64
	segEnd   int64
	scale    time.Duration // timestamp unit
	first    int64         // first cluster position
	tracks   map[uint64]*track

	cues    []mkvCueEntry // video keyframes
	subCues []mkvCueEntry // subtitle blocks
	memo    *seekMemo

	head    []byte // the first bytes of the file
	audio   *track // the selected audio track
	textSub []*track

	mu       sync.Mutex
	block    []byte // the last block body read
	blockOff int64
}

// openMatroska parses a Matroska header: info, tracks and cues.
func openMatroska(ctx context.Context, src byteSource, head []byte) (*mkvDemuxer, error) {
	d := &mkvDemuxer{src: src, head: head, scale: time.Millisecond, tracks: map[uint64]*track{}, memo: newSeekMemo(), first: -1}
	d.info = &mediaInfo{format: "matroska", size: src.size()}
	id, n := readID(head)
	if id != mkvEBML {
		return nil, errors.New("remux: not a Matroska file")
	}
	size, m := readVint(head[n:])
	if m == 0 || size < 0 {
		return nil, errors.New("remux: bad EBML header")
	}
	off := int64(n+m) + size
	sid, sn, ssize, err := d.header(ctx, off)
	if err != nil {
		return nil, err
	}
	if sid != mkvSegment {
		return nil, errors.New("remux: no Matroska segment")
	}
	d.segStart = off + int64(sn)
	d.segEnd = src.size()
	if ssize >= 0 && d.segStart+ssize < d.segEnd {
		d.segEnd = d.segStart + ssize
	}

	var seekInfo, seekTracks, seekCues int64 = -1, -1, -1
	var gotInfo, gotTracks, gotCues bool
	parseSeekHead := func(body []byte) {
		for _, e := range ebmlChildren(body) {
			if e.id != mkvSeek {
				continue
			}
			var sid uint32
			pos := int64(-1)
			for _, c := range ebmlChildren(e.data) {
				switch c.id {
				case mkvSeekID:
					sid = uint32(ebmlUint(c.data))
				case mkvSeekPosition:
					pos = d.segStart + int64(ebmlUint(c.data))
				}
			}
			switch sid {
			case mkvInfo:
				seekInfo = pos
			case mkvTracks:
				seekTracks = pos
			case mkvCues:
				seekCues = pos
			}
		}
	}

	// Walk the segment's children up to the first cluster.
	off = d.segStart
	for off < d.segEnd {
		id, hl, size, err := d.header(ctx, off)
		if err != nil {
			return nil, err
		}
		if id == mkvCluster {
			d.first = off
			break
		}
		if size < 0 {
			return nil, fmt.Errorf("remux: Matroska element %X of unknown size", id)
		}
		switch id {
		case mkvSeekHead, mkvInfo, mkvTracks, mkvCues:
			body, err := d.body(ctx, off+int64(hl), size)
			if err != nil {
				return nil, err
			}
			switch id {
			case mkvSeekHead:
				parseSeekHead(body)
			case mkvInfo:
				d.parseInfo(body)
				gotInfo = true
			case mkvTracks:
				if err := d.parseTracks(body); err != nil {
					return nil, err
				}
				gotTracks = true
			case mkvCues:
				d.parseCues(body)
				gotCues = true
			}
		}
		off += int64(hl) + size
	}
	if d.first < 0 {
		return nil, errors.New("remux: Matroska file has no clusters")
	}
	readAt := func(pos int64, want uint32, parse func([]byte) error) error {
		if pos < 0 {
			return nil
		}
		id, hl, size, err := d.header(ctx, pos)
		if err != nil || id != want || size < 0 {
			return err
		}
		body, err := d.body(ctx, pos+int64(hl), size)
		if err != nil {
			return err
		}
		return parse(body)
	}
	if !gotInfo {
		if err := readAt(seekInfo, mkvInfo, func(b []byte) error { d.parseInfo(b); return nil }); err != nil {
			return nil, err
		}
	}
	if !gotTracks {
		if err := readAt(seekTracks, mkvTracks, d.parseTracks); err != nil {
			return nil, err
		}
	}
	if !gotCues {
		if err := readAt(seekCues, mkvCues, func(b []byte) error { d.parseCues(b); gotCues = true; return nil }); err != nil {
			return nil, err
		}
	}
	if len(d.tracks) == 0 {
		return nil, errors.New("remux: Matroska file has no tracks")
	}
	return d, nil
}

// header reads an element header at off.
func (d *mkvDemuxer) header(ctx context.Context, off int64) (id uint32, hl int, size int64, err error) {
	b, err := d.body(ctx, off, 12)
	if err != nil && len(b) == 0 {
		return 0, 0, 0, err
	}
	id, n := readID(b)
	if n == 0 {
		return 0, 0, 0, fmt.Errorf("remux: bad Matroska element at %d", off)
	}
	size, m := readVint(b[n:])
	if m == 0 {
		return 0, 0, 0, fmt.Errorf("remux: bad Matroska element size at %d", off)
	}
	return id, n + m, size, nil
}

// body returns n bytes at off, from the head buffer or the last block
// read when they have them. Reads are at least 64 KB, so that an element's
// header and body (Cues at the end, Tracks after attachments) come in one
// request.
func (d *mkvDemuxer) body(ctx context.Context, off, n int64) ([]byte, error) {
	if n > 64<<20 {
		return nil, fmt.Errorf("remux: Matroska element of %d bytes is too large", n)
	}
	if off+n <= int64(len(d.head)) {
		return d.head[off : off+n], nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if off >= d.blockOff && off+n <= d.blockOff+int64(len(d.block)) {
		return d.block[off-d.blockOff : off-d.blockOff+n], nil
	}
	want := max(n, 64<<10)
	if size := d.src.size(); size > 0 && off+want > size {
		want = max(size-off, 0)
	}
	b := make([]byte, want)
	k, err := d.src.readAt(ctx, b, off)
	if err == io.EOF && k > 0 {
		err = nil
	}
	d.block, d.blockOff = b[:k], off
	if int64(k) < n {
		return b[:k], err
	}
	return b[:n], err
}

func (d *mkvDemuxer) parseInfo(b []byte) {
	var dur float64
	for _, e := range ebmlChildren(b) {
		switch e.id {
		case mkvTimeScale:
			if v := ebmlUint(e.data); v > 0 {
				d.scale = time.Duration(v)
			}
		case mkvDuration:
			dur = ebmlFloat(e.data)
		}
	}
	d.info.duration = time.Duration(dur * float64(d.scale))
	d.info.durationIncludesStart = d.info.duration > 0
}

func (d *mkvDemuxer) parseTracks(b []byte) error {
	orders := map[TrackKind]int{}
	for _, e := range ebmlChildren(b) {
		if e.id != mkvTrackEntry {
			continue
		}
		t := &track{isDefault: true}
		var typ uint64
		var codecID string
		var priv []byte
		var width, height, channels int
		var rate float64
		var colour *colourInfo
		enabled := true
		var dvType string
		var dvData []byte
		for _, c := range ebmlChildren(e.data) {
			switch c.id {
			case mkvTrackNumber:
				t.id = int64(ebmlUint(c.data))
			case mkvTrackType:
				typ = ebmlUint(c.data)
			case mkvFlagEnabled:
				enabled = ebmlUint(c.data) != 0
			case mkvFlagDefault:
				t.isDefault = ebmlUint(c.data) != 0
			case mkvFlagForced:
				t.forced = ebmlUint(c.data) != 0
			case mkvDefaultDur:
				t.defaultDur = time.Duration(ebmlUint(c.data))
			case mkvCodecDelay:
				t.codecDelay = time.Duration(ebmlUint(c.data))
			case mkvName:
				t.title = ebmlString(c.data)
			case mkvLanguage:
				if t.language == "" {
					t.language = ebmlString(c.data)
				}
			case mkvLanguageBCP:
				t.language = ebmlString(c.data)
			case mkvCodecID:
				codecID = ebmlString(c.data)
			case mkvCodecPrivate:
				priv = c.data
			case mkvVideo:
				for _, v := range ebmlChildren(c.data) {
					switch v.id {
					case mkvPixelWidth:
						width = int(ebmlUint(v.data))
					case mkvPixelHeight:
						height = int(ebmlUint(v.data))
					case mkvColour:
						colour = parseMKVColour(v.data)
					}
				}
			case mkvAudio:
				for _, a := range ebmlChildren(c.data) {
					switch a.id {
					case mkvSampleRate:
						rate = ebmlFloat(a.data)
					case mkvChannels:
						channels = int(ebmlUint(a.data))
					}
				}
			case mkvEncodings:
				if err := t.parseMKVEncodings(c.data); err != nil {
					t.why = err.Error()
				}
			case mkvBlockAddMap:
				for _, a := range ebmlChildren(c.data) {
					switch a.id {
					case mkvBlockAddType:
						v := uint32(ebmlUint(a.data))
						dvType = string([]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
					case mkvBlockAddData:
						dvData = a.data
					}
				}
			}
		}
		if !enabled || t.id == 0 {
			continue
		}
		t.codec = mkvCodec(codecID)
		switch typ {
		case 1:
			t.kind = KindVideo
		case 2:
			t.kind = KindAudio
		case 17:
			t.kind = KindSubtitle
		default:
			continue
		}
		t.order = orders[t.kind]
		orders[t.kind]++
		d.tracks[uint64(t.id)] = t
		why := t.why
		switch t.kind {
		case KindVideo:
			if d.info.video != nil {
				continue // only the first video track plays
			}
			d.info.video = t
			setupVideo(t, priv, width, height)
			if t.out != nil {
				if colour != nil {
					t.out.colour = colour
				}
				if t.defaultDur > 0 {
					t.out.frameRate = float64(time.Second) / float64(t.defaultDur)
				}
				if (dvType == "dvcC" || dvType == "dvvC") && len(dvData) >= 4 {
					applyDolbyVision(t, dvType, dvData)
				}
			}
		case KindAudio:
			d.info.audio = append(d.info.audio, t)
			setupAudio(t, priv, int(math.Round(rate)), channels)
		case KindSubtitle:
			d.info.subs = append(d.info.subs, t)
			if !isTextSubtitle(t.codec) {
				t.why = "graphic subtitles (" + t.codec + ") are not extracted"
			}
		}
		if why != "" {
			t.why, t.out = why, nil
		}
	}
	if d.info.video != nil && d.info.video.out == nil && d.info.video.why == "" {
		d.info.video.why = "unsupported video"
	}
	return nil
}

func parseMKVColour(b []byte) *colourInfo {
	c := &colourInfo{primaries: 2, transfer: 2, matrix: 2}
	seen := false
	for _, e := range ebmlChildren(b) {
		v := uint8(ebmlUint(e.data))
		switch e.id {
		case mkvMatrix:
			c.matrix, seen = v, true
		case mkvTransfer:
			c.transfer, seen = v, true
		case mkvPrimaries:
			c.primaries, seen = v, true
		case mkvRange:
			c.fullRange = v == 2
		}
	}
	if !seen || (c.primaries == 2 && c.transfer == 2 && c.matrix == 2) {
		return nil
	}
	return c
}

func (t *track) parseMKVEncodings(b []byte) error {
	for _, e := range ebmlChildren(b) {
		if e.id != mkvEncoding {
			continue
		}
		var encType uint64
		algo := uint64(0)
		var settings []byte
		hasComp := false
		for _, c := range ebmlChildren(e.data) {
			switch c.id {
			case mkvEncType:
				encType = ebmlUint(c.data)
			case mkvCompression:
				hasComp = true
				for _, x := range ebmlChildren(c.data) {
					switch x.id {
					case mkvCompAlgo:
						algo = ebmlUint(x.data)
					case mkvCompSettings:
						settings = x.data
					}
				}
			}
		}
		if encType == 1 {
			return errors.New("encrypted track")
		}
		if !hasComp {
			continue
		}
		switch algo {
		case 0:
			t.zlib = true
		case 3:
			t.stripped = append([]byte(nil), settings...)
		default:
			return fmt.Errorf("unsupported Matroska compression %d", algo)
		}
	}
	return nil
}

func mkvCodec(id string) string {
	switch {
	case id == "V_MPEG4/ISO/AVC":
		return "h264"
	case id == "V_MPEGH/ISO/HEVC":
		return "hevc"
	case id == "V_AV1":
		return "av1"
	case id == "V_VP9":
		return "vp9"
	case id == "V_VP8":
		return "vp8"
	case strings.HasPrefix(id, "V_MPEG2"), strings.HasPrefix(id, "V_MPEG1"):
		return "mpeg2video"
	case id == "V_MPEG4/ISO/ASP", id == "V_MPEG4/ISO/SP":
		return "mpeg4"
	case strings.HasPrefix(id, "A_AAC"):
		return "aac"
	case id == "A_AC3", id == "A_AC3/BSID9", id == "A_AC3/BSID10":
		return "ac3"
	case id == "A_EAC3":
		return "eac3"
	case strings.HasPrefix(id, "A_DTS"):
		return "dts"
	case id == "A_TRUEHD":
		return "truehd"
	case id == "A_FLAC":
		return "flac"
	case id == "A_OPUS":
		return "opus"
	case id == "A_VORBIS":
		return "vorbis"
	case id == "A_MPEG/L3":
		return "mp3"
	case id == "A_MPEG/L2":
		return "mp2"
	case strings.HasPrefix(id, "A_PCM"):
		return "pcm"
	case id == "S_TEXT/UTF8", id == "S_TEXT/ASCII":
		return "subrip"
	case id == "S_TEXT/ASS":
		return "ass"
	case id == "S_TEXT/SSA":
		return "ssa"
	case id == "S_TEXT/WEBVTT":
		return "webvtt"
	case id == "S_HDMV/PGS":
		return "hdmv_pgs_subtitle"
	case id == "S_VOBSUB":
		return "dvd_subtitle"
	case id == "S_DVBSUB":
		return "dvb_subtitle"
	}
	return strings.ToLower(id)
}

func (d *mkvDemuxer) parseCues(b []byte) {
	videoID := uint64(0)
	if d.info.video != nil {
		videoID = uint64(d.info.video.id)
	}
	for _, cp := range ebmlChildren(b) {
		if cp.id != mkvCuePoint {
			continue
		}
		var t time.Duration
		var poss []ebmlElem
		for _, c := range ebmlChildren(cp.data) {
			switch c.id {
			case mkvCueTime:
				t = time.Duration(ebmlUint(c.data)) * d.scale
			case mkvCueTrackPos:
				poss = append(poss, c)
			}
		}
		for _, p := range poss {
			e := mkvCueEntry{t: t, cluster: -1, rel: -1}
			for _, c := range ebmlChildren(p.data) {
				switch c.id {
				case mkvCueTrack:
					e.track = ebmlUint(c.data)
				case mkvCueCluster:
					e.cluster = d.segStart + int64(ebmlUint(c.data))
				case mkvCueRelPos:
					e.rel = int64(ebmlUint(c.data))
				case mkvCueDuration:
					e.dur = time.Duration(ebmlUint(c.data)) * d.scale
				}
			}
			if e.cluster < 0 {
				continue
			}
			if e.track == videoID {
				d.cues = append(d.cues, e)
			} else if t := d.tracks[e.track]; t != nil && t.kind == KindSubtitle {
				d.subCues = append(d.subCues, e)
			}
		}
	}
	sort.Slice(d.cues, func(i, j int) bool { return d.cues[i].t < d.cues[j].t })
	sort.Slice(d.subCues, func(i, j int) bool { return d.subCues[i].t < d.subCues[j].t })
}

// gop is a guess for files without Cues: segments of 6 s.
func (d *mkvDemuxer) gop() time.Duration {
	if len(d.cues) < 2 {
		return 6 * time.Second
	}
	return 0
}

func (d *mkvDemuxer) keyframes() []keyframe {
	if len(d.cues) < 2 {
		return nil
	}
	kfs := make([]keyframe, 0, len(d.cues))
	for _, c := range d.cues {
		if n := len(kfs); n > 0 && kfs[n-1].t == c.t {
			continue
		}
		kfs = append(kfs, keyframe{t: c.t, pos: c.cluster})
	}
	return kfs
}

// selectTracks sets the audio and subtitle tracks that reads deliver.
func (d *mkvDemuxer) selectTracks(audio *track, subs []*track) {
	d.audio = audio
	d.textSub = subs
}

func (d *mkvDemuxer) probeStarts(ctx context.Context, audio *track) (time.Duration, time.Duration, error) {
	if audio == nil {
		return 0, 0, errors.New("no selected audio track")
	}
	videoFound, audioFound, mediaFound := false, false, false
	var mediaStart, audioStart time.Duration
	previousAudio := d.audio
	d.audio = audio
	defer func() { d.audio = previousAudio }()
	r := d.src.stream(ctx, d.first, 512<<10)
	defer r.Close()
	s := newMKVStream(r, d.first)
	limit := d.first + 32<<20
	err := s.blocks(d, func(t *track, ts time.Duration, key bool, frames [][]byte, dur time.Duration, pos int64) bool {
		if t.kind == KindVideo && !videoFound {
			videoFound = true
			if !mediaFound {
				mediaFound = true
				mediaStart = ts
			} else if ts < mediaStart {
				mediaStart = ts
			}
		} else if t.kind == KindVideo && ts < mediaStart {
			mediaStart = ts
		}
		if t.kind == KindAudio {
			trackStart := ts - t.codecDelay
			if !mediaFound {
				mediaFound, mediaStart = true, trackStart
			} else if trackStart < mediaStart {
				mediaStart = trackStart
			}
			if t == audio && !audioFound {
				audioFound, audioStart = true, trackStart
			}
		}
		return (!videoFound || !audioFound) && s.pos < limit
	})
	if err != nil && err != io.EOF {
		return 0, 0, err
	}
	if !audioFound {
		return 0, 0, errors.New("selected audio has no timestamp at the start of the file")
	}
	if !mediaFound {
		mediaStart = audioStart
	}
	return mediaStart, audioStart, nil
}

// prepare reads the start of the first clusters until every selected
// track that still needs codec parameters has shown a frame.
func (d *mkvDemuxer) prepare(ctx context.Context) error {
	need := func() []*track {
		var n []*track
		for _, t := range []*track{d.info.video, d.audio} {
			if t != nil && t.out != nil && t.needParams {
				n = append(n, t)
			}
		}
		return n
	}
	if len(need()) == 0 {
		return nil
	}
	r := d.src.stream(ctx, d.first, 512<<10)
	defer r.Close()
	s := newMKVStream(r, d.first)
	limit := d.first + 32<<20
	err := s.blocks(d, func(t *track, ts time.Duration, key bool, frames [][]byte, dur time.Duration, pos int64) bool {
		if !t.needParams || t.out == nil || len(frames) == 0 {
			return s.pos < limit
		}
		if t.kind == KindVideo && !key {
			return s.pos < limit
		}
		if err := t.complete(frames[0]); err != nil {
			t.why, t.out = err.Error(), nil
		}
		return len(need()) > 0 && s.pos < limit
	})
	if err != nil && err != io.EOF {
		return err
	}
	for _, t := range need() {
		t.why, t.out = "no codec parameters found at the start of the file", nil
	}
	return nil
}

func (t *track) complete(frame []byte) error {
	if t.kind == KindVideo {
		return completeVideo(t, frame)
	}
	return completeAudio(t, frame)
}

// wanted reports whether reads deliver a track's blocks.
func (d *mkvDemuxer) wanted(t *track) bool {
	if t == nil {
		return false
	}
	if t == d.info.video || t == d.audio {
		return t == d.audio || t.out != nil || t.needParams
	}
	for _, s := range d.textSub {
		if s == t {
			return true
		}
	}
	return false
}

func (d *mkvDemuxer) read(ctx context.Context, seg *segment, f *segFilter) error {
	start := seg.pos
	if seg.pos < 0 {
		done := d.memo.begin(seg.index)
		defer done()
	}
	if seg.first {
		start = d.first
	} else if start < 0 {
		if p, ok := d.memo.startAfter(ctx, seg.index); ok {
			start = p
		} else {
			var err error
			start, err = locate(ctx, d.memo, seekPoint{d.first, 0}, seekPoint{d.segEnd, d.info.duration + time.Second}, seg.start+seg.offset, d.probe)
			if err != nil {
				return err
			}
		}
	}
	hint := int64(4 << 20)
	if seg.endPos > start {
		hint = seg.endPos - start + 1<<20
	} else if d.info.duration > 0 {
		rate := float64(d.segEnd-d.first) / d.info.duration.Seconds()
		hint = int64(rate * (seg.duration() + 2*time.Second).Seconds())
	}
	r := d.src.stream(ctx, start, hint)
	defer r.Close()
	s := newMKVStream(r, start)
	var perr error
	err := s.blocks(d, func(t *track, ts time.Duration, key bool, frames [][]byte, dur time.Duration, pos int64) bool {
		for i, fr := range frames {
			// Audio timestamps include the encoder delay (priming).
			p := &packet{t: t, pts: ts - t.codecDelay, key: key && i == 0, data: fr, pos: pos, dur: dur}
			if len(frames) > 1 && t.out != nil {
				p.pts += time.Duration(i) * t.out.frameDuration()
			}
			if t.kind == KindSubtitle && p.dur == 0 {
				p.dur = t.defaultDur
			}
			done, err := f.push(p)
			if err != nil {
				perr = err
				return false
			}
			if done {
				return false
			}
		}
		// Safety net for files whose tracks end early or interleave
		// loosely: stop well past the end.
		return seg.last || f.lastPTS < seg.end+10*time.Second
	})
	if perr != nil {
		return perr
	}
	if err != nil && err != io.EOF {
		return err
	}
	if !seg.last && f.nextPos >= 0 && seg.pos < 0 {
		d.memo.setStart(seg.index+1, f.nextPos)
	}
	return nil
}

// probe finds the first cluster at or after off and its timestamp.
func (d *mkvDemuxer) probe(ctx context.Context, off int64) (seekPoint, bool, error) {
	const window = 256 << 10
	for scanned := int64(0); scanned < 8<<20 && off < d.segEnd; scanned += window - 16 {
		buf := make([]byte, window)
		n, err := d.src.readAt(ctx, buf, off)
		if err != nil && err != io.EOF {
			return seekPoint{}, false, err
		}
		buf = buf[:n]
		for i := 0; i+12 <= len(buf); i++ {
			if buf[i] != 0x1F || buf[i+1] != 0x43 || buf[i+2] != 0xB6 || buf[i+3] != 0x75 {
				continue
			}
			size, m := readVint(buf[i+4:])
			if m == 0 || (size >= 0 && (size < 4 || size > 256<<20)) {
				continue
			}
			j := i + 4 + m
			// The cluster's first child is its timestamp (or CRC-32).
			for k := 0; k < 2 && j+2 <= len(buf); k++ {
				id, idn := readID(buf[j:])
				if idn == 0 {
					break
				}
				sz, szn := readVint(buf[j+idn:])
				if szn == 0 || sz < 0 || sz > 8 || j+idn+szn+int(sz) > len(buf) {
					break
				}
				if id == mkvTimestamp {
					tc := ebmlUint(buf[j+idn+szn : j+idn+szn+int(sz)])
					return seekPoint{pos: off + int64(i), t: time.Duration(tc) * d.scale}, true, nil
				}
				if id != 0xBF { // CRC-32
					break
				}
				j += idn + szn + int(sz)
			}
		}
		if n < window {
			break
		}
		off += window - 16
	}
	return seekPoint{}, false, nil
}

func (d *mkvDemuxer) subtitlesBefore(ctx context.Context, t time.Duration, emit func(*packet)) error {
	if len(d.textSub) == 0 || len(d.subCues) == 0 {
		return nil
	}
	want := map[uint64]*track{}
	for _, s := range d.textSub {
		want[uint64(s.id)] = s
	}
	// Cue entries of subtitle blocks that started before t and still show.
	var hits []mkvCueEntry
	i := sort.Search(len(d.subCues), func(i int) bool { return d.subCues[i].t >= t })
	for j := i - 1; j >= 0 && t-d.subCues[j].t < 30*time.Second; j-- {
		c := d.subCues[j]
		if want[c.track] != nil && c.rel >= 0 && (c.dur == 0 || c.t+c.dur > t) {
			hits = append(hits, c)
		}
	}
	for _, c := range hits {
		// CueRelativePosition counts from the cluster's data, after its
		// header.
		_, hl, _, err := d.header(ctx, c.cluster)
		if err != nil {
			continue
		}
		buf := make([]byte, 16<<10)
		n, err := d.src.readAt(ctx, buf, c.cluster+int64(hl)+c.rel)
		if err != nil && err != io.EOF {
			return err
		}
		s := newMKVStream(bytes.NewReader(buf[:n]), 0)
		s.clusterTime, s.fixedTime = c.t, true
		_ = s.blocks(d, func(tr *track, ts time.Duration, key bool, frames [][]byte, dur time.Duration, pos int64) bool {
			if tr == want[c.track] && len(frames) > 0 {
				if dur == 0 {
					dur = c.dur
				}
				emit(&packet{t: tr, pts: c.t, dur: dur, data: frames[0]})
			}
			return false
		})
	}
	return nil
}

// mkvStream parses clusters and blocks from a sequential reader.
type mkvStream struct {
	r           *bufio.Reader
	pos         int64
	cluster     int64 // position of the current cluster
	clusterTime time.Duration
	clusterEnd  int64 // -1: unknown size
	fixedTime   bool  // timestamps are the cue's, not the cluster's
}

func newMKVStream(r io.Reader, pos int64) *mkvStream {
	return &mkvStream{r: bufio.NewReaderSize(r, 256<<10), pos: pos, cluster: pos, clusterEnd: -1}
}

func (s *mkvStream) readHeader() (uint32, int64, error) {
	b, err := s.r.Peek(12)
	if len(b) == 0 {
		if err == nil {
			err = io.EOF
		}
		return 0, 0, err
	}
	id, n := readID(b)
	if n == 0 {
		return 0, 0, fmt.Errorf("remux: bad Matroska element at %d", s.pos)
	}
	size, m := readVint(b[n:])
	if m == 0 {
		return 0, 0, fmt.Errorf("remux: bad Matroska size at %d", s.pos)
	}
	s.r.Discard(n + m)
	s.pos += int64(n + m)
	return id, size, nil
}

func (s *mkvStream) skip(n int64) error {
	for n > 0 {
		k := n
		if k > 1<<30 {
			k = 1 << 30
		}
		m, err := s.r.Discard(int(k))
		s.pos += int64(m)
		n -= int64(m)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *mkvStream) bytes(n int64) ([]byte, error) {
	if n > 256<<20 {
		return nil, fmt.Errorf("remux: Matroska block of %d bytes", n)
	}
	b := make([]byte, n)
	k, err := io.ReadFull(s.r, b)
	s.pos += int64(k)
	return b, err
}

// blockFunc receives one block: its frames (several when laced), the
// block's timestamp and duration (0 if none), and the position of its
// cluster. It returns false to stop.
type blockFunc func(t *track, ts time.Duration, key bool, frames [][]byte, dur time.Duration, pos int64) bool

// blocks reads clusters from the current position, calling fn for each
// block of a wanted track.
func (s *mkvStream) blocks(d *mkvDemuxer, fn blockFunc) error {
	for {
		hstart := s.pos
		id, size, err := s.readHeader()
		if err != nil {
			return err
		}
		switch id {
		case mkvSegment:
			continue
		case mkvCluster:
			s.cluster = hstart
			s.clusterEnd = -1
			if size >= 0 {
				s.clusterEnd = s.pos + size
			}
			continue
		case mkvTimestamp:
			b, err := s.bytes(size)
			if err != nil {
				return err
			}
			if !s.fixedTime {
				s.clusterTime = time.Duration(ebmlUint(b)) * d.scale
			}
		case mkvSimpleBlock, mkvBlockGroup:
			more, err := s.block(d, id, size, fn)
			if err != nil {
				return err
			}
			if !more {
				return nil
			}
		case mkvCues, mkvTags, mkvChapters, mkvAttachments:
			// Past the clusters (index or metadata at the end).
			if size < 0 {
				return io.EOF
			}
			if err := s.skip(size); err != nil {
				return err
			}
		default:
			if size < 0 {
				continue
			}
			if err := s.skip(size); err != nil {
				return err
			}
		}
	}
}

func (s *mkvStream) block(d *mkvDemuxer, id uint32, size int64, fn blockFunc) (bool, error) {
	if size < 4 {
		return true, s.skip(max(size, 0))
	}
	// Peek at the track number first, to skip unwanted blocks cheaply.
	hdr, _ := s.r.Peek(int(min(size, 16)))
	var trackNum int64
	if id == mkvSimpleBlock {
		n, m := readVint(hdr)
		if m == 0 {
			return true, s.skip(size)
		}
		trackNum = n
	} else {
		// BlockGroup: the Block child usually comes first.
		cid, cn := readID(hdr)
		if cid == mkvBlock {
			_, sn := readVint(hdr[cn:])
			if sn > 0 {
				n, m := readVint(hdr[cn+sn:])
				if m > 0 {
					trackNum = n
				}
			}
		}
	}
	if trackNum > 0 {
		if t := d.tracks[uint64(trackNum)]; !d.wanted(t) {
			return true, s.skip(size)
		}
	}
	body, err := s.bytes(size)
	if err != nil {
		return false, err
	}
	var blk []byte
	var dur time.Duration
	key := false
	if id == mkvSimpleBlock {
		blk = body
	} else {
		hasRef := false
		for _, c := range ebmlChildren(body) {
			switch c.id {
			case mkvBlock:
				blk = c.data
			case mkvBlockDur:
				dur = time.Duration(ebmlUint(c.data)) * d.scale
			case mkvRefBlock:
				hasRef = true
			}
		}
		key = !hasRef
	}
	if len(blk) < 4 {
		return true, nil
	}
	tn, m := readVint(blk)
	if m == 0 || len(blk) < m+3 {
		return true, nil
	}
	t := d.tracks[uint64(tn)]
	if !d.wanted(t) {
		return true, nil
	}
	rel := int16(binary.BigEndian.Uint16(blk[m:]))
	flags := blk[m+2]
	if id == mkvSimpleBlock {
		key = flags&0x80 != 0
	}
	ts := s.clusterTime + time.Duration(rel)*d.scale
	if s.fixedTime {
		ts = s.clusterTime
	}
	frames, err := unlace(blk[m+3:], flags)
	if err != nil {
		return true, nil // a damaged block: skip it
	}
	for i, fr := range frames {
		if t.zlib {
			zr, err := zlib.NewReader(bytes.NewReader(fr))
			if err != nil {
				return true, nil
			}
			fr, err = io.ReadAll(io.LimitReader(zr, 64<<20))
			if err != nil {
				return true, nil
			}
		}
		if len(t.stripped) > 0 {
			fr = append(append(make([]byte, 0, len(t.stripped)+len(fr)), t.stripped...), fr...)
		}
		frames[i] = fr
	}
	return fn(t, ts, key, frames, dur, s.cluster), nil
}

// unlace splits a block's payload into frames.
func unlace(b []byte, flags byte) ([][]byte, error) {
	lacing := (flags >> 1) & 3
	if lacing == 0 {
		return [][]byte{b}, nil
	}
	if len(b) < 1 {
		return nil, errors.New("short lace")
	}
	n := int(b[0]) + 1
	b = b[1:]
	sizes := make([]int, n)
	switch lacing {
	case 1: // Xiph
		for i := 0; i < n-1; i++ {
			for {
				if len(b) == 0 {
					return nil, errors.New("short lace")
				}
				v := int(b[0])
				b = b[1:]
				sizes[i] += v
				if v != 255 {
					break
				}
			}
		}
	case 3: // EBML
		v, m := readVint(b)
		if m == 0 || v < 0 {
			return nil, errors.New("bad lace")
		}
		sizes[0] = int(v)
		b = b[m:]
		for i := 1; i < n-1; i++ {
			raw, m := readVint(b)
			if m == 0 || raw < 0 {
				return nil, errors.New("bad lace")
			}
			delta := raw - (int64(1)<<(7*uint(m)-1) - 1)
			sizes[i] = sizes[i-1] + int(delta)
			b = b[m:]
		}
	case 2: // fixed
		if len(b)%n != 0 {
			return nil, errors.New("bad fixed lace")
		}
		for i := range sizes {
			sizes[i] = len(b) / n
		}
	}
	if lacing != 2 {
		total := 0
		for _, s := range sizes[:n-1] {
			if s < 0 {
				return nil, errors.New("bad lace")
			}
			total += s
		}
		if total > len(b) {
			return nil, errors.New("short lace")
		}
		sizes[n-1] = len(b) - total
	}
	frames := make([][]byte, n)
	for i, s := range sizes {
		frames[i] = b[:s]
		b = b[s:]
	}
	return frames, nil
}

// applyDolbyVision marks profile 5 streams (no HDR10 base layer) as dvh1
// so AVPlayer reshapes them; other profiles play as their HEVC base layer.
func applyDolbyVision(t *track, box string, cfg []byte) {
	profile := cfg[2] >> 1
	if profile != 5 || t.out == nil || t.out.fourcc != "hvc1" {
		return
	}
	level := (cfg[2]&1)<<5 | cfg[3]>>3
	t.out.fourcc = "dvh1"
	t.out.dvBox = box
	t.out.dvConfig = append([]byte(nil), cfg...)
	t.out.codecs = fmt.Sprintf("dvh1.%02d.%02d", profile, level)
}
