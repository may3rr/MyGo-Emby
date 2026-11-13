package remux

import (
	"errors"
	"fmt"
)

// H.264 NAL unit types used here.
const (
	h264NALSlice = 1
	h264NALIDR   = 5
	h264NALSEI   = 6
	h264NALSPS   = 7
	h264NALPPS   = 8
	h264NALAUD   = 9
)

// colourInfo is the VUI colour description of a video stream.
type colourInfo struct {
	primaries, transfer, matrix uint8
	fullRange                   bool
}

// videoRange is the HLS VIDEO-RANGE of a transfer characteristic.
func (c *colourInfo) videoRange() string {
	if c == nil {
		return "SDR"
	}
	switch c.transfer {
	case 16:
		return "PQ"
	case 18:
		return "HLG"
	}
	return "SDR"
}

// spsInfo is what the muxer needs from a sequence parameter set.
type spsInfo struct {
	profile, compat, level uint8 // H.264
	chromaFormat           uint8
	bitDepthLuma           uint8
	bitDepthChroma         uint8
	width, height          int
	sarNum, sarDen         int
	colour                 *colourInfo
	fpsNum, fpsDen         uint32 // from VUI timing, 0 if absent
}

var sarTable = [...][2]int{
	{0, 0}, {1, 1}, {12, 11}, {10, 11}, {16, 11}, {40, 33}, {24, 11}, {20, 11},
	{32, 11}, {80, 33}, {18, 11}, {15, 11}, {64, 33}, {160, 99}, {4, 3}, {3, 2}, {2, 1},
}

