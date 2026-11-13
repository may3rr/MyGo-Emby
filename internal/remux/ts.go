package remux

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

const (
	tsPacket  = 188
	ptsMask   = 1<<33 - 1
	ptsWrap   = 1 << 33
	tsHeadMax = 16 << 20
	tsGOPScan = 8 << 20
)

// tsStream is one elementary stream of a transport stream.
type tsStream struct {
	*track
	pid    int
	stype  byte
	pesBuf []byte
	pesPos int64
	pesPTS int64 // -1 if none
	pesDTS int64
	pesLen int // expected payload length, 0 if unbounded
	active bool
	rai    bool
	// eac3 frames waiting to complete an access unit
}

type tsDemuxer struct {
	src       byteSource
	info      *mediaInfo
	pktSize   int   // 188 or 192 (M2TS)
	syncOff   int   // offset of the 0x47 byte in a packet
	dataStart int64 // first packet
	startPTS  int64
	starts    map[*track]time.Duration
	streams   map[int]*tsStream
	video     *tsStream
	audio     *tsStream
	pmtPID    int
	memo      *seekMemo
	gopLen    time.Duration
}

// detectTS finds the packet size and first packet of a transport stream.
func detectTS(b []byte) (pktSize, syncOff int, start int64, ok bool) {
	for _, size := range []int{188, 192} {
		off := 0
		if size == 192 {
			off = 4
		}
		for i := 0; i < size && i+off+size*4 < len(b); i++ {
			good := true
			for k := 0; k < 5; k++ {
				p := i + off + k*size
				if p >= len(b) || b[p] != 0x47 {
					good = false
					break
				}
			}
			if good {
				return size, off, int64(i), true
			}
		}
	}
	return 0, 0, 0, false
}

func openTS(ctx context.Context, src byteSource, head []byte) (*tsDemuxer, error) {
	pktSize, syncOff, start, ok := detectTS(head)
	if !ok {
		return nil, errors.New("remux: not an MPEG transport stream")
	}
	d := &tsDemuxer{src: src, pktSize: pktSize, syncOff: syncOff, dataStart: start,
		streams: map[int]*tsStream{}, starts: map[*track]time.Duration{}, pmtPID: -1, memo: newSeekMemo(), startPTS: -1}
	d.info = &mediaInfo{format: "mpegts", size: src.size()}
	// Read the start until the program is known and every stream showed
	// its codec parameters and first timestamp.
	firstPTS := map[int]int64{}
	// The head is in memory already; read on after it only if needed.
	rest := src.stream(ctx, int64(len(head)), 1<<20)
	defer rest.Close()
	sc := d.newScanner(io.MultiReader(bytes.NewReader(head[start:]), rest), start)
	pmtDone := false
	var keys []int64 // first video keyframe times, for the GOP length
	err := sc.run(func(p *tsPacket2) bool {
		if p.pid == 0 && p.pusi && d.pmtPID < 0 {
			d.parsePAT(p.payload)
		} else if p.pid == d.pmtPID && p.pusi && !pmtDone {
			pmtDone = d.parsePMT(p.payload)
		}
		return sc.pos < start+tsHeadMax
	}, func(s *tsStream, pes *pesUnit) bool {
		if _, ok := firstPTS[s.pid]; !ok && pes.pts >= 0 {
			firstPTS[s.pid] = pes.pts
		}
		if s.kind == KindVideo && len(keys) < 2 && pes.pts >= 0 && isKeyAU(s.track, pes.data) {
			keys = append(keys, pes.pts)
		}
		if s.needParams && s.out != nil {
			switch s.kind {
			case KindVideo:
				if isKeyAU(s.track, pes.data) {
					if err := completeVideo(s.track, pes.data); err != nil {
						s.why, s.out = err.Error(), nil
					}
				}
			case KindAudio:
				if err := completeAudio(s.track, pes.data); err != nil {
					s.why, s.out = err.Error(), nil
				}
			}
		}
		for _, x := range d.streams {
			if (x.kind == KindVideo || x.kind == KindAudio) && x.out != nil && (x.needParams || !hasKey(firstPTS, x.pid)) {
				return sc.pos < start+tsHeadMax
			}
		}
		if len(keys) < 2 && d.video != nil && d.video.out != nil && sc.pos < start+tsGOPScan {
			return true
		}
		return !pmtDone && sc.pos < start+tsHeadMax
	}, nil)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if !pmtDone {
		return nil, errors.New("remux: transport stream has no program map")
	}
	for _, s := range d.streams {
		if s.needParams && s.out != nil {
			s.why, s.out = "no codec parameters found at the start of the stream", nil
		}
	}
	for pid, pts := range firstPTS {
		if s := d.streams[pid]; s != nil && (s.kind == KindVideo || s.kind == KindAudio) && s.out != nil {
			if d.startPTS < 0 || ptsBefore(pts, d.startPTS) {
				d.startPTS = pts
			}
		}
	}
	if d.startPTS < 0 {
		return nil, errors.New("remux: transport stream has no timestamps")
	}
	for pid, pts := range firstPTS {
		if s := d.streams[pid]; s != nil && (s.kind == KindAudio || s.kind == KindVideo) && s.out != nil {
			d.starts[s.track] = d.norm(pts)
			if s.kind == KindAudio {
				s.out.anchor = d.norm(pts)
			}
		}
	}
	if len(keys) == 2 {
		d.gopLen = time.Duration((keys[1]-keys[0])&ptsMask) * time.Second / 90000
	}
	d.info.duration = d.tailDuration(ctx)
	return d, nil
}

