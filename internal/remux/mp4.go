package remux

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// mp4Track is a track of an MP4/MOV file with its sample table.
type mp4Track struct {
	*track
	timescale uint32
	shift     int64 // media time of presentation zero (edit list)
	fourcc    string

	offsets []int64
	sizes   []uint32
	dts     []int64
	cto     []int32  // nil without ctts
	sync    []uint32 // sample indices of sync samples; nil when all are
	syncPTS []time.Duration
}

func (m *mp4Track) n() int { return len(m.sizes) }

func (m *mp4Track) ptsOf(i int) time.Duration {
	v := m.dts[i] - m.shift
	if m.cto != nil {
		v += int64(m.cto[i])
	}
	return fromTimescale(v, m.timescale)
}

func (m *mp4Track) durOf(i int) time.Duration {
	if i+1 < len(m.dts) {
		return fromTimescale(m.dts[i+1]-m.dts[i], m.timescale)
	}
	if i > 0 {
		return fromTimescale(m.dts[i]-m.dts[i-1], m.timescale)
	}
	return 0
}

func (m *mp4Track) isSync(i int) bool {
	if m.sync == nil {
		return true
	}
	j := sort.Search(len(m.sync), func(j int) bool { return m.sync[j] >= uint32(i) })
	return j < len(m.sync) && m.sync[j] == uint32(i)
}

type mp4Demuxer struct {
	src    byteSource
	info   *mediaInfo
	video  *mp4Track
	audios []*mp4Track
	subs   []*mp4Track
	byTrk  map[*track]*mp4Track

	audio   *mp4Track
	textSub []*mp4Track
}

type mp4Box struct {
	typ  string
	data []byte
}

// mp4Boxes parses the boxes in b.
func mp4Boxes(b []byte) []mp4Box {
	var out []mp4Box
	for len(b) >= 8 {
		size := int64(binary.BigEndian.Uint32(b))
		typ := string(b[4:8])
		hl := int64(8)
		switch size {
		case 1:
			if len(b) < 16 {
				return out
			}
			size = int64(binary.BigEndian.Uint64(b[8:]))
			hl = 16
		case 0:
			size = int64(len(b))
		}
		if size < hl || size > int64(len(b)) {
			return out
		}
		out = append(out, mp4Box{typ, b[hl:size]})
		b = b[size:]
	}
	return out
}

func mp4Child(b []byte, typ string) []byte {
	for _, c := range mp4Boxes(b) {
		if c.typ == typ {
			return c.data
		}
	}
	return nil
}

