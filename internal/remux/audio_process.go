package remux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	// ErrAudioTranscoderUnavailable: the selected audio needs local
	// decoding, but FFmpeg was not found or did not start.
	ErrAudioTranscoderUnavailable = errors.New("remux: ffmpeg audio transcoder unavailable")
	// ErrAudioTranscodeFailed: FFmpeg ran but did not produce usable AAC.
	ErrAudioTranscodeFailed = errors.New("remux: ffmpeg audio transcoding failed")
)

const (
	audioTranscodeRate     = 48000
	audioTranscodeChannels = 2
	audioTranscodeBitrate  = "192k"
)

func (s *Server) transcodeAudio(ctx context.Context, input string, audio *track,
	seek, start, duration time.Duration) ([][]byte, error) {
	if s.ffmpeg == "" {
		return nil, ErrAudioTranscoderUnavailable
	}
	stream := fmt.Sprintf("0:a:%d", audio.order)
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if seek > 0 {
		args = append(args, "-ss", formatFFmpegDuration(seek))
	}
	pad := 2 * time.Second * 1024 / time.Duration(audioTranscodeRate)
	args = append(args,
		"-i", input,
		"-map", stream,
		"-vn", "-sn", "-dn",
		"-t", formatFFmpegDuration(duration+pad),
		"-ac", fmt.Sprint(audioTranscodeChannels),
		"-ar", fmt.Sprint(audioTranscodeRate),
		"-c:a", "aac",
		"-b:a", audioTranscodeBitrate,
		"-f", "adts",
		"pipe:1",
	)

	cmd := exec.CommandContext(ctx, s.ffmpeg, args...)
	configureAudioProcess(cmd)
	cmd.Cancel = func() error { return terminateAudioProcess(cmd) }
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAudioTranscoderUnavailable, err)
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := sanitizeFFmpegError(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", ErrAudioTranscodeFailed, msg)
		}
		return nil, fmt.Errorf("%w: %v", ErrAudioTranscodeFailed, err)
	}
	frames, err := splitADTSFrames(stdout.Bytes())
	if err != nil || len(frames) == 0 {
		if err == nil {
			err = errors.New("no AAC frames")
		}
		return nil, fmt.Errorf("%w: %v", ErrAudioTranscodeFailed, err)
	}
	// ADTS cannot carry MP4's encoder-delay edit list. FFmpeg's native AAC
	// encoder emits one initial 1024-sample priming frame; keeping it puts
	// silence at every independently encoded segment and breaks a seek's
	// first audio frame. The segment's AAC frame grid starts exactly where
	// the previous crossing frame ends, and the ending pad is not emitted.
	if len(frames) > 1 {
		frames = frames[1:]
	}
	return frames, nil
}

// transcodeFrameStart rounds a segment boundary up to the AAC frame grid.
// The previous segment includes the frame that crosses the boundary, so
// this segment starts exactly where that frame ends: neither the encoder
// priming frame nor padding creates a gap or repeated audio.
func transcodeFrameStart(t time.Duration) time.Duration {
	samples := (t.Nanoseconds()*audioTranscodeRate + int64(time.Second) - 1) / int64(time.Second)
	frame := (samples + 1023) / 1024
	return time.Duration(frame*1024) * time.Second / audioTranscodeRate
}

func formatFFmpegDuration(d time.Duration) string {
	return fmt.Sprintf("%.6f", d.Seconds())
}

func sanitizeFFmpegError(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	for {
		i := strings.Index(s, "http://")
		j := strings.Index(s, "https://")
		if i < 0 || j >= 0 && j < i {
			i = j
		}
		if i < 0 {
			break
		}
		k := i
		for k < len(s) && s[k] != ' ' && s[k] != '\t' {
			k++
		}
		s = s[:i] + "<url>" + s[k:]
	}
	const limit = 400
	if len(s) > limit {
		s = s[len(s)-limit:]
	}
	return s
}

func splitADTSFrames(b []byte) ([][]byte, error) {
	var frames [][]byte
	for len(b) > 0 {
		for len(b) > 0 && (len(b) < 2 || b[0] != 0xff || b[1]&0xf6 != 0xf0) {
			b = b[1:]
		}
		if len(b) == 0 {
			break
		}
		h, err := parseADTS(b)
		if err != nil {
			return nil, err
		}
		if h.frameLen > len(b) {
			return nil, errors.New("truncated ADTS frame")
		}
		frames = append(frames, b[h.headerLen:h.frameLen])
		b = b[h.frameLen:]
	}
	return frames, nil
}

func transcodeAACOutput() *outTrack {
	return &outTrack{
		fourcc: "mp4a", timescale: audioTranscodeRate,
		config: makeASC(2, audioTranscodeRate, audioTranscodeChannels),
		codecs: "mp4a.40.2", sampleRate: audioTranscodeRate,
		channels: audioTranscodeChannels, frameSamples: 1024,
	}
}