// gop is the keyframe interval seen at the start of the stream.
func (d *tsDemuxer) gop() time.Duration {
	if d.gopLen > 20*time.Second {
		return 20 * time.Second
	}
	return d.gopLen
}

func (d *tsDemuxer) probeStarts(_ context.Context, audio *track) (time.Duration, time.Duration, error) {
	audioStart, ok := d.starts[audio]
	if !ok {
		return 0, 0, errors.New("selected audio has no timestamp at the start of the stream")
	}
	mediaStart := audioStart
	for track, start := range d.starts {
		if track == nil {
			continue
		}
		if start < mediaStart {
			mediaStart = start
		}
	}
	return mediaStart, audioStart, nil
}

func hasKey(m map[int]int64, k int) bool { _, ok := m[k]; return ok }

// ptsBefore compares 33-bit timestamps across a wrap.
func ptsBefore(a, b int64) bool {
	d := (a - b) & ptsMask
	return d > ptsWrap/2
}

// norm converts a 90 kHz timestamp to time since the stream start.
func (d *tsDemuxer) norm(pts int64) time.Duration {
	v := (pts - d.startPTS) & ptsMask
	if v > ptsWrap/2 {
		v -= ptsWrap
	}
	return time.Duration(v) * time.Second / 90000
}

func (d *tsDemuxer) parsePAT(b []byte) {
	sec := psiSection(b)
	if len(sec) < 12 {
		return
	}
	for p := 8; p+4 <= len(sec)-4; p += 4 {
		prog := int(sec[p])<<8 | int(sec[p+1])
		pid := int(sec[p+2]&0x1f)<<8 | int(sec[p+3])
		if prog != 0 {
			d.pmtPID = pid
			return
		}
	}
}

// psiSection returns the section of a PSI payload (pointer field skipped).
func psiSection(b []byte) []byte {
	if len(b) < 1 {
		return nil
	}
	ptr := int(b[0])
	if 1+ptr >= len(b) {
		return nil
	}
	sec := b[1+ptr:]
	if len(sec) < 3 {
		return nil
	}
	n := int(sec[1]&0x0f)<<8 | int(sec[2])
	if 3+n > len(sec) {
		return sec
	}
	return sec[:3+n]
}

