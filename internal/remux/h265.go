package remux

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// H.265 NAL unit types used here.
const (
	hevcNALBLAWLP     = 16
	hevcNALCRA        = 21
	hevcNALRSVIRAP23  = 23
	hevcNALVPS        = 32
	hevcNALSPS        = 33
	hevcNALPPS        = 34
	hevcNALAUD        = 35
	hevcNALSEIPrefix  = 39
	hevcNALSEISuffix  = 40
	hevcConfigHdrSize = 23
)

func hevcNALType(nal []byte) int {
	if len(nal) == 0 {
		return -1
	}
	return int(nal[0]>>1) & 0x3f
}

// hevcPTL is the general profile, tier and level of an HEVC stream.
type hevcPTL struct {
	profileSpace uint8
	tier         uint8
	profileIdc   uint8
	compat       uint32
	constraints  [6]byte
	level        uint8
}

// hevcSPS is what the muxer needs from an HEVC SPS.
type hevcSPS struct {
	spsInfo
	ptl              hevcPTL
	maxSubLayers     uint8
	temporalIDNested bool
}

// parseHEVCSPS parses an HEVC SPS NAL unit (with its 2-byte header).
func parseHEVCSPS(nal []byte) (*hevcSPS, error) {
	if hevcNALType(nal) != hevcNALSPS || len(nal) < 4 {
		return nil, errors.New("remux: not an HEVC SPS")
	}
	r := &bitReader{b: unescapeRBSP(nal[2:])}
	s := &hevcSPS{}
	s.sarNum, s.sarDen = 1, 1
	r.u(4) // sps_video_parameter_set_id
	maxSubLayersMinus1 := int(r.u(3))
	s.maxSubLayers = uint8(maxSubLayersMinus1 + 1)
	s.temporalIDNested = r.flag()
	s.ptl = parsePTL(r, maxSubLayersMinus1)
	r.ue() // sps_seq_parameter_set_id
	s.chromaFormat = uint8(r.ue())
	if s.chromaFormat == 3 {
		r.u(1)
	}
	w, h := int(r.ue()), int(r.ue())
	if r.flag() { // conformance_window_flag
		subW, subH := 1, 1
		switch s.chromaFormat {
		case 1:
			subW, subH = 2, 2
		case 2:
			subW = 2
		}
		l, rr, t, b := int(r.ue()), int(r.ue()), int(r.ue()), int(r.ue())
		w -= subW * (l + rr)
		h -= subH * (t + b)
	}
	s.width, s.height = w, h
	s.bitDepthLuma = uint8(r.ue() + 8)
	s.bitDepthChroma = uint8(r.ue() + 8)
	log2MaxPOCLsb := int(r.ue() + 4)
	subLayerOrdering := r.flag()
	first := maxSubLayersMinus1
	if subLayerOrdering {
		first = 0
	}
	for i := first; i <= maxSubLayersMinus1; i++ {
		r.ue()
		r.ue()
		r.ue()
	}
	for i := 0; i < 6; i++ {
		r.ue() // block sizes and transform depths
	}
	if r.flag() { // scaling_list_enabled_flag
		if r.flag() { // sps_scaling_list_data_present_flag
			skipHEVCScalingList(r)
		}
	}
	r.u(1)        // amp_enabled_flag
	r.u(1)        // sample_adaptive_offset_enabled_flag
	if r.flag() { // pcm_enabled_flag
		r.u(4)
		r.u(4)
		r.ue()
		r.ue()
		r.u(1)
	}
	numSets := int(r.ue())
	if numSets > 64 {
		return nil, errors.New("remux: bad HEVC SPS")
	}
	numDeltaPocs := make([]int, numSets)
	for i := 0; i < numSets && r.err == nil; i++ {
		inter := false
		if i != 0 {
			inter = r.flag()
		}
		if inter {
			r.u(1) // delta_rps_sign
			r.ue() // abs_delta_rps_minus1
			ref := i - 1
			n := 0
			for j := 0; j <= numDeltaPocs[ref]; j++ {
				used := r.flag()
				use := true
				if !used {
					use = r.flag()
				}
				if used || use {
					n++
				}
			}
			numDeltaPocs[i] = n
		} else {
			neg, pos := int(r.ue()), int(r.ue())
			if neg > 16 || pos > 16 {
				return nil, errors.New("remux: bad HEVC SPS")
			}
			for j := 0; j < neg+pos; j++ {
				r.ue()
				r.u(1)
			}
			numDeltaPocs[i] = neg + pos
		}
	}
	if r.flag() { // long_term_ref_pics_present_flag
		n := int(r.ue())
		for i := 0; i < n && r.err == nil; i++ {
			r.u(log2MaxPOCLsb)
			r.u(1)
		}
	}
	r.u(1)        // sps_temporal_mvp_enabled_flag
	r.u(1)        // strong_intra_smoothing_enabled_flag
	if r.flag() { // vui_parameters_present_flag
		parseVUICommon(r, &s.spsInfo)
		r.u(1)        // neutral_chroma_indication_flag
		r.u(1)        // field_seq_flag
		r.u(1)        // frame_field_info_present_flag
		if r.flag() { // default_display_window_flag
			r.ue()
			r.ue()
			r.ue()
			r.ue()
		}
		if r.flag() { // vui_timing_info_present_flag
			units := uint32(r.u(32))
			scale := uint32(r.u(32))
			if units > 0 && scale > 0 {
				s.fpsNum, s.fpsDen = scale, units
			}
		}
	}
	if s.width <= 0 || s.height <= 0 {
		return nil, errors.New("remux: bad HEVC SPS dimensions")
	}
	return s, nil
}