// openMP4 finds and parses the moov box.
func openMP4(ctx context.Context, src byteSource, head []byte) (*mp4Demuxer, error) {
	size := src.size()
	var moov []byte
	off := int64(0)
	for off+8 <= size || (size < 0 && off+8 <= int64(len(head))) {
		var hdr []byte
		if off+16 <= int64(len(head)) {
			hdr = head[off : off+16]
		} else {
			buf := make([]byte, 16)
			n, err := src.readAt(ctx, buf, off)
			if n < 8 {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return nil, fmt.Errorf("remux: MP4 box at %d: %w", off, err)
			}
			hdr = buf[:n]
		}
		bs := int64(binary.BigEndian.Uint32(hdr))
		typ := string(hdr[4:8])
		hl := int64(8)
		if bs == 1 && len(hdr) >= 16 {
			bs = int64(binary.BigEndian.Uint64(hdr[8:]))
			hl = 16
		} else if bs == 0 {
			bs = size - off
		}
		if bs < hl {
			return nil, fmt.Errorf("remux: bad MP4 box %q at %d", typ, off)
		}
		if typ == "moov" {
			if bs > 256<<20 {
				return nil, errors.New("remux: moov box too large")
			}
			if off+bs <= int64(len(head)) {
				moov = head[off+hl : off+bs]
			} else {
				moov = make([]byte, bs-hl)
				if _, err := src.readAt(ctx, moov, off+hl); err != nil && err != io.EOF {
					return nil, err
				}
			}
			break
		}
		off += bs
	}
	if moov == nil {
		return nil, errors.New("remux: MP4 file has no moov box")
	}
	if mp4Child(moov, "mvex") != nil {
		return nil, errors.New("remux: fragmented MP4 sources are not supported")
	}
	d := &mp4Demuxer{src: src, info: &mediaInfo{format: "mp4", size: size}, byTrk: map[*track]*mp4Track{}}
	var movieScale uint32 = 1000
	if mvhd := mp4Child(moov, "mvhd"); len(mvhd) >= 20 {
		if mvhd[0] == 1 && len(mvhd) >= 32 {
			movieScale = binary.BigEndian.Uint32(mvhd[20:])
			d.info.duration = fromTimescale(int64(binary.BigEndian.Uint64(mvhd[24:])), movieScale)
		} else {
			movieScale = binary.BigEndian.Uint32(mvhd[12:])
			d.info.duration = fromTimescale(int64(binary.BigEndian.Uint32(mvhd[16:])), movieScale)
		}
	}
	orders := map[TrackKind]int{}
	for _, b := range mp4Boxes(moov) {
		if b.typ != "trak" {
			continue
		}
		m, err := parseTrak(b.data, movieScale)
		if err != nil || m == nil {
			continue
		}
		m.order = orders[m.kind]
		orders[m.kind]++
		d.byTrk[m.track] = m
		switch m.kind {
		case KindVideo:
			if d.video == nil && m.out != nil {
				d.video = m
				d.info.video = m.track
			} else if d.info.video == nil {
				d.info.video = m.track
			}
		case KindAudio:
			d.audios = append(d.audios, m)
			d.info.audio = append(d.info.audio, m.track)
		case KindSubtitle:
			d.subs = append(d.subs, m)
			d.info.subs = append(d.info.subs, m.track)
		}
	}
	if d.video != nil {
		d.info.video = d.video.track
		// The movie lasts as long as its longest track, which may be a
		// subtitle track: the stream lasts as long as its video.
		if v := d.video; v.n() > 0 {
			last := v.n() - 1
			end := time.Duration(0)
			for i := max(0, last-32); i <= last; i++ {
				end = max(end, v.ptsOf(i)+v.durOf(i))
			}
			if end > 0 {
				d.info.duration = end
			}
		}
		if d.video.out != nil && d.video.out.frameRate == 0 && d.video.n() > 1 {
			span := d.video.dts[d.video.n()-1] - d.video.dts[0]
			if span > 0 {
				d.video.out.frameRate = float64(d.video.n()-1) * float64(d.video.timescale) / float64(span)
			}
		}
	}
	return d, nil
}

func parseTrak(b []byte, movieScale uint32) (*mp4Track, error) {
	m := &mp4Track{track: &track{isDefault: true}}
	if tkhd := mp4Child(b, "tkhd"); len(tkhd) >= 24 {
		flags := binary.BigEndian.Uint32(tkhd) & 0xffffff
		m.isDefault = flags&1 != 0
		if tkhd[0] == 1 {
			m.id = int64(binary.BigEndian.Uint32(tkhd[20:]))
		} else {
			m.id = int64(binary.BigEndian.Uint32(tkhd[12:]))
		}
	}
	mdia := mp4Child(b, "mdia")
	mdhd := mp4Child(mdia, "mdhd")
	if len(mdhd) < 24 {
		return nil, errors.New("no mdhd")
	}
	lang := uint16(0)
	if mdhd[0] == 1 && len(mdhd) >= 36 {
		m.timescale = binary.BigEndian.Uint32(mdhd[20:])
		lang = binary.BigEndian.Uint16(mdhd[32:])
	} else {
		m.timescale = binary.BigEndian.Uint32(mdhd[12:])
		lang = binary.BigEndian.Uint16(mdhd[20:])
	}
	if m.timescale == 0 {
		return nil, errors.New("zero timescale")
	}
	if lang != 0 && lang != 0x7fff && lang>>15 == 0 {
		m.language = string([]byte{byte(lang>>10&31) + 0x60, byte(lang>>5&31) + 0x60, byte(lang&31) + 0x60})
		if m.language == "und" {
			m.language = ""
		}
	}
	hdlr := mp4Child(mdia, "hdlr")
	if len(hdlr) < 12 {
		return nil, errors.New("no hdlr")
	}
	switch string(hdlr[8:12]) {
	case "vide":
		m.kind = KindVideo
	case "soun":
		m.kind = KindAudio
	case "sbtl", "text", "subt":
		m.kind = KindSubtitle
	default:
		return nil, nil
	}
	stbl := mp4Child(mp4Child(mdia, "minf"), "stbl")
	if err := m.parseSampleTable(stbl); err != nil {
		return nil, err
	}
	m.parseEdits(mp4Child(b, "edts"), movieScale)
	m.parseEntry(mp4Child(stbl, "stsd"))
	return m, nil
}