func (d *tsDemuxer) parsePMT(b []byte) bool {
	sec := psiSection(b)
	if len(sec) < 16 || sec[0] != 0x02 {
		return false
	}
	progInfo := int(sec[10]&0x0f)<<8 | int(sec[11])
	p := 12 + progInfo
	end := len(sec) - 4
	orders := map[TrackKind]int{}
	for p+5 <= end {
		stype := sec[p]
		pid := int(sec[p+1]&0x1f)<<8 | int(sec[p+2])
		esLen := int(sec[p+3]&0x0f)<<8 | int(sec[p+4])
		desc := sec[p+5 : min(p+5+esLen, end)]
		p += 5 + esLen
		s := &tsStream{pid: pid, stype: stype, track: &track{id: int64(pid), isDefault: true}}
		codec, kind := tsCodec(stype, desc)
		if codec == "" {
			continue
		}
		s.codec, s.kind = codec, kind
		for q := 0; q+2 <= len(desc); {
			tag, l := desc[q], int(desc[q+1])
			if q+2+l > len(desc) {
				break
			}
			if tag == 0x0a && l >= 3 {
				s.language = string(desc[q+2 : q+5])
			}
			q += 2 + l
		}
		s.order = orders[kind]
		orders[kind]++
		switch kind {
		case KindVideo:
			if d.video != nil {
				continue
			}
			setupVideo(s.track, nil, 0, 0)
			if s.out != nil {
				s.needParams, s.lengthSize = true, 0
			}
			d.video = s
			d.info.video = s.track
		case KindAudio:
			switch codec {
			case "aac":
				s.needParams = true
				s.out = &outTrack{fourcc: "mp4a", frameSamples: 1024}
			case "ac3", "eac3":
				setupAudio(s.track, nil, 0, 0)
			default:
				s.why = "audio codec " + codec + " is not supported by AVPlayer without transcoding"
			}
			d.info.audio = append(d.info.audio, s.track)
		case KindSubtitle:
			s.why = "subtitle format " + codec + " is not extracted"
			d.info.subs = append(d.info.subs, s.track)
		}
		d.streams[pid] = s
	}
	return len(d.streams) > 0
}

func tsCodec(stype byte, desc []byte) (string, TrackKind) {
	hasDesc := func(tag byte) bool {
		for q := 0; q+2 <= len(desc); q += 2 + int(desc[q+1]) {
			if desc[q] == tag {
				return true
			}
		}
		return false
	}
	reg := ""
	for q := 0; q+2 <= len(desc); q += 2 + int(desc[q+1]) {
		if desc[q] == 0x05 && desc[q+1] >= 4 && q+6 <= len(desc) {
			reg = string(desc[q+2 : q+6])
		}
	}
	switch stype {
	case 0x01, 0x02:
		return "mpeg2video", KindVideo
	case 0x10:
		return "mpeg4", KindVideo
	case 0x1b:
		return "h264", KindVideo
	case 0x24:
		return "hevc", KindVideo
	case 0xea:
		return "vc1", KindVideo
	case 0x03, 0x04:
		return "mp2", KindAudio
	case 0x0f:
		return "aac", KindAudio
	case 0x11:
		return "aac_latm", KindAudio
	case 0x81:
		return "ac3", KindAudio
	case 0x87, 0x84, 0xa1:
		return "eac3", KindAudio
	case 0x82, 0x85, 0x86, 0xa2:
		return "dts", KindAudio
	case 0x83:
		return "truehd", KindAudio
	case 0x80:
		return "pcm_bluray", KindAudio
	case 0x90:
		return "hdmv_pgs_subtitle", KindSubtitle
	case 0x92:
		return "hdmv_text_subtitle", KindSubtitle
	case 0x06:
		switch {
		case hasDesc(0x6a) || reg == "AC-3":
			return "ac3", KindAudio
		case hasDesc(0x7a) || reg == "EAC3":
			return "eac3", KindAudio
		case hasDesc(0x7b) || reg == "DTS1" || reg == "DTS2" || reg == "DTS3":
			return "dts", KindAudio
		case hasDesc(0x59):
			return "dvb_subtitle", KindSubtitle
		case hasDesc(0x56):
			return "dvb_teletext", KindSubtitle
		}
	}
	return "", 0
}

// tailDuration finds the last video (or audio) timestamp near the end.
func (d *tsDemuxer) tailDuration(ctx context.Context) time.Duration {
	size := d.src.size()
	if size <= 0 {
		return 0
	}
	const tail = 2 << 20
	off := max(d.dataStart, size-tail)
	off = d.dataStart + (off-d.dataStart)/int64(d.pktSize)*int64(d.pktSize)
	r := d.src.stream(ctx, off, size-off)
	defer r.Close()
	var last time.Duration
	sc := d.newScanner(r, off)
	sc.headersOnly = true
	_ = sc.run(nil, nil, func(s *tsStream, pts, dts int64, pos int64) {
		if s.out == nil {
			return
		}
		if t := d.norm(pts); t > last {
			last = t
		}
	})
	if last <= 0 {
		return 0
	}
	if d.video != nil && d.video.out != nil {
		fr := d.video.out.frameRate
		if fr <= 0 {
			fr = 25
		}
		last += time.Duration(float64(time.Second) / fr)
	}
	return last
}

