package remux

import (
	"errors"
	"strconv"
)

var aacSampleRates = [...]int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// aacConfig is the part of an AudioSpecificConfig the muxer needs.
type aacConfig struct {
	objectType int
	sampleRate int // of the core decoder
	channels   int
}

func parseASC(b []byte) (*aacConfig, error) {
	r := &bitReader{b: b}
	c := &aacConfig{}
	c.objectType = int(r.u(5))
	if c.objectType == 31 {
		c.objectType = 32 + int(r.u(6))
	}
	idx := int(r.u(4))
	if idx == 15 {
		c.sampleRate = int(r.u(24))
	} else if idx < len(aacSampleRates) {
		c.sampleRate = aacSampleRates[idx]
	}
	c.channels = int(r.u(4))
	if c.channels == 7 {
		c.channels = 8
	}
	if r.err != nil || c.sampleRate == 0 {
		return nil, errors.New("remux: bad AudioSpecificConfig")
	}
	return c, nil
}

// codecString returns "mp4a.40.<object type>".
func (c *aacConfig) codecString() string { return "mp4a.40." + strconv.Itoa(c.objectType) }

// makeASC writes a two-byte AudioSpecificConfig.
func makeASC(objectType, sampleRate, channels int) []byte {
	idx := 4
	for i, r := range aacSampleRates {
		if r == sampleRate {
			idx = i
			break
		}
	}
	if channels == 8 {
		channels = 7
	}
	v := objectType<<11 | idx<<7 | channels<<3
	return []byte{byte(v >> 8), byte(v)}
}

// adtsHeader is a parsed ADTS frame header.
type adtsHeader struct {
	objectType, sampleRate, channels int
	headerLen, frameLen              int
}

func parseADTS(b []byte) (*adtsHeader, error) {
	if len(b) < 7 || b[0] != 0xff || b[1]&0xf6 != 0xf0 {
		return nil, errors.New("remux: no ADTS sync")
	}
	h := &adtsHeader{headerLen: 7}
	if b[1]&1 == 0 {
		h.headerLen = 9
	}
	h.objectType = int(b[2]>>6) + 1
	idx := int(b[2]>>2) & 0xf
	if idx >= len(aacSampleRates) {
		return nil, errors.New("remux: bad ADTS sample rate")
	}
	h.sampleRate = aacSampleRates[idx]
	h.channels = int(b[2]&1)<<2 | int(b[3]>>6)
	h.frameLen = int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
	if h.frameLen < h.headerLen {
		return nil, errors.New("remux: bad ADTS frame length")
	}
	return h, nil
}

// ac3Header is a parsed AC-3 or E-AC-3 syncframe header.
type ac3Header struct {
	eac3        bool
	strmtyp     int // E-AC-3: 0 independent, 1 dependent, 2 AC-3 converted
	substreamID int
	frameSize   int // bytes
	sampleRate  int
	fscod       int
	bsid        int
	bsmod       int
	acmod       int
	lfeon       int
	bitRateCode int // AC-3
	blocks      int // audio blocks of 256 samples
	chanmap     int // E-AC-3 dependent substream channel map, -1 if absent
}

var ac3Rates = [...]int{48000, 44100, 32000}

// ac3FrameWords is the AC-3 frame size in 16-bit words by frmsizecod/2 and
// fscod (48, 44.1, 32 kHz); 44.1 kHz frames add a word when frmsizecod is odd.
var ac3FrameWords = [19][3]int{
	{64, 69, 96}, {80, 87, 120}, {96, 104, 144}, {112, 121, 168}, {128, 139, 192},
	{160, 174, 240}, {192, 208, 288}, {224, 243, 336}, {256, 278, 384}, {320, 348, 480},
	{384, 417, 576}, {448, 487, 672}, {512, 557, 768}, {640, 696, 960}, {768, 835, 1152},
	{896, 975, 1344}, {1024, 1114, 1536}, {1152, 1253, 1728}, {1280, 1393, 1920},
}

var ac3Bitrates = [...]int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 448, 512, 576, 640}

func ac3Channels(acmod, lfeon int) int {
	return [...]int{2, 1, 2, 3, 3, 4, 4, 5}[acmod&7] + lfeon
}

func parseAC3Header(b []byte) (*ac3Header, error) {
	if len(b) < 8 || b[0] != 0x0b || b[1] != 0x77 {
		return nil, errors.New("remux: no AC-3 sync")
	}
	bsid := int(b[5] >> 3)
	if bsid > 10 {
		return parseEAC3Header(b)
	}
	r := &bitReader{b: b[4:]}
	h := &ac3Header{chanmap: -1, blocks: 6}
	h.fscod = int(r.u(2))
	frmsizecod := int(r.u(6))
	if h.fscod == 3 || frmsizecod >= 38 {
		return nil, errors.New("remux: bad AC-3 header")
	}
	h.bsid = int(r.u(5))
	h.bsmod = int(r.u(3))
	h.acmod = int(r.u(3))
	if h.acmod&1 != 0 && h.acmod != 1 {
		r.u(2)
	}
	if h.acmod&4 != 0 {
		r.u(2)
	}
	if h.acmod == 2 {
		r.u(2)
	}
	h.lfeon = int(r.u(1))
	h.sampleRate = ac3Rates[h.fscod]
	h.bitRateCode = frmsizecod >> 1
	words := ac3FrameWords[frmsizecod>>1][h.fscod]
	if h.fscod == 1 && frmsizecod&1 == 1 {
		words++
	}
	h.frameSize = words * 2
	return h, nil
}