func parsePTL(r *bitReader, maxSubLayersMinus1 int) hevcPTL {
	var p hevcPTL
	p.profileSpace = uint8(r.u(2))
	p.tier = uint8(r.u(1))
	p.profileIdc = uint8(r.u(5))
	p.compat = uint32(r.u(32))
	for i := range p.constraints {
		p.constraints[i] = byte(r.u(8))
	}
	p.level = uint8(r.u(8))
	profilePresent := make([]bool, maxSubLayersMinus1)
	levelPresent := make([]bool, maxSubLayersMinus1)
	for i := 0; i < maxSubLayersMinus1; i++ {
		profilePresent[i] = r.flag()
		levelPresent[i] = r.flag()
	}
	if maxSubLayersMinus1 > 0 {
		for i := maxSubLayersMinus1; i < 8; i++ {
			r.u(2)
		}
	}
	for i := 0; i < maxSubLayersMinus1; i++ {
		if profilePresent[i] {
			r.skip(88)
		}
		if levelPresent[i] {
			r.skip(8)
		}
	}
	return p
}

func skipHEVCScalingList(r *bitReader) {
	for sizeID := 0; sizeID < 4; sizeID++ {
		step := 1
		if sizeID == 3 {
			step = 3
		}
		for matrixID := 0; matrixID < 6; matrixID += step {
			if !r.flag() {
				r.ue()
				continue
			}
			n := 1 << (4 + (sizeID << 1))
			if n > 64 {
				n = 64
			}
			if sizeID > 1 {
				r.se()
			}
			for i := 0; i < n; i++ {
				r.se()
			}
		}
	}
}

// hevcConfig is a parsed HEVCDecoderConfigurationRecord.
type hevcConfig struct {
	header     [hevcConfigHdrSize]byte
	lengthSize int
	arrays     []hevcArray
}

type hevcArray struct {
	complete bool
	nalType  int
	nals     [][]byte
}

func parseHVCC(b []byte) (*hevcConfig, error) {
	if len(b) < hevcConfigHdrSize || b[0] != 1 {
		return nil, errors.New("remux: bad hvcC")
	}
	c := &hevcConfig{lengthSize: int(b[21]&3) + 1}
	copy(c.header[:], b)
	n := int(b[22])
	p := hevcConfigHdrSize
	for i := 0; i < n; i++ {
		if p+3 > len(b) {
			return nil, errors.New("remux: short hvcC")
		}
		a := hevcArray{complete: b[p]&0x80 != 0, nalType: int(b[p] & 0x3f)}
		cnt := int(b[p+1])<<8 | int(b[p+2])
		p += 3
		for j := 0; j < cnt; j++ {
			if p+2 > len(b) {
				return nil, errors.New("remux: short hvcC")
			}
			l := int(b[p])<<8 | int(b[p+1])
			p += 2
			if p+l > len(b) {
				return nil, errors.New("remux: short hvcC")
			}
			a.nals = append(a.nals, b[p:p+l])
			p += l
		}
		c.arrays = append(c.arrays, a)
	}
	return c, nil
}

// nalsOf returns the parameter sets of one type.
func (c *hevcConfig) nalsOf(t int) [][]byte {
	var out [][]byte
	for _, a := range c.arrays {
		if a.nalType == t {
			out = append(out, a.nals...)
		}
	}
	return out
}