func (m *mp4Track) parseEdits(edts []byte, movieScale uint32) {
	elst := mp4Child(edts, "elst")
	if len(elst) < 8 {
		return
	}
	v := elst[0]
	n := int(binary.BigEndian.Uint32(elst[4:]))
	p := elst[8:]
	var empty int64
	for i := 0; i < n; i++ {
		var segDur, mediaTime int64
		if v == 1 {
			if len(p) < 20 {
				return
			}
			segDur = int64(binary.BigEndian.Uint64(p))
			mediaTime = int64(binary.BigEndian.Uint64(p[8:]))
			p = p[20:]
		} else {
			if len(p) < 12 {
				return
			}
			segDur = int64(binary.BigEndian.Uint32(p))
			mediaTime = int64(int32(binary.BigEndian.Uint32(p[4:])))
			p = p[12:]
		}
		if mediaTime == -1 {
			empty += segDur
			continue
		}
		delay := toTimescale(fromTimescale(empty, movieScale), m.timescale)
		m.shift = mediaTime - delay
		return
	}
}

func (m *mp4Track) parseSampleTable(stbl []byte) error {
	// Sample sizes.
	if stsz := mp4Child(stbl, "stsz"); len(stsz) >= 12 {
		fixed := binary.BigEndian.Uint32(stsz[4:])
		n := int(binary.BigEndian.Uint32(stsz[8:]))
		if n > 50_000_000 {
			return errors.New("too many samples")
		}
		m.sizes = make([]uint32, n)
		if fixed != 0 {
			for i := range m.sizes {
				m.sizes[i] = fixed
			}
		} else {
			if len(stsz) < 12+4*n {
				return errors.New("short stsz")
			}
			for i := range m.sizes {
				m.sizes[i] = binary.BigEndian.Uint32(stsz[12+4*i:])
			}
		}
	} else if stz2 := mp4Child(stbl, "stz2"); len(stz2) >= 12 {
		field := int(stz2[7])
		n := int(binary.BigEndian.Uint32(stz2[8:]))
		m.sizes = make([]uint32, n)
		p := stz2[12:]
		for i := range m.sizes {
			switch field {
			case 4:
				if i/2 >= len(p) {
					return errors.New("short stz2")
				}
				v := p[i/2]
				if i%2 == 0 {
					v >>= 4
				}
				m.sizes[i] = uint32(v & 15)
			case 8:
				m.sizes[i] = uint32(p[i])
			case 16:
				m.sizes[i] = uint32(binary.BigEndian.Uint16(p[2*i:]))
			}
		}
	} else {
		return errors.New("no sample sizes")
	}
	n := len(m.sizes)
	// Decode times.
	m.dts = make([]int64, n)
	stts := mp4Child(stbl, "stts")
	if len(stts) < 8 {
		return errors.New("no stts")
	}
	i, t := 0, int64(0)
	for e, ne := 0, int(binary.BigEndian.Uint32(stts[4:])); e < ne && 8+8*e+8 <= len(stts); e++ {
		cnt := int(binary.BigEndian.Uint32(stts[8+8*e:]))
		delta := int64(binary.BigEndian.Uint32(stts[12+8*e:]))
		for k := 0; k < cnt && i < n; k++ {
			m.dts[i] = t
			t += delta
			i++
		}
	}
	for ; i < n; i++ {
		m.dts[i] = t
	}
	// Composition offsets.
	if ctts := mp4Child(stbl, "ctts"); len(ctts) >= 8 {
		m.cto = make([]int32, n)
		i := 0
		for e, ne := 0, int(binary.BigEndian.Uint32(ctts[4:])); e < ne && 8+8*e+8 <= len(ctts); e++ {
			cnt := int(binary.BigEndian.Uint32(ctts[8+8*e:]))
			off := int32(binary.BigEndian.Uint32(ctts[12+8*e:]))
			for k := 0; k < cnt && i < n; k++ {
				m.cto[i] = off
				i++
			}
		}
	}
	// Sync samples.
	if stss := mp4Child(stbl, "stss"); len(stss) >= 8 {
		cnt := int(binary.BigEndian.Uint32(stss[4:]))
		m.sync = make([]uint32, 0, cnt)
		for e := 0; e < cnt && 8+4*e+4 <= len(stss); e++ {
			m.sync = append(m.sync, binary.BigEndian.Uint32(stss[8+4*e:])-1)
		}
	}
	// Chunk offsets and sample-to-chunk.
	var chunks []int64
	if stco := mp4Child(stbl, "stco"); len(stco) >= 8 {
		cnt := int(binary.BigEndian.Uint32(stco[4:]))
		for e := 0; e < cnt && 8+4*e+4 <= len(stco); e++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint32(stco[8+4*e:])))
		}
	} else if co64 := mp4Child(stbl, "co64"); len(co64) >= 8 {
		cnt := int(binary.BigEndian.Uint32(co64[4:]))
		for e := 0; e < cnt && 8+8*e+8 <= len(co64); e++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint64(co64[8+8*e:])))
		}
	}
	stsc := mp4Child(stbl, "stsc")
	if len(stsc) < 8 || len(chunks) == 0 {
		return errors.New("no chunk table")
	}
	type run struct{ first, perChunk int }
	var runs []run
	for e, ne := 0, int(binary.BigEndian.Uint32(stsc[4:])); e < ne && 8+12*e+12 <= len(stsc); e++ {
		runs = append(runs, run{int(binary.BigEndian.Uint32(stsc[8+12*e:])) - 1, int(binary.BigEndian.Uint32(stsc[12+12*e:]))})
	}
	m.offsets = make([]int64, n)
	s := 0
	for r := range runs {
		last := len(chunks)
		if r+1 < len(runs) {
			last = runs[r+1].first
		}
		for c := runs[r].first; c < last && c < len(chunks); c++ {
			off := chunks[c]
			for k := 0; k < runs[r].perChunk && s < n; k++ {
				m.offsets[s] = off
				off += int64(m.sizes[s])
				s++
			}
		}
	}
	if s < n {
		m.sizes = m.sizes[:s]
		m.dts = m.dts[:s]
		m.offsets = m.offsets[:s]
		if m.cto != nil {
			m.cto = m.cto[:s]
		}
	}
	return nil
}