func parseEAC3Header(b []byte) (*ac3Header, error) {
	r := &bitReader{b: b[2:]}
	h := &ac3Header{eac3: true, chanmap: -1}
	h.strmtyp = int(r.u(2))
	h.substreamID = int(r.u(3))
	h.frameSize = (int(r.u(11)) + 1) * 2
	h.fscod = int(r.u(2))
	if h.fscod == 3 {
		fscod2 := int(r.u(2))
		if fscod2 == 3 {
			return nil, errors.New("remux: bad E-AC-3 header")
		}
		h.sampleRate = ac3Rates[fscod2] / 2
		h.blocks = 6
	} else {
		h.sampleRate = ac3Rates[h.fscod]
		h.blocks = [...]int{1, 2, 3, 6}[r.u(2)]
	}
	h.acmod = int(r.u(3))
	h.lfeon = int(r.u(1))
	h.bsid = int(r.u(5))
	r.u(5) // dialnorm
	if r.flag() {
		r.u(8)
	}
	if h.acmod == 0 {
		r.u(5)
		if r.flag() {
			r.u(8)
		}
	}
	if h.strmtyp == 1 && r.flag() {
		h.chanmap = int(r.u(16))
	}
	if r.err != nil || h.frameSize < 8 {
		return nil, errors.New("remux: bad E-AC-3 header")
	}
	return h, nil
}

// dac3 returns the AC3SpecificBox payload.
func (h *ac3Header) dac3() []byte {
	v := uint32(h.fscod)<<22 | uint32(h.bsid)<<17 | uint32(h.bsmod)<<14 |
		uint32(h.acmod)<<11 | uint32(h.lfeon)<<10 | uint32(h.bitRateCode)<<5
	return []byte{byte(v >> 16), byte(v >> 8), byte(v)}
}

// ac3Frames splits a buffer into syncframes. It stops at the first byte
// that does not start a valid frame, returning what it found.
func ac3Frames(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 8 {
		h, err := parseAC3Header(b)
		if err != nil || h.frameSize > len(b) {
			break
		}
		out = append(out, b[:h.frameSize])
		b = b[h.frameSize:]
	}
	return out
}

// eac3Info builds the EC3SpecificBox payload and the channel count from
// the syncframes of one access unit (an independent substream and its
// dependent substreams).
func eac3Info(frames [][]byte) (dec3 []byte, channels int, err error) {
	type sub struct {
		h       *ac3Header
		numDep  int
		chanLoc int
	}
	var subs []*sub
	bitrate := 0
	for _, f := range frames {
		h, err := parseAC3Header(f)
		if err != nil {
			return nil, 0, err
		}
		blocks := h.blocks
		if blocks == 0 {
			blocks = 6
		}
		bitrate += h.frameSize * 8 * h.sampleRate / (blocks * 256)
		if !h.eac3 || h.strmtyp != 1 {
			if h.substreamID == 0 || len(subs) == 0 || !h.eac3 {
				subs = append(subs, &sub{h: h})
				channels += ac3Channels(h.acmod, h.lfeon)
			}
			continue
		}
		if len(subs) == 0 {
			continue
		}
		p := subs[len(subs)-1]
		p.numDep++
		if h.chanmap >= 0 {
			p.chanLoc |= (h.chanmap >> 5) & 0x1ff
		}
		channels += ac3Channels(h.acmod, h.lfeon) // approximate: extra channels
	}
	if len(subs) == 0 {
		return nil, 0, errors.New("remux: no E-AC-3 independent substream")
	}
	w := &bitWriter{}
	w.put(uint64(bitrate/1000), 13)
	w.put(uint64(len(subs)-1), 3)
	for _, s := range subs {
		w.put(uint64(s.h.fscod), 2)
		w.put(uint64(s.h.bsid), 5)
		w.put(0, 1) // reserved
		w.put(0, 1) // asvc
		w.put(uint64(s.h.bsmod), 3)
		w.put(uint64(s.h.acmod), 3)
		w.put(uint64(s.h.lfeon), 1)
		w.put(0, 3)
		w.put(uint64(s.numDep), 4)
		if s.numDep > 0 {
			w.put(uint64(s.chanLoc), 9)
		} else {
			w.put(0, 1)
		}
	}
	if len(subs) == 1 && subs[0].numDep == 0 {
		channels = ac3Channels(subs[0].h.acmod, subs[0].h.lfeon)
	}
	return w.bytes(), channels, nil
}

// bitWriter writes big-endian bit fields.
type bitWriter struct {
	b    []byte
	nbit int
}

func (w *bitWriter) put(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.nbit%8 == 0 {
			w.b = append(w.b, 0)
		}
		if v>>uint(i)&1 == 1 {
			w.b[len(w.b)-1] |= 0x80 >> uint(w.nbit%8)
		}
		w.nbit++
	}
}

func (w *bitWriter) bytes() []byte { return w.b }
