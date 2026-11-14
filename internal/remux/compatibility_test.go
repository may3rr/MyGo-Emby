package remux

import (
	"errors"
	"testing"

	"mygo-emby/internal/emby"
)

func TestMediaCompatibilityMatrix(t *testing.T) {
	type matrixCase struct {
		name           string
		container      string
		video          string
		videoTag       string
		audio          string
		mode           Mode
		audioTranscode bool
		wantErr        error
	}
	cases := []matrixCase{
		{name: "MP4 H.264 AAC", container: "mp4", video: "h264", videoTag: "avc1", audio: "aac", mode: DirectPlay},
		{name: "MOV H.264 AAC", container: "mov", video: "h264", videoTag: "avc1", audio: "aac", mode: DirectPlay},

		{name: "MKV H.264", container: "mkv", video: "h264", audio: "aac", mode: Remux},
		{name: "Matroska AVC", container: "matroska", video: "avc", audio: "aac", mode: Remux},
		{name: "WebM HEVC", container: "webm", video: "hevc", audio: "aac", mode: Remux},
		{name: "MKV H.265", container: "mkv", video: "h265", audio: "aac", mode: Remux},
		{name: "TS H.264", container: "ts", video: "h264", audio: "aac", mode: Remux},
		{name: "MPEG-TS HEVC", container: "mpegts", video: "hevc", audio: "aac", mode: Remux},
		{name: "M2TS H.264", container: "m2ts", video: "h264", audio: "aac", mode: Remux},
		{name: "MTS H.264", container: "mts", video: "h264", audio: "aac", mode: Remux},
		{name: "M2T H.264", container: "m2t", video: "h264", audio: "aac", mode: Remux},
		{name: "MPEG H.264", container: "mpeg", video: "h264", audio: "aac", mode: Remux},

		{name: "AAC copy", container: "mkv", video: "h264", audio: "aac", mode: Remux},
		{name: "AC-3 copy", container: "mkv", video: "h264", audio: "ac3", mode: Remux},
		{name: "E-AC-3 copy", container: "mkv", video: "h264", audio: "eac3", mode: Remux},
		{name: "DTS transcode", container: "mkv", video: "h264", audio: "dts", mode: Remux, audioTranscode: true},
		{name: "TrueHD transcode", container: "mkv", video: "h264", audio: "truehd", mode: Remux, audioTranscode: true},
		{name: "FLAC transcode", container: "mkv", video: "h264", audio: "flac", mode: Remux, audioTranscode: true},
		{name: "Opus transcode", container: "mkv", video: "h264", audio: "opus", mode: Remux, audioTranscode: true},
		{name: "Vorbis transcode", container: "mkv", video: "h264", audio: "vorbis", mode: Remux, audioTranscode: true},
		{name: "MP3 transcode", container: "mkv", video: "h264", audio: "mp3", mode: Remux, audioTranscode: true},
		{name: "MP2 transcode", container: "mkv", video: "h264", audio: "mp2", mode: Remux, audioTranscode: true},
		{name: "PCM transcode", container: "mkv", video: "h264", audio: "pcm", mode: Remux, audioTranscode: true},
		{name: "Blu-ray PCM transcode", container: "m2ts", video: "h264", audio: "pcm_bluray", mode: Remux, audioTranscode: true},
		{name: "AAC-LATM transcode", container: "ts", video: "h264", audio: "aac_latm", mode: Remux, audioTranscode: true},

		{name: "AV1", container: "mkv", video: "av1", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},
		{name: "VP9", container: "mkv", video: "vp9", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},
		{name: "VP8", container: "mkv", video: "vp8", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},
		{name: "MPEG-2", container: "ts", video: "mpeg2video", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},
		{name: "MPEG-4 Part 2", container: "mkv", video: "mpeg4", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},
		{name: "VC-1", container: "mkv", video: "vc1", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedVideo},

		{name: "Blu-ray ISO", container: "blurayiso", video: "hevc", audio: "truehd", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "ISO", container: "iso", video: "hevc", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "DVD ISO", container: "dvdiso", video: "h264", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "Blu-ray folder", container: "bluray", video: "hevc", audio: "truehd", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "DVD folder", container: "dvd", video: "mpeg2video", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "VOB", container: "vob", video: "mpeg2video", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "AVI", container: "avi", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "WMV", container: "wmv", video: "vc1", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "ASF", container: "asf", video: "vc1", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "RealMedia", container: "rm", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "RealMedia VBR", container: "rmvb", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "FLV", container: "flv", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "3GP", container: "3gp", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "Ogg", container: "ogg", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "Ogg Video", container: "ogv", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "DivX", container: "divx", video: "mpeg4", audio: "ac3", mode: Unsupported, wantErr: ErrUnsupportedFormat},
		{name: "STRM", container: "strm", video: "h264", audio: "aac", mode: Unsupported, wantErr: ErrUnsupportedFormat},
	}

	t.Log("| Current | Container | Video | Audio | Result |")
	t.Log("|---|---|---|---|---|")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := emby.MediaSource{
				Container: tc.container,
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: tc.video, CodecTag: tc.videoTag, IsDefault: true},
					{Index: 1, Type: emby.StreamAudio, Codec: tc.audio, IsDefault: true},
				},
			}
			got := Decide(&source, -1)
			result := got.Mode.String()
			if got.AudioTranscode {
				result += " + local audio transcode"
			}
			t.Logf("| %s | %s | %s | %s | %s |", tc.name, tc.container, tc.video, tc.audio, result)
			if got.Mode != tc.mode {
				t.Fatalf("mode = %s, want %s (%s)", got.Mode, tc.mode, got.Reason)
			}
			if got.AudioTranscode != tc.audioTranscode {
				t.Fatalf("audio transcode = %v, want %v (%s)", got.AudioTranscode, tc.audioTranscode, got.Reason)
			}
			if tc.wantErr == nil {
				if got.Err != nil {
					t.Fatalf("unexpected cause: %v", got.Err)
				}
				return
			}
			if !errors.Is(got.Err, tc.wantErr) {
				t.Fatalf("cause = %v, want %v", got.Err, tc.wantErr)
			}
		})
	}
}

func TestSubtitleCompatibilityMatrix(t *testing.T) {
	cases := []struct {
		codec    string
		extracts bool
	}{
		{codec: "subrip", extracts: true},
		{codec: "ass", extracts: true},
		{codec: "ssa", extracts: true},
		{codec: "webvtt", extracts: true},
		{codec: "mov_text", extracts: true},
		{codec: "hdmv_pgs_subtitle"},
		{codec: "dvd_subtitle"},
		{codec: "dvb_subtitle"},
		{codec: "eia_608"},
	}
	t.Log("| Subtitle codec | Local text extraction |")
	t.Log("|---|---|")
	for _, tc := range cases {
		t.Run(tc.codec, func(t *testing.T) {
			got := isTextSubtitle(tc.codec)
			t.Logf("| %s | %v |", tc.codec, got)
			if got != tc.extracts {
				t.Fatalf("isTextSubtitle(%q) = %v, want %v", tc.codec, got, tc.extracts)
			}
		})
	}
}
