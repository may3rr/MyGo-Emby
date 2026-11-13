package remux

import (
	"encoding/binary"
	"time"
)

// boxWriter builds ISO BMFF boxes in a byte slice.
type boxWriter struct {
	b     []byte
	stack []int
}

func (w *boxWriter) start(typ string) {
	w.stack = append(w.stack, len(w.b))
	w.b = append(w.b, 0, 0, 0, 0)
	w.b = append(w.b, typ...)
}

func (w *boxWriter) startFull(typ string, version byte, flags uint32) {
	w.start(typ)
	w.u32(uint32(version)<<24 | flags&0xffffff)
}

func (w *boxWriter) end() {
	n := len(w.stack) - 1
	start := w.stack[n]
	w.stack = w.stack[:n]
	binary.BigEndian.PutUint32(w.b[start:], uint32(len(w.b)-start))
}

func (w *boxWriter) u8(v uint8)   { w.b = append(w.b, v) }
func (w *boxWriter) u16(v uint16) { w.b = binary.BigEndian.AppendUint16(w.b, v) }
func (w *boxWriter) u32(v uint32) { w.b = binary.BigEndian.AppendUint32(w.b, v) }
func (w *boxWriter) u64(v uint64) { w.b = binary.BigEndian.AppendUint64(w.b, v) }
func (w *boxWriter) raw(p []byte) { w.b = append(w.b, p...) }
func (w *boxWriter) str(s string) { w.b = append(w.b, s...) }
func (w *boxWriter) zeros(n int)  { w.b = append(w.b, make([]byte, n)...) }
func (w *boxWriter) leaf(typ string, payload []byte) {
	w.start(typ)
	w.raw(payload)
	w.end()
}

var unityMatrix = [9]uint32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000}

func (w *boxWriter) matrix() {
	for _, v := range unityMatrix {
		w.u32(v)
	}
}

// outTrack describes one track of the fMP4 output.
type outTrack struct {
	id        uint32
	video     bool
	timescale uint32
	fourcc    string // avc1, hvc1, dvh1, mp4a, ac-3, ec-3
	config    []byte // avcC, hvcC, ASC, dac3 or dec3 payload
	codecs    string // RFC 6381 codec string for the playlist

	width, height  int
	sarNum, sarDen int
	colour         *colourInfo
	dvBox          string // "dvcC" or "dvvC" with dvConfig, for Dolby Vision
	dvConfig       []byte
	frameRate      float64

	sampleRate, channels int
	frameSamples         int64         // audio samples per access unit
	anchor               time.Duration // a time on the audio frame grid
	language             string
}

// writeInit returns the initialization segment (ftyp + moov) for tracks.
func writeInit(tracks []*outTrack, duration time.Duration) []byte {
	w := &boxWriter{b: make([]byte, 0, 1024)}
	w.start("ftyp")
	w.str("iso5")
	w.u32(1)
	w.str("iso5iso6mp41")
	w.end()

	w.start("moov")
	w.startFull("mvhd", 0, 0)
	w.u32(0)
	w.u32(0)
	w.u32(1000)
	w.u32(0)
	w.u32(0x00010000)
	w.u16(0x0100)
	w.zeros(10)
	w.matrix()
	w.zeros(24)
	w.u32(uint32(len(tracks) + 1))
	w.end()

	for _, t := range tracks {
		writeTrak(w, t)
	}

	w.start("mvex")
	w.startFull("mehd", 1, 0)
	w.u64(uint64(duration / time.Millisecond))
	w.end()
	for _, t := range tracks {
		w.startFull("trex", 0, 0)
		w.u32(t.id)
		w.u32(1)
		w.u32(0)
		w.u32(0)
		if t.video {
			w.u32(sampleFlagsNonSync)
		} else {
			w.u32(sampleFlagsSync)
		}
		w.end()
	}
	w.end()
	w.end()
	return w.b
}

const (
	sampleFlagsSync    = 0x02000000 // depends on no other sample
	sampleFlagsNonSync = 0x01010000 // depends on others, not a sync sample
)