// parseEntry reads the sample description into the track.
func (m *mp4Track) parseEntry(stsd []byte) {
	if len(stsd) < 8 {
		m.why = "no sample description"
		return
	}
	entries := mp4Boxes(stsd[8:])
	if len(entries) == 0 {
		m.why = "no sample description"
		return
	}
	e := entries[0]
	m.fourcc = e.typ
	switch m.kind {
	case KindVideo:
		if len(e.data) < 78 {
			m.why = "short visual sample entry"
			return
		}
		w := int(binary.BigEndian.Uint16(e.data[24:]))
		h := int(binary.BigEndian.Uint16(e.data[26:]))
		children := e.data[78:]
		switch e.typ {
		case "avc1", "avc3":
			m.codec = "h264"
			setupVideo(m.track, mp4Child(children, "avcC"), w, h)
		case "hvc1", "hev1", "dvh1", "dvhe":
			m.codec = "hevc"
			setupVideo(m.track, mp4Child(children, "hvcC"), w, h)
			if m.out != nil && (e.typ == "dvh1" || e.typ == "dvhe") {
				// Profile 5 stays dvh1; profile 8 and the like play as
				// their HEVC base layer.
				for _, box := range []string{"dvcC", "dvvC"} {
					if cfg := mp4Child(children, box); len(cfg) >= 4 {
						applyDolbyVision(m.track, box, cfg)
						break
					}
				}
			}
		default:
			m.codec = mp4CodecName(e.typ)
			m.why = "video codec " + m.codec + " is not supported"
			return
		}
		if m.out != nil {
			if colr := mp4Child(children, "colr"); len(colr) >= 11 && string(colr[:4]) == "nclx" {
				m.out.colour = &colourInfo{
					primaries: uint8(binary.BigEndian.Uint16(colr[4:])),
					transfer:  uint8(binary.BigEndian.Uint16(colr[6:])),
					matrix:    uint8(binary.BigEndian.Uint16(colr[8:])),
					fullRange: colr[10]&0x80 != 0,
				}
			}
			m.out.timescale = m.timescale
		}
	case KindAudio:
		if len(e.data) < 28 {
			m.why = "short audio sample entry"
			return
		}
		ver := binary.BigEndian.Uint16(e.data[8:])
		channels := int(binary.BigEndian.Uint16(e.data[16:]))
		rate := int(binary.BigEndian.Uint32(e.data[24:]) >> 16)
		children := e.data[28:]
		switch ver {
		case 1:
			if len(e.data) >= 44 {
				children = e.data[44:]
			}
		case 2:
			if len(e.data) >= 64 {
				children = e.data[64:]
			}
		}
		m.codec = mp4CodecName(e.typ)
		switch e.typ {
		case "mp4a":
			esds := mp4Child(children, "esds")
			if esds == nil {
				esds = mp4Child(mp4Child(children, "wave"), "esds")
			}
			oti, asc := parseESDS(esds)
			switch oti {
			case 0x40, 0x66, 0x67, 0x68:
				m.codec = "aac"
				setupAudio(m.track, asc, rate, channels)
			case 0x69, 0x6b:
				m.codec = "mp3"
				m.why = "MP3 audio cannot be remuxed for AVPlayer"
				return
			case 0xa5:
				m.codec = "ac3"
			case 0xa6:
				m.codec = "eac3"
			default:
				m.why = fmt.Sprintf("audio object type 0x%x is not supported", oti)
				return
			}
		case "ac-3":
			m.codec = "ac3"
			m.out = &outTrack{fourcc: "ac-3", config: mp4Child(children, "dac3"), sampleRate: rate, channels: channels, frameSamples: 1536, codecs: "ac-3"}
			if len(m.out.config) < 3 {
				m.out, m.why = nil, "AC-3 without dac3"
				return
			}
		case "ec-3":
			m.codec = "eac3"
			m.out = &outTrack{fourcc: "ec-3", config: mp4Child(children, "dec3"), sampleRate: rate, channels: channels, frameSamples: 1536, codecs: "ec-3"}
			if len(m.out.config) < 2 {
				m.out, m.why = nil, "E-AC-3 without dec3"
				return
			}
		default:
			setupAudio(m.track, nil, rate, channels)
			return
		}
		if m.out != nil && !m.needParams {
			if m.out.sampleRate == 0 {
				m.out.sampleRate = int(m.timescale)
			}
			m.out.timescale = m.timescale
			m.out.frameSamples = m.out.frameSamples * int64(m.timescale) / int64(max(m.out.sampleRate, 1))
			if m.n() > 0 {
				m.out.anchor = m.ptsOf(0)
			}
		}
	case KindSubtitle:
		m.codec = mp4CodecName(e.typ)
		if m.codec != "mov_text" {
			m.why = "subtitle format " + m.codec + " is not extracted"
		}
	}
}

