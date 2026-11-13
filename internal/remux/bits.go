package remux

import (
	"encoding/binary"
	"errors"
)

var errShortBits = errors.New("remux: bitstream ended early")

// bitReader reads big-endian bit fields and Exp-Golomb codes from an RBSP.
// Reads past the end yield zeros and set err, so parsers can check once.
type bitReader struct {
	b   []byte
	pos int // bit position
	err error
}

func (r *bitReader) u(n int) uint64 {
	var v uint64
	for i := 0; i < n; i++ {
		v <<= 1
		if r.pos>>3 >= len(r.b) {
			r.err = errShortBits
			r.pos++
			continue
		}
		v |= uint64(r.b[r.pos>>3]>>(7-uint(r.pos&7))) & 1
		r.pos++
	}
	return v
}

func (r *bitReader) flag() bool { return r.u(1) == 1 }

func (r *bitReader) skip(n int) {
	r.pos += n
	if r.pos > len(r.b)*8 {
		r.err = errShortBits
	}
}

// ue reads an unsigned Exp-Golomb code.
func (r *bitReader) ue() uint64 {
	zeros := 0
	for r.u(1) == 0 {
		if r.err != nil || zeros > 31 {
			r.err = errShortBits
			return 0
		}
		zeros++
	}
	return (1<<uint(zeros) - 1) + r.u(zeros)
}

// se reads a signed Exp-Golomb code.
func (r *bitReader) se() int64 {
	v := r.ue()
	if v&1 == 1 {
		return int64(v+1) / 2
	}
	return -int64(v / 2)
}

func (r *bitReader) left() int { return len(r.b)*8 - r.pos }

// unescapeRBSP removes emulation prevention bytes (00 00 03 → 00 00).
func unescapeRBSP(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, c := range nal {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// splitAnnexB returns the NAL units of an Annex B byte stream, without
// start codes. Trailing zero bytes of a NAL unit are dropped.
func splitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	i := 0
	for i+2 < len(b) {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				nals = appendNAL(nals, b[start:i])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 {
		nals = appendNAL(nals, b[start:])
	} else if len(b) > 0 {
		nals = appendNAL(nals, b)
	}
	return nals
}

func appendNAL(nals [][]byte, n []byte) [][]byte {
	for len(n) > 0 && n[len(n)-1] == 0 {
		n = n[:len(n)-1]
	}
	if len(n) > 0 {
		nals = append(nals, n)
	}
	return nals
}

// forEachNAL calls fn for each NAL unit of a length-prefixed sample.
func forEachNAL(sample []byte, lengthSize int, fn func(nal []byte) bool) {
	for len(sample) >= lengthSize {
		var n int
		switch lengthSize {
		case 1:
			n = int(sample[0])
		case 2:
			n = int(binary.BigEndian.Uint16(sample))
		case 3:
			n = int(sample[0])<<16 | int(sample[1])<<8 | int(sample[2])
		default:
			n = int(binary.BigEndian.Uint32(sample))
		}
		sample = sample[lengthSize:]
		if n > len(sample) || n < 0 {
			return
		}
		if !fn(sample[:n]) {
			return
		}
		sample = sample[n:]
	}
}

// appendLengthPrefixed appends nal with a 4-byte length.
func appendLengthPrefixed(dst, nal []byte) []byte {
	return append(binary.BigEndian.AppendUint32(dst, uint32(len(nal))), nal...)
}