// tsPacket2 is one parsed transport packet.
type tsPacket2 struct {
	pid     int
	pusi    bool
	rai     bool
	payload []byte
	pos     int64
}

// pesUnit is one complete PES packet of a selected stream.
type pesUnit struct {
	pts, dts int64 // -1 if absent
	data     []byte
	pos      int64
	rai      bool
}

// tsScanner reads packets sequentially and assembles PES packets.
type tsScanner struct {
	d   *tsDemuxer
	r   *bufio.Reader
	pos int64
	buf []byte
	// headersOnly reports PES headers without assembling payloads.
	headersOnly bool
}

func (d *tsDemuxer) newScanner(r io.Reader, pos int64) *tsScanner {
	return &tsScanner{d: d, r: bufio.NewReaderSize(r, 256<<10), pos: pos, buf: make([]byte, d.pktSize)}
}

// run reads packets. onPacket sees every packet; onPES complete PES
// packets of the streams with a track; onHeader each PES header (with
// headersOnly). Either callback returning false stops the scan.
func (sc *tsScanner) run(onPacket func(*tsPacket2) bool, onPES func(*tsStream, *pesUnit) bool, onHeader func(*tsStream, int64, int64, int64)) error {
	d := sc.d
	resync := 0
	for {
		if _, err := io.ReadFull(sc.r, sc.buf); err != nil {
			if err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			if err == io.EOF && onPES != nil {
				// Flush what is pending at the end of the stream.
				for _, s := range d.streams {
					if s.active && len(s.pesBuf) > 0 {
						if !onPES(s, sc.finish(s)) {
							break
						}
					}
				}
			}
			return err
		}
		pktPos := sc.pos
		sc.pos += int64(d.pktSize)
		b := sc.buf[d.syncOff:]
		if b[0] != 0x47 {
			// Lost sync: skip ahead byte by byte (rare, corrupt data).
			resync++
			if resync > 64 {
				return errors.New("remux: transport stream lost sync")
			}
			if _, err := sc.r.Discard(1); err != nil {
				return err
			}
			sc.pos++
			continue
		}
		resync = 0
		p := tsPacket2{pid: int(b[1]&0x1f)<<8 | int(b[2]), pusi: b[1]&0x40 != 0, pos: pktPos}
		afc := (b[3] >> 4) & 3
		payload := b[4:tsPacket]
		if afc&2 != 0 {
			al := int(payload[0])
			if al > 0 && len(payload) > 1 {
				p.rai = payload[1]&0x40 != 0
			}
			if 1+al > len(payload) {
				continue
			}
			payload = payload[1+al:]
		}
		if afc&1 == 0 {
			payload = nil
		}
		p.payload = payload
		if onPacket != nil && !onPacket(&p) {
			return nil
		}
		s := d.streams[p.pid]
		if s == nil || len(payload) == 0 {
			continue
		}
		if sc.headersOnly {
			if p.pusi {
				if pts, dts, _, ok := parsePESHeader(payload); ok && pts >= 0 && onHeader != nil {
					onHeader(s, pts, dts, pktPos)
				}
			}
			continue
		}
		if onPES == nil || s.out == nil {
			continue
		}
		if p.pusi {
			if s.active && len(s.pesBuf) > 0 {
				if !onPES(s, sc.finish(s)) {
					return nil
				}
			}
			pts, dts, hdr, ok := parsePESHeader(payload)
			if !ok {
				s.active = false
				continue
			}
			s.active = true
			s.pesPTS, s.pesDTS, s.pesPos, s.rai = pts, dts, pktPos, p.rai
			s.pesBuf = append(make([]byte, 0, 64<<10), payload[hdr:]...)
			s.pesLen = 0
			if l := int(payload[4])<<8 | int(payload[5]); l > 0 {
				s.pesLen = l - (hdr - 6)
			}
		} else if s.active {
			s.pesBuf = append(s.pesBuf, payload...)
		}
		if s.active && s.pesLen > 0 && len(s.pesBuf) >= s.pesLen {
			s.pesBuf = s.pesBuf[:s.pesLen]
			if !onPES(s, sc.finish(s)) {
				return nil
			}
		}
	}
}

func (sc *tsScanner) finish(s *tsStream) *pesUnit {
	u := &pesUnit{pts: s.pesPTS, dts: s.pesDTS, data: s.pesBuf, pos: s.pesPos, rai: s.rai}
	s.pesBuf = nil
	s.active = false
	return u
}