func mp4CodecName(fourcc string) string {
	switch fourcc {
	case "avc1", "avc3":
		return "h264"
	case "hvc1", "hev1", "dvh1", "dvhe":
		return "hevc"
	case "mp4a":
		return "aac"
	case "ac-3":
		return "ac3"
	case "ec-3":
		return "eac3"
	case "tx3g", "text":
		return "mov_text"
	case "wvtt":
		return "webvtt"
	case "c608":
		return "eia_608"
	case "av01":
		return "av1"
	case "vp09":
		return "vp9"
	case "mp4v":
		return "mpeg4"
	case "jpeg", "mjpa", "mjpb":
		return "mjpeg"
	case "fLaC":
		return "flac"
	case "Opus":
		return "opus"
	case "alac":
		return "alac"
	case ".mp3":
		return "mp3"
	}
	return fourcc
}

// parseESDS returns the object type and DecoderSpecificInfo of an esds box.
func parseESDS(b []byte) (oti byte, asc []byte) {
	if len(b) < 4 {
		return 0, nil
	}
	b = b[4:]
	readDesc := func(b []byte) (tag byte, body, rest []byte, ok bool) {
		if len(b) < 2 {
			return 0, nil, nil, false
		}
		tag = b[0]
		n, i := 0, 1
		for ; i < 5 && i < len(b); i++ {
			n = n<<7 | int(b[i]&0x7f)
			if b[i]&0x80 == 0 {
				i++
				break
			}
		}
		if i+n > len(b) {
			n = len(b) - i
		}
		return tag, b[i : i+n], b[i+n:], true
	}
	tag, es, _, ok := readDesc(b)
	if !ok || tag != 0x03 || len(es) < 3 {
		return 0, nil
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 && len(es) >= 2 {
		es = es[2:]
	}
	if flags&0x40 != 0 && len(es) >= 1 {
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 && len(es) >= 2 {
		es = es[2:]
	}
	tag, dcd, _, ok := readDesc(es)
	if !ok || tag != 0x04 || len(dcd) < 13 {
		return 0, nil
	}
	oti = dcd[0]
	if t, dsi, _, ok := readDesc(dcd[13:]); ok && t == 0x05 {
		asc = dsi
	}
	return oti, asc
}

func (d *mp4Demuxer) keyframes() []keyframe {
	v := d.video
	if v == nil {
		return nil
	}
	if v.sync == nil {
		// Every sample is a sync sample (intra-only): one per second.
		var kfs []keyframe
		next := time.Duration(0)
		for i := 0; i < v.n(); i++ {
			if t := v.ptsOf(i); t >= next {
				kfs = append(kfs, keyframe{t: t, pos: int64(i)})
				next = t + time.Second
			}
		}
		return kfs
	}
	kfs := make([]keyframe, 0, len(v.sync))
	for _, s := range v.sync {
		if int(s) < v.n() {
			kfs = append(kfs, keyframe{t: v.ptsOf(int(s)), pos: int64(s)})
		}
	}
	return kfs
}

func (d *mp4Demuxer) selectTracks(audio *track, subs []*track) {
	d.audio = d.byTrk[audio]
	d.textSub = nil
	for _, s := range subs {
		if m := d.byTrk[s]; m != nil {
			d.textSub = append(d.textSub, m)
		}
	}
}

func (d *mp4Demuxer) probeStarts(_ context.Context, selected *track) (time.Duration, time.Duration, error) {
	var audioStart time.Duration
	var starts []time.Duration
	if d.video != nil && d.video.n() > 0 {
		starts = append(starts, d.video.ptsOf(0))
	}
	for _, audio := range d.audios {
		if audio != nil && audio.n() > 0 {
			starts = append(starts, audio.ptsOf(0))
		}
	}
	audioFound := false
	if audio := d.byTrk[selected]; audio != nil && audio.n() > 0 {
		audioStart, audioFound = audio.ptsOf(0), true
	}
	if !audioFound || len(starts) == 0 {
		return 0, 0, errors.New("no media timestamps")
	}
	mediaStart := starts[0]
	for _, start := range starts[1:] {
		if start < mediaStart {
			mediaStart = start
		}
	}
	return mediaStart, audioStart, nil
}

// prepare reads the first sync sample of a video track whose parameter
// sets are in-band (hev1, avc3).
func (d *mp4Demuxer) prepare(ctx context.Context) error {
	for _, m := range []*mp4Track{d.video, d.audio} {
		if m == nil || !m.needParams || m.out == nil || m.n() == 0 {
			continue
		}
		i := 0
		if m.sync != nil && len(m.sync) > 0 {
			i = int(m.sync[0])
		}
		buf := make([]byte, m.sizes[i])
		if _, err := d.src.readAt(ctx, buf, m.offsets[i]); err != nil && err != io.EOF {
			return err
		}
		if err := m.complete(buf); err != nil {
			m.why, m.out = err.Error(), nil
		}
		if m.out != nil && m.kind == KindVideo {
			m.out.timescale = m.timescale
		}
	}
	return nil
}

// sampleRef is one sample to read for a segment.
type sampleRef struct {
	m   *mp4Track
	i   int
	off int64
	n   uint32
}

func (d *mp4Demuxer) read(ctx context.Context, seg *segment, f *segFilter) error {
	var refs []sampleRef
	if v := d.video; v != nil && v.out != nil {
		i0, i1 := 0, v.n()
		if !seg.first && seg.pos >= 0 {
			i0 = int(seg.pos)
		}
		if !seg.last && seg.endPos >= 0 {
			i1 = int(seg.endPos)
		}
		for i := i0; i < i1; i++ {
			refs = append(refs, sampleRef{v, i, v.offsets[i], v.sizes[i]})
		}
	}
	inRange := func(t time.Duration) bool {
		relative := t - seg.offset
		return (seg.first || relative >= seg.start) && (seg.last || relative < seg.end)
	}
	if a := d.audio; a != nil && a.out != nil {
		// Audio decode times are presentation times.
		lo := sort.Search(a.n(), func(i int) bool { return seg.first || a.ptsOf(i)-seg.offset >= seg.start })
		for i := lo; i < a.n(); i++ {
			t := a.ptsOf(i)
			if !inRange(t) {
				break
			}
			refs = append(refs, sampleRef{a, i, a.offsets[i], a.sizes[i]})
		}
	}
	for _, s := range d.textSub {
		lo := sort.Search(s.n(), func(i int) bool { return s.ptsOf(i)-seg.offset >= seg.start-30*time.Second })
		for i := lo; i < s.n(); i++ {
			t := s.ptsOf(i)
			if !seg.last && t >= seg.end {
				break
			}
			if t-seg.offset+s.durOf(i) > seg.start || seg.first {
				refs = append(refs, sampleRef{s, i, s.offsets[i], s.sizes[i]})
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	// Read in file order, merging nearby samples into one request.
	order := make([]int, len(refs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return refs[order[a]].off < refs[order[b]].off })
	const gap = 512 << 10
	type span struct{ from, to int } // indices into order
	var spans []span
	for k := 0; k < len(order); {
		j := k + 1
		end := refs[order[k]].off + int64(refs[order[k]].n)
		for j < len(order) && refs[order[j]].off <= end+gap {
			end = max(end, refs[order[j]].off+int64(refs[order[j]].n))
			j++
		}
		spans = append(spans, span{k, j})
		k = j
	}
	// Samples go to the filter in decode order per track; the file order
	// of one track's samples is its decode order.
	for _, sp := range spans {
		first := refs[order[sp.from]]
		last := refs[order[sp.to-1]]
		start := first.off
		end := last.off + int64(last.n)
		for _, k := range order[sp.from:sp.to] {
			end = max(end, refs[k].off+int64(refs[k].n))
		}
		r := d.src.stream(ctx, start, end-start)
		pos := start
		var err error
		for _, k := range order[sp.from:sp.to] {
			ref := refs[k]
			if ref.off < pos {
				// Overlapping samples (broken table): read it alone.
				buf := make([]byte, ref.n)
				if _, err = d.src.readAt(ctx, buf, ref.off); err != nil && err != io.EOF {
					break
				}
				err = d.emit(f, ref, buf)
				if err != nil {
					break
				}
				continue
			}
			if ref.off > pos {
				if _, err = io.CopyN(io.Discard, r, ref.off-pos); err != nil {
					break
				}
				pos = ref.off
			}
			buf := make([]byte, ref.n)
			if _, err = io.ReadFull(r, buf); err != nil {
				break
			}
			pos += int64(ref.n)
			if err = d.emit(f, ref, buf); err != nil {
				break
			}
		}
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *mp4Demuxer) emit(f *segFilter, ref sampleRef, data []byte) error {
	m := ref.m
	p := &packet{t: m.track, pts: m.ptsOf(ref.i), data: data, key: m.isSync(ref.i), dur: m.durOf(ref.i)}
	if m.kind == KindSubtitle {
		p.data = movTextPayload(data)
	}
	_, err := f.push(p)
	return err
}

func (d *mp4Demuxer) subtitlesBefore(ctx context.Context, t time.Duration, emit func(*packet)) error {
	return nil // segment reads include cues that overlap their start
}

// movTextPayload returns the text of a tx3g sample.
func movTextPayload(b []byte) []byte {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b))
	if n > len(b)-2 {
		n = len(b) - 2
	}
	text := b[2 : 2+n]
	if bytes.HasPrefix(text, []byte{0xfe, 0xff}) {
		return utf16BE(text[2:])
	}
	return text
}

func utf16BE(b []byte) []byte {
	var out []rune
	for i := 0; i+1 < len(b); i += 2 {
		out = append(out, rune(binary.BigEndian.Uint16(b[i:])))
	}
	return []byte(string(out))
}