func writeTrak(w *boxWriter, t *outTrack) {
	w.start("trak")
	w.startFull("tkhd", 0, 3)
	w.u32(0)
	w.u32(0)
	w.u32(t.id)
	w.u32(0)
	w.u32(0)
	w.zeros(8)
	w.u16(0)
	w.u16(0)
	if t.video {
		w.u16(0)
	} else {
		w.u16(0x0100)
	}
	w.u16(0)
	w.matrix()
	if t.video {
		dw := t.width
		if t.sarNum > 0 && t.sarDen > 0 && t.sarNum != t.sarDen {
			dw = t.width * t.sarNum / t.sarDen
		}
		w.u32(uint32(dw) << 16)
		w.u32(uint32(t.height) << 16)
	} else {
		w.u32(0)
		w.u32(0)
	}
	w.end()

	w.start("mdia")
	w.startFull("mdhd", 0, 0)
	w.u32(0)
	w.u32(0)
	w.u32(t.timescale)
	w.u32(0)
	w.u16(packLanguage(t.language))
	w.u16(0)
	w.end()
	w.startFull("hdlr", 0, 0)
	w.u32(0)
	if t.video {
		w.str("vide")
	} else {
		w.str("soun")
	}
	w.zeros(12)
	if t.video {
		w.str("VideoHandler\x00")
	} else {
		w.str("SoundHandler\x00")
	}
	w.end()

	w.start("minf")
	if t.video {
		w.startFull("vmhd", 0, 1)
		w.zeros(8)
		w.end()
	} else {
		w.startFull("smhd", 0, 0)
		w.zeros(4)
		w.end()
	}
	w.start("dinf")
	w.startFull("dref", 0, 0)
	w.u32(1)
	w.startFull("url ", 0, 1)
	w.end()
	w.end()
	w.end()

	w.start("stbl")
	w.startFull("stsd", 0, 0)
	w.u32(1)
	if t.video {
		writeVisualEntry(w, t)
	} else {
		writeAudioEntry(w, t)
	}
	w.end()
	for _, typ := range []string{"stts", "stsc", "stco"} {
		w.startFull(typ, 0, 0)
		w.u32(0)
		w.end()
	}
	w.startFull("stsz", 0, 0)
	w.u32(0)
	w.u32(0)
	w.end()
	w.end() // stbl
	w.end() // minf
	w.end() // mdia
	w.end() // trak
}

func writeVisualEntry(w *boxWriter, t *outTrack) {
	w.start(t.fourcc)
	w.zeros(6)
	w.u16(1)
	w.zeros(16)
	w.u16(uint16(t.width))
	w.u16(uint16(t.height))
	w.u32(0x00480000)
	w.u32(0x00480000)
	w.u32(0)
	w.u16(1)
	w.zeros(32)
	w.u16(0x0018)
	w.u16(0xffff)
	switch t.fourcc {
	case "avc1":
		w.leaf("avcC", t.config)
	default:
		w.leaf("hvcC", t.config)
	}
	if t.dvBox != "" && len(t.dvConfig) > 0 {
		w.leaf(t.dvBox, t.dvConfig)
	}
	if t.colour != nil {
		w.start("colr")
		w.str("nclx")
		w.u16(uint16(t.colour.primaries))
		w.u16(uint16(t.colour.transfer))
		w.u16(uint16(t.colour.matrix))
		if t.colour.fullRange {
			w.u8(0x80)
		} else {
			w.u8(0)
		}
		w.end()
	}
	if t.sarNum > 0 && t.sarDen > 0 && t.sarNum != t.sarDen {
		w.start("pasp")
		w.u32(uint32(t.sarNum))
		w.u32(uint32(t.sarDen))
		w.end()
	}
	w.end()
}

