package remux

import (
	"bytes"
	"errors"
	"fmt"
	"time"
)

const videoTimescale = 90000

// setupVideo fills t.out from a decoder configuration record (avcC or
// hvcC). A record without parameter sets leaves t.needParams set; the
// demuxer completes it from the first keyframe with completeVideo.
func setupVideo(t *track, config []byte, width, height int) {
	switch t.codec {
	case "h264":
		if len(config) == 0 {
			t.needParams, t.lengthSize = true, 4
			t.out = &outTrack{video: true, fourcc: "avc1", timescale: videoTimescale, width: width, height: height}
			return
		}
		c, err := parseAVCC(config)
		if err != nil {
			t.why = err.Error()
			return
		}
		t.lengthSize = c.lengthSize
		t.out = &outTrack{video: true, fourcc: "avc1", timescale: videoTimescale, config: config, width: width, height: height}
		if len(c.sps) == 0 || len(c.pps) == 0 {
			t.needParams = true
			return
		}
		applyH264SPS(t, c.sps[0])
	case "hevc":
		t.out = &outTrack{video: true, fourcc: "hvc1", timescale: videoTimescale, width: width, height: height}
		if len(config) == 0 {
			t.needParams, t.lengthSize = true, 4
			return
		}
		c, err := parseHVCC(config)
		if err != nil {
			t.why = err.Error()
			t.out = nil
			return
		}
		t.lengthSize = c.lengthSize
		if len(c.nalsOf(hevcNALVPS)) == 0 || len(c.nalsOf(hevcNALSPS)) == 0 || len(c.nalsOf(hevcNALPPS)) == 0 {
			t.needParams = true
			t.out.config = config
			return
		}
		applyHEVCConfig(t, c)
	default:
		t.why = "video codec " + t.codec + " is not supported"
	}
}

func applyH264SPS(t *track, sps []byte) {
	s, err := parseH264SPS(sps)
	if err != nil {
		t.why = err.Error()
		t.out = nil
		return
	}
	if s.bitDepthLuma > 8 || s.chromaFormat > 1 {
		t.why = fmt.Sprintf("H.264 %d-bit %s cannot be decoded by AVPlayer", s.bitDepthLuma, chromaName(s.chromaFormat))
		t.out = nil
		return
	}
	o := t.out
	o.width, o.height = s.width, s.height
	o.sarNum, o.sarDen = s.sarNum, s.sarDen
	o.colour = s.colour
	o.codecs = avcCodecString(s)
	if s.fpsNum > 0 && o.frameRate == 0 {
		o.frameRate = float64(s.fpsNum) / float64(s.fpsDen)
	}
}

func applyHEVCConfig(t *track, c *hevcConfig) {
	s, err := parseHEVCSPS(c.nalsOf(hevcNALSPS)[0])
	if err != nil {
		t.why = err.Error()
		t.out = nil
		return
	}
	o := t.out
	o.config = c.bytes()
	o.width, o.height = s.width, s.height
	o.sarNum, o.sarDen = s.sarNum, s.sarDen
	if o.colour == nil {
		o.colour = s.colour
	}
	o.codecs = hevcCodecString(o.fourcc, c.header[:])
	if s.fpsNum > 0 && o.frameRate == 0 {
		o.frameRate = float64(s.fpsNum) / float64(s.fpsDen)
	}
	t.needParams = false
}

func chromaName(c uint8) string {
	switch c {
	case 0:
		return "4:0:0"
	case 2:
		return "4:2:2"
	case 3:
		return "4:4:4"
	}
	return "4:2:0"
}

// completeVideo takes the parameter sets of a keyframe (length-prefixed
// with t.lengthSize, or Annex B when 0) into the configuration record.
func completeVideo(t *track, frame []byte) error {
	var nals [][]byte
	if t.lengthSize == 0 {
		nals = splitAnnexB(frame)
	} else {
		forEachNAL(frame, t.lengthSize, func(n []byte) bool {
			nals = append(nals, n)
			return true
		})
	}
	switch t.codec {
	case "h264":
		var sps, pps [][]byte
		for _, n := range nals {
			switch n[0] & 0x1f {
			case h264NALSPS:
				sps = appendUnique(sps, n)
			case h264NALPPS:
				pps = appendUnique(pps, n)
			}
		}
		cfg, err := buildAVCC(sps, pps)
		if err != nil {
			return err
		}
		if t.lengthSize != 0 && t.lengthSize != 4 {
			cfg[4] = 0xfc | byte(t.lengthSize-1)
		}
		t.out.config = cfg
		t.needParams = false
		applyH264SPS(t, sps[0])
	case "hevc":
		var vps, sps, pps [][]byte
		for _, n := range nals {
			switch hevcNALType(n) {
			case hevcNALVPS:
				vps = appendUnique(vps, n)
			case hevcNALSPS:
				sps = appendUnique(sps, n)
			case hevcNALPPS:
				pps = appendUnique(pps, n)
			}
		}
		c, err := buildHVCC(vps, sps, pps)
		if err != nil {
			return err
		}
		if t.lengthSize != 0 {
			c.header[21] = c.header[21]&^3 | byte(t.lengthSize-1)
		}
		applyHEVCConfig(t, c)
	}
	if t.out == nil {
		return errors.New(t.why)
	}
	return nil
}