// parsePESHeader returns the timestamps and header length of a PES packet.
func parsePESHeader(b []byte) (pts, dts int64, hdr int, ok bool) {
	if len(b) < 9 || b[0] != 0 || b[1] != 0 || b[2] != 1 {
		return 0, 0, 0, false
	}
	pts, dts = -1, -1
	sid := b[3]
	if sid == 0xbc || sid == 0xbe || sid == 0xbf || sid == 0xf0 || sid == 0xf1 || sid == 0xff || sid == 0xf2 || sid == 0xf8 {
		return pts, dts, 6, true
	}
	flags := b[7] >> 6
	hdr = 9 + int(b[8])
	if hdr > len(b) {
		return 0, 0, 0, false
	}
	if flags&2 != 0 && len(b) >= 14 {
		pts = readTimestamp(b[9:])
	}
	if flags == 3 && len(b) >= 19 {
		dts = readTimestamp(b[14:])
	}
	return pts, dts, hdr, true
}

func readTimestamp(b []byte) int64 {
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

// isKeyAU reports whether an Annex B access unit starts a GOP: an IDR or
// IRAP picture, or for H.264 broadcasts without IDRs an I slice that
// comes with its SPS.
func isKeyAU(t *track, au []byte) bool {
	nals := splitAnnexB(au)
	switch t.codec {
	case "h264":
		if h264IsIDR(nals) {
			return true
		}
		hasSPS := false
		for _, n := range nals {
			switch n[0] & 0x1f {
			case h264NALSPS:
				hasSPS = true
			case h264NALSlice:
				if !hasSPS || len(n) < 2 {
					return false
				}
				r := &bitReader{b: unescapeRBSP(n[1:min(len(n), 16)])}
				r.ue()
				st := r.ue() % 5
				return st == 2 || st == 4
			}
		}
	case "hevc":
		for _, n := range nals {
			t := hevcNALType(n)
			if t < 32 {
				return hevcIsIRAP(t)
			}
		}
	}
	return false
}

// annexBToSample converts an access unit to 4-byte length prefixes and
// drops access unit delimiters.
func annexBToSample(codec string, au []byte) []byte {
	nals := splitAnnexB(au)
	n := 0
	for _, x := range nals {
		n += 4 + len(x)
	}
	out := make([]byte, 0, n)
	for _, x := range nals {
		if codec == "h264" && x[0]&0x1f == h264NALAUD || codec == "hevc" && hevcNALType(x) == hevcNALAUD {
			continue
		}
		out = appendLengthPrefixed(out, x)
	}
	return out
}

func (d *tsDemuxer) keyframes() []keyframe { return nil }

func (d *tsDemuxer) selectTracks(audio *track, subs []*track) {
	d.audio = nil
	for _, s := range d.streams {
		if s.track == audio {
			d.audio = s
		}
	}
}

func (d *tsDemuxer) read(ctx context.Context, seg *segment, f *segFilter) error {
	start := d.dataStart
	done := d.memo.begin(seg.index)
	defer done()
	if !seg.first {
		if p, ok := d.memo.startAfter(ctx, seg.index); ok {
			start = p
		} else {
			var err error
			start, err = locate(ctx, d.memo, seekPoint{d.dataStart, 0}, seekPoint{d.src.size(), d.info.duration + time.Second}, seg.start+seg.offset, d.probe)
			if err != nil {
				return err
			}
		}
	}
	hint := int64(4 << 20)
	if d.info.duration > 0 && d.src.size() > 0 {
		rate := float64(d.src.size()-d.dataStart) / d.info.duration.Seconds()
		hint = int64(rate * (seg.duration() + 2*time.Second).Seconds())
	}
	r := d.src.stream(ctx, start, hint)
	defer r.Close()
	sc := d.newScanner(r, start)
	var perr error
	// Only the selected streams are assembled, each read with its own
	// PES state.
	streams := d.cloneStreams()
	sc.d = &tsDemuxer{src: d.src, info: d.info, pktSize: d.pktSize, syncOff: d.syncOff, startPTS: d.startPTS, streams: streams}
	video, audio := streams[pidOf(d.video)], streams[pidOf(d.audio)]
	err := sc.run(nil, func(s *tsStream, pes *pesUnit) bool {
		if pes.pts < 0 {
			return true
		}
		switch {
		case s == video && s != nil:
			key := isKeyAU(s.track, pes.data)
			p := &packet{t: d.video.track, pts: d.norm(pes.pts), key: key, data: annexBToSample(s.codec, pes.data), pos: pes.pos}
			if pes.dts >= 0 {
				d.memo.add(seekPoint{pes.pos, d.norm(pes.dts)})
			}
			done, err := f.push(p)
			if err != nil {
				perr = err
				return false
			}
			if done {
				return false
			}
		case s == audio && s != nil:
			for i, fr := range d.audioFrames(s.track, pes.data) {
				p := &packet{t: d.audio.track, pts: d.norm(pes.pts) + time.Duration(i)*d.audio.out.frameDuration(), data: fr, pos: pes.pos}
				done, err := f.push(p)
				if err != nil {
					perr = err
					return false
				}
				if done {
					return false
				}
			}
		}
		return seg.last || f.lastPTS < seg.end+10*time.Second
	}, nil)
	if perr != nil {
		return perr
	}
	if err != nil && err != io.EOF {
		return err
	}
	if !seg.last && f.nextPos >= 0 {
		d.memo.setStart(seg.index+1, f.nextPos)
	}
	return nil
}

func pidOf(s *tsStream) int {
	if s == nil {
		return -1
	}
	return s.pid
}

// cloneStreams copies the selected streams' state for one read, so that
// concurrent reads do not share PES buffers.
func (d *tsDemuxer) cloneStreams() map[int]*tsStream {
	m := map[int]*tsStream{}
	for _, s := range []*tsStream{d.video, d.audio} {
		if s != nil && s.out != nil {
			c := &tsStream{track: s.track, pid: s.pid, stype: s.stype}
			m[s.pid] = c
		}
	}
	return m
}

// audioFrames splits a PES payload into access units without headers.
func (d *tsDemuxer) audioFrames(t *track, b []byte) [][]byte {
	var out [][]byte
	switch t.codec {
	case "aac":
		for len(b) >= 7 {
			h, err := parseADTS(b)
			if err != nil || h.frameLen > len(b) {
				break
			}
			out = append(out, b[h.headerLen:h.frameLen])
			b = b[h.frameLen:]
		}
	case "ac3":
		out = ac3Frames(b)
	case "eac3":
		// An access unit is an independent frame and its dependents.
		for _, fr := range ac3Frames(b) {
			h, err := parseAC3Header(fr)
			if err == nil && h.eac3 && h.strmtyp == 1 && len(out) > 0 {
				out[len(out)-1] = append(out[len(out)-1], fr...)
				continue
			}
			out = append(out, append([]byte(nil), fr...))
		}
	}
	return out
}

// probe finds the first video PES at or after off and its decode time.
func (d *tsDemuxer) probe(ctx context.Context, off int64) (seekPoint, bool, error) {
	if d.video == nil {
		return seekPoint{}, false, nil
	}
	off = d.dataStart + (off-d.dataStart+int64(d.pktSize)-1)/int64(d.pktSize)*int64(d.pktSize)
	const window = 512 << 10
	buf := make([]byte, window)
	n, err := d.src.readAt(ctx, buf, off)
	if err != nil && err != io.EOF {
		return seekPoint{}, false, err
	}
	buf = buf[:n]
	for i := 0; i+d.pktSize <= len(buf); i += d.pktSize {
		b := buf[i+d.syncOff:]
		if b[0] != 0x47 || b[1]&0x40 == 0 {
			continue
		}
		pid := int(b[1]&0x1f)<<8 | int(b[2])
		if pid != d.video.pid {
			continue
		}
		afc := (b[3] >> 4) & 3
		payload := b[4:tsPacket]
		if afc&2 != 0 {
			al := int(payload[0])
			if 1+al > len(payload) {
				continue
			}
			payload = payload[1+al:]
		}
		pts, dts, _, ok := parsePESHeader(payload)
		if !ok || pts < 0 {
			continue
		}
		if dts < 0 {
			dts = pts
		}
		return seekPoint{pos: off + int64(i), t: d.norm(dts)}, true, nil
	}
	return seekPoint{}, false, nil
}

func (d *tsDemuxer) subtitlesBefore(ctx context.Context, t time.Duration, emit func(*packet)) error {
	return nil
}