func writeAudioEntry(w *boxWriter, t *outTrack) {
	w.start(t.fourcc)
	w.zeros(6)
	w.u16(1)
	w.zeros(8)
	ch := t.channels
	if t.fourcc != "mp4a" || ch <= 0 || ch > 8 {
		ch = 2
	}
	w.u16(uint16(ch))
	w.u16(16)
	w.u32(0)
	rate := t.sampleRate
	if rate > 0xffff {
		rate = 0
	}
	w.u32(uint32(rate) << 16)
	switch t.fourcc {
	case "mp4a":
		writeESDS(w, t.config)
	case "ac-3":
		w.leaf("dac3", t.config)
	case "ec-3":
		w.leaf("dec3", t.config)
	}
	w.end()
}

func writeESDS(w *boxWriter, asc []byte) {
	w.startFull("esds", 0, 0)
	dsi := descriptor(0x05, asc)
	dcd := descriptor(0x04, append([]byte{
		0x40,             // objectTypeIndication: MPEG-4 audio
		0x15,             // streamType audio, upStream 0, reserved 1
		0x00, 0x00, 0x00, // bufferSizeDB
		0, 0, 0, 0, // maxBitrate
		0, 0, 0, 0, // avgBitrate
	}, dsi...))
	sl := descriptor(0x06, []byte{0x02})
	es := descriptor(0x03, append(append([]byte{0, 0, 0}, dcd...), sl...))
	w.raw(es)
	w.end()
}

func descriptor(tag byte, payload []byte) []byte {
	n := len(payload)
	b := []byte{tag, 0x80 | byte(n>>21)&0x7f, 0x80 | byte(n>>14)&0x7f, 0x80 | byte(n>>7)&0x7f, byte(n) & 0x7f}
	return append(b, payload...)
}

// packLanguage packs an ISO 639-2/T code as in mdhd; unknown codes are "und".
func packLanguage(lang string) uint16 {
	if len(lang) != 3 {
		lang = "und"
	}
	var v uint16
	for i := 0; i < 3; i++ {
		c := lang[i]
		if c < 'a' || c > 'z' {
			return packLanguage("und")
		}
		v = v<<5 | uint16(c-0x60)
	}
	return v
}

// fragSample is one sample of a fragment.
type fragSample struct {
	dur   uint32
	cto   int32
	flags uint32
	data  []byte
}

// fragTrack is the samples of one track in a fragment.
type fragTrack struct {
	t       *outTrack
	baseDTS uint64
	samples []fragSample
}

// writeFragment returns one moof+mdat with the given track runs.
func writeFragment(seq uint32, runs []fragTrack) []byte {
	size := 0
	for _, r := range runs {
		for _, s := range r.samples {
			size += len(s.data)
		}
	}
	w := &boxWriter{b: make([]byte, 0, 256+size)}
	w.start("moof")
	w.startFull("mfhd", 0, 0)
	w.u32(seq)
	w.end()
	var offsets []int
	for _, r := range runs {
		w.start("traf")
		w.startFull("tfhd", 0, 0x020000) // default-base-is-moof
		w.u32(r.t.id)
		w.end()
		w.startFull("tfdt", 1, 0)
		w.u64(r.baseDTS)
		w.end()
		if r.t.video {
			w.startFull("trun", 1, 0x000f01)
		} else {
			w.startFull("trun", 0, 0x000301)
		}
		w.u32(uint32(len(r.samples)))
		offsets = append(offsets, len(w.b))
		w.u32(0) // data_offset, patched below
		for _, s := range r.samples {
			w.u32(s.dur)
			w.u32(uint32(len(s.data)))
			if r.t.video {
				w.u32(s.flags)
				w.u32(uint32(s.cto))
			}
		}
		w.end()
		w.end()
	}
	w.end() // moof
	moofSize := len(w.b)
	data := moofSize + 8
	for i, r := range runs {
		binary.BigEndian.PutUint32(w.b[offsets[i]:], uint32(data))
		for _, s := range r.samples {
			data += len(s.data)
		}
	}
	w.u32(uint32(8 + size))
	w.str("mdat")
	for _, r := range runs {
		for _, s := range r.samples {
			w.raw(s.data)
		}
	}
	return w.b
}