func appendUnique(list [][]byte, n []byte) [][]byte {
	for _, x := range list {
		if bytes.Equal(x, n) {
			return list
		}
	}
	return append(list, append([]byte(nil), n...))
}

// setupAudio fills t.out for an audio track. AAC needs its
// AudioSpecificConfig; AC-3 and E-AC-3 leave t.needParams set until
// completeAudio sees a frame.
func setupAudio(t *track, config []byte, sampleRate, channels int) {
	switch t.codec {
	case "aac":
		c, err := parseASC(config)
		if err != nil {
			if sampleRate <= 0 {
				t.needParams = true
				t.out = &outTrack{fourcc: "mp4a"}
				return
			}
			config = makeASC(2, sampleRate, channels)
			c, _ = parseASC(config)
		}
		t.out = &outTrack{fourcc: "mp4a", config: config, timescale: uint32(c.sampleRate),
			sampleRate: c.sampleRate, channels: c.channels, frameSamples: 1024, codecs: c.codecString()}
		if c.objectType == 5 || c.objectType == 29 || c.channels == 0 {
			// Explicit SBR or PCE: keep the container's view of channels.
			if channels > 0 {
				t.out.channels = channels
			}
		}
	case "ac3", "eac3":
		t.needParams = true
		fourcc := "ac-3"
		if t.codec == "eac3" {
			fourcc = "ec-3"
		}
		t.out = &outTrack{fourcc: fourcc, sampleRate: sampleRate, channels: channels, frameSamples: 1536}
	default:
		t.why = "audio codec " + t.codec + " is not supported by AVPlayer without transcoding"
	}
}

// completeAudio reads the codec parameters of an AC-3 or E-AC-3 track from
// one access unit, or an AAC track's from an ADTS frame.
func completeAudio(t *track, frame []byte) error {
	o := t.out
	switch t.codec {
	case "aac":
		h, err := parseADTS(frame)
		if err != nil {
			return err
		}
		o.config = makeASC(h.objectType, h.sampleRate, h.channels)
		o.sampleRate, o.channels = h.sampleRate, h.channels
		o.timescale = uint32(h.sampleRate)
		o.frameSamples = 1024
		o.codecs = fmt.Sprintf("mp4a.40.%d", h.objectType)
	case "ac3":
		h, err := parseAC3Header(frame)
		if err != nil {
			return err
		}
		if h.eac3 {
			t.codec, o.fourcc = "eac3", "ec-3"
			return completeAudio(t, frame)
		}
		o.config = h.dac3()
		o.sampleRate, o.channels = h.sampleRate, ac3Channels(h.acmod, h.lfeon)
		o.timescale = uint32(h.sampleRate)
		o.frameSamples = 1536
		o.codecs = "ac-3"
	case "eac3":
		frames := ac3Frames(frame)
		if len(frames) == 0 {
			return errors.New("remux: no E-AC-3 frame")
		}
		h, err := parseAC3Header(frames[0])
		if err != nil {
			return err
		}
		if !h.eac3 {
			t.codec, o.fourcc = "ac3", "ac-3"
			return completeAudio(t, frame)
		}
		dec3, ch, err := eac3Info(frames)
		if err != nil {
			return err
		}
		o.config = dec3
		o.sampleRate, o.channels = h.sampleRate, ch
		o.timescale = uint32(h.sampleRate)
		o.frameSamples = int64(256 * h.blocks)
		o.codecs = "ec-3"
	}
	t.needParams = false
	return nil
}

// frameDuration is the duration of one audio access unit.
func (o *outTrack) frameDuration() time.Duration {
	if o.sampleRate <= 0 || o.frameSamples <= 0 {
		return 0
	}
	return time.Duration(o.frameSamples) * time.Second / time.Duration(o.sampleRate)
}