// bytes writes the record for an hvc1 sample entry: parameter set arrays
// marked complete, VPS, SPS, PPS first.
func (c *hevcConfig) bytes() []byte {
	b := append([]byte(nil), c.header[:]...)
	var arrays []hevcArray
	for _, t := range []int{hevcNALVPS, hevcNALSPS, hevcNALPPS} {
		if n := c.nalsOf(t); len(n) > 0 {
			arrays = append(arrays, hevcArray{complete: true, nalType: t, nals: n})
		}
	}
	for _, a := range c.arrays {
		if a.nalType != hevcNALVPS && a.nalType != hevcNALSPS && a.nalType != hevcNALPPS && len(a.nals) > 0 {
			arrays = append(arrays, a)
		}
	}
	b[22] = byte(len(arrays))
	for _, a := range arrays {
		h := byte(a.nalType & 0x3f)
		if a.complete {
			h |= 0x80
		}
		b = append(b, h, byte(len(a.nals)>>8), byte(len(a.nals)))
		for _, n := range a.nals {
			b = append(b, byte(len(n)>>8), byte(len(n)))
			b = append(b, n...)
		}
	}
	return b
}

// buildHVCC makes a decoder configuration record from parameter sets, for
// streams that carry them in-band (MPEG-TS, hev1).
func buildHVCC(vps, sps, pps [][]byte) (*hevcConfig, error) {
	if len(vps) == 0 || len(sps) == 0 || len(pps) == 0 {
		return nil, errors.New("remux: HEVC parameter sets missing")
	}
	s, err := parseHEVCSPS(sps[0])
	if err != nil {
		return nil, err
	}
	c := &hevcConfig{lengthSize: 4}
	h := c.header[:]
	h[0] = 1
	h[1] = s.ptl.profileSpace<<6 | s.ptl.tier<<5 | s.ptl.profileIdc&0x1f
	h[2], h[3], h[4], h[5] = byte(s.ptl.compat>>24), byte(s.ptl.compat>>16), byte(s.ptl.compat>>8), byte(s.ptl.compat)
	copy(h[6:12], s.ptl.constraints[:])
	h[12] = s.ptl.level
	h[13], h[14] = 0xf0, 0 // min_spatial_segmentation_idc
	h[15] = 0xfc           // parallelismType unknown
	h[16] = 0xfc | s.chromaFormat&3
	h[17] = 0xf8 | (s.bitDepthLuma-8)&7
	h[18] = 0xf8 | (s.bitDepthChroma-8)&7
	h[19], h[20] = 0, 0 // avgFrameRate
	nested := byte(0)
	if s.temporalIDNested {
		nested = 1
	}
	h[21] = (s.maxSubLayers&7)<<3 | nested<<2 | 3
	c.arrays = []hevcArray{
		{complete: true, nalType: hevcNALVPS, nals: vps},
		{complete: true, nalType: hevcNALSPS, nals: sps},
		{complete: true, nalType: hevcNALPPS, nals: pps},
	}
	return c, nil
}

// hevcCodecString returns the RFC 6381 codec string of an hvc1 track, as
// "hvc1.2.4.L153.B0".
func hevcCodecString(fourcc string, hdr []byte) string {
	if len(hdr) < 13 {
		return fourcc
	}
	space := hdr[1] >> 6
	tier := (hdr[1] >> 5) & 1
	idc := hdr[1] & 0x1f
	compat := uint32(hdr[2])<<24 | uint32(hdr[3])<<16 | uint32(hdr[4])<<8 | uint32(hdr[5])
	var rev uint32
	for i := 0; i < 32; i++ {
		if compat&(1<<uint(i)) != 0 {
			rev |= 1 << uint(31-i)
		}
	}
	var sb strings.Builder
	sb.WriteString(fourcc)
	sb.WriteByte('.')
	if space > 0 {
		sb.WriteByte('A' + space - 1)
	}
	sb.WriteString(strconv.Itoa(int(idc)))
	sb.WriteString("." + strconv.FormatUint(uint64(rev), 16))
	if tier == 1 {
		sb.WriteString(".H")
	} else {
		sb.WriteString(".L")
	}
	sb.WriteString(strconv.Itoa(int(hdr[12])))
	cons := hdr[6:12]
	last := len(cons)
	for last > 0 && cons[last-1] == 0 {
		last--
	}
	for i := 0; i < last; i++ {
		sb.WriteString(fmt.Sprintf(".%X", cons[i]))
	}
	return sb.String()
}

// hevcIsIRAP reports whether a NAL unit type is an intra random access
// point (IDR, CRA or BLA).
func hevcIsIRAP(t int) bool { return t >= hevcNALBLAWLP && t <= hevcNALRSVIRAP23 }