// parseH264SPS parses an H.264 SPS NAL unit (with its header byte).
func parseH264SPS(nal []byte) (*spsInfo, error) {
	if len(nal) < 4 || nal[0]&0x1f != h264NALSPS {
		return nil, errors.New("remux: not an H.264 SPS")
	}
	r := &bitReader{b: unescapeRBSP(nal[1:])}
	s := &spsInfo{chromaFormat: 1, bitDepthLuma: 8, bitDepthChroma: 8, sarNum: 1, sarDen: 1}
	s.profile = uint8(r.u(8))
	s.compat = uint8(r.u(8))
	s.level = uint8(r.u(8))
	r.ue() // seq_parameter_set_id
	switch s.profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		s.chromaFormat = uint8(r.ue())
		if s.chromaFormat == 3 {
			r.u(1)
		}
		s.bitDepthLuma = uint8(r.ue() + 8)
		s.bitDepthChroma = uint8(r.ue() + 8)
		r.u(1) // qpprime_y_zero_transform_bypass_flag
		if r.flag() {
			n := 8
			if s.chromaFormat == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if r.flag() {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := int64(8), int64(8)
					for j := 0; j < size; j++ {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue() // log2_max_frame_num_minus4
	switch r.ue() {
	case 0:
		r.ue()
	case 1:
		r.u(1)
		r.se()
		r.se()
		n := r.ue()
		for i := uint64(0); i < n && r.err == nil; i++ {
			r.se()
		}
	}
	r.ue() // max_num_ref_frames
	r.u(1)
	wMbs := int(r.ue() + 1)
	hMapUnits := int(r.ue() + 1)
	frameMbsOnly := r.u(1)
	if frameMbsOnly == 0 {
		r.u(1)
	}
	r.u(1) // direct_8x8_inference_flag
	var cl, cr, ct, cb int
	if r.flag() {
		cl, cr, ct, cb = int(r.ue()), int(r.ue()), int(r.ue()), int(r.ue())
	}
	cropX, cropY := 1, 2-int(frameMbsOnly)
	switch s.chromaFormat {
	case 1:
		cropX, cropY = 2, 2*(2-int(frameMbsOnly))
	case 2:
		cropX = 2
	}
	s.width = wMbs*16 - cropX*(cl+cr)
	s.height = (2-int(frameMbsOnly))*hMapUnits*16 - cropY*(ct+cb)
	if r.flag() { // vui_parameters_present_flag
		parseVUICommon(r, s)
		if r.flag() { // timing_info_present_flag
			units := uint32(r.u(32))
			scale := uint32(r.u(32))
			if units > 0 && scale > 0 {
				s.fpsNum, s.fpsDen = scale, units*2
			}
		}
	}
	if r.err != nil && s.width <= 0 {
		return nil, r.err
	}
	if s.width <= 0 || s.height <= 0 {
		return nil, errors.New("remux: bad H.264 SPS dimensions")
	}
	return s, nil
}

// parseVUICommon reads the VUI fields shared by H.264 and H.265 up to and
// including the chroma location.
func parseVUICommon(r *bitReader, s *spsInfo) {
	if r.flag() { // aspect_ratio_info_present_flag
		idc := int(r.u(8))
		if idc == 255 {
			s.sarNum, s.sarDen = int(r.u(16)), int(r.u(16))
		} else if idc > 0 && idc < len(sarTable) {
			s.sarNum, s.sarDen = sarTable[idc][0], sarTable[idc][1]
		}
		if s.sarNum <= 0 || s.sarDen <= 0 {
			s.sarNum, s.sarDen = 1, 1
		}
	}
	if r.flag() { // overscan_info_present_flag
		r.u(1)
	}
	if r.flag() { // video_signal_type_present_flag
		r.u(3)
		full := r.flag()
		if r.flag() {
			s.colour = &colourInfo{
				primaries: uint8(r.u(8)),
				transfer:  uint8(r.u(8)),
				matrix:    uint8(r.u(8)),
				fullRange: full,
			}
		}
	}
	if r.flag() { // chroma_loc_info_present_flag
		r.ue()
		r.ue()
	}
}

// avcConfig is a parsed AVCDecoderConfigurationRecord.
type avcConfig struct {
	lengthSize int
	sps, pps   [][]byte
	ext        []byte // trailing high-profile fields, kept verbatim
}

func parseAVCC(b []byte) (*avcConfig, error) {
	if len(b) < 7 || b[0] != 1 {
		return nil, errors.New("remux: bad avcC")
	}
	c := &avcConfig{lengthSize: int(b[4]&3) + 1}
	n := int(b[5] & 0x1f)
	p := 6
	for i := 0; i < n; i++ {
		if p+2 > len(b) {
			return nil, errors.New("remux: short avcC")
		}
		l := int(b[p])<<8 | int(b[p+1])
		p += 2
		if p+l > len(b) {
			return nil, errors.New("remux: short avcC")
		}
		c.sps = append(c.sps, b[p:p+l])
		p += l
	}
	if p >= len(b) {
		return c, nil
	}
	n = int(b[p])
	p++
	for i := 0; i < n; i++ {
		if p+2 > len(b) {
			return nil, errors.New("remux: short avcC")
		}
		l := int(b[p])<<8 | int(b[p+1])
		p += 2
		if p+l > len(b) {
			return nil, errors.New("remux: short avcC")
		}
		c.pps = append(c.pps, b[p:p+l])
		p += l
	}
	c.ext = b[p:]
	return c, nil
}

// buildAVCC writes an AVCDecoderConfigurationRecord with 4-byte lengths.
func buildAVCC(sps, pps [][]byte) ([]byte, error) {
	if len(sps) == 0 || len(pps) == 0 {
		return nil, errors.New("remux: H.264 parameter sets missing")
	}
	info, err := parseH264SPS(sps[0])
	if err != nil {
		return nil, err
	}
	b := []byte{1, sps[0][1], sps[0][2], sps[0][3], 0xff, 0xe0 | byte(len(sps))}
	for _, s := range sps {
		b = append(b, byte(len(s)>>8), byte(len(s)))
		b = append(b, s...)
	}
	b = append(b, byte(len(pps)))
	for _, p := range pps {
		b = append(b, byte(len(p)>>8), byte(len(p)))
		b = append(b, p...)
	}
	switch info.profile {
	case 100, 110, 122, 144:
		b = append(b, 0xfc|info.chromaFormat&3, 0xf8|(info.bitDepthLuma-8)&7, 0xf8|(info.bitDepthChroma-8)&7, 0)
	}
	return b, nil
}

func avcCodecString(sps *spsInfo) string {
	return fmt.Sprintf("avc1.%02X%02X%02X", sps.profile, sps.compat, sps.level)
}

// h264Keyframe reports whether an Annex B or length-prefixed access unit
// holds an IDR slice.
func h264IsIDR(nals [][]byte) bool {
	for _, n := range nals {
		if len(n) > 0 && n[0]&0x1f == h264NALIDR {
			return true
		}
	}
	return false
}
