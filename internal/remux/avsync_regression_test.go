//go:build darwin || linux

package remux

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

type avEventPair struct {
	video, audio time.Duration
}

func (p avEventPair) delta() time.Duration { return p.video - p.audio }

type avTiming struct {
	start, duration time.Duration
}

func TestTranscodedAudioPreservesAVEvents(t *testing.T) {
	ffmpeg, ffprobe := avFFmpeg(t)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "av-sync.mkv")
	avGenerateSource(t, ffmpeg, source)

	files := httptest.NewServer(http.FileServer(http.Dir(sourceDir)))
	defer files.Close()
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	startingCues := make(chan emby.Cue, 32)
	session, err := srv.Open(context.Background(), Source{
		URL:            files.URL + "/av-sync.mkv",
		AudioIndex:     -1,
		AudioTranscode: true,
		Duration:       72 * time.Second,
		OnCues: func(_ int, cues []emby.Cue) {
			for _, cue := range cues {
				startingCues <- cue
			}
		},
		MediaSource: &emby.MediaSource{
			Container: "mkv",
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "h264"},
				{Index: 1, Type: emby.StreamAudio, Codec: "dts", IsDefault: true},
				{Index: 2, Type: emby.StreamSubtitle, Codec: "subrip"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	sourceVideoStart, sourceAudioStart := avStreamStarts(t, ffprobe, source)
	sourceDuration := avAssertMediaDuration(t, ffprobe, session, source, sourceVideoStart, sourceAudioStart)
	sourceEvents := avMeasureEvents(t, ffmpeg, ffprobe, source, min(sourceVideoStart, sourceAudioStart))
	t.Logf("source events: %+v", sourceEvents)
	if len(sourceEvents) < 4 {
		t.Fatalf("source has only %d visual/audio event pairs: %+v", len(sourceEvents), sourceEvents)
	}

	base := strings.TrimSuffix(session.PlaylistURL, "master.m3u8")
	init := get(t, base+"init.mp4")
	all := append([]byte(nil), init...)
	for i := range session.segs {
		segment := get(t, fmt.Sprintf("%s%d.m4s", base, i))
		if i == 0 {
			for _, run := range parseSegment(t, segment) {
				if run.track == 1 && len(run.ctos) > 0 {
					t.Logf("first video fragment: base=%d durs=%v ctos=%v sync=%v", run.base, run.durs[:12], run.ctos[:12], run.sync[:12])
					break
				}
			}
		}
		all = append(all, segment...)
	}
	output := filepath.Join(t.TempDir(), "output.mp4")
	if err := os.WriteFile(output, all, 0o600); err != nil {
		t.Fatal(err)
	}
	avDump(t, source, output)
	outputEvents := avMeasureEvents(t, ffmpeg, ffprobe, output, 0)
	t.Logf("continuous output events: %+v", outputEvents)
	avCompareEvents(t, "continuous", sourceEvents, outputEvents, 0, sourceDuration)
	avAssertNormalizedCues(t, "continuous", startingCues, []time.Duration{2200 * time.Millisecond, 32600 * time.Millisecond, 62700 * time.Millisecond})

	for _, target := range []time.Duration{30 * time.Second, 60 * time.Second} {
		first := session.segmentAt(target)
		last := min(first+2, len(session.segs)-1)
		seekOutput := append([]byte(nil), init...)
		for i := first; i <= last; i++ {
			seekOutput = append(seekOutput, get(t, fmt.Sprintf("%s%d.m4s", base, i))...)
		}
		path := filepath.Join(t.TempDir(), fmt.Sprintf("seek-%d.mp4", target/time.Second))
		if err := os.WriteFile(path, seekOutput, 0o600); err != nil {
			t.Fatal(err)
		}
		avDump(t, path)
		seekEvents := avMeasureEvents(t, ffmpeg, ffprobe, path, 0)
		t.Logf("seek-%ds output events: %+v", target/time.Second, seekEvents)
		avCompareEvents(t, fmt.Sprintf("seek-%ds", target/time.Second), sourceEvents, seekEvents, target, target+12*time.Second)
	}

	// The final media segment must not inherit the source's non-zero timestamp
	// origin as a long silent/tail EXTINF. Exercise the actual last segment too.
	last := len(session.segs) - 1
	lastOutput := append([]byte(nil), init...)
	lastOutput = append(lastOutput, get(t, fmt.Sprintf("%s%d.m4s", base, last))...)
	lastPath := filepath.Join(t.TempDir(), "last-segment.mp4")
	if err := os.WriteFile(lastPath, lastOutput, 0o600); err != nil {
		t.Fatal(err)
	}
	avDump(t, lastPath)
	lastEvents := avMeasureEvents(t, ffmpeg, ffprobe, lastPath, 0)
	t.Logf("last segment %d (%v-%v) events: %+v", last, session.segs[last].start, session.segs[last].end, lastEvents)
	avCompareEvents(t, "last-segment", sourceEvents, lastEvents, session.segs[last].start, sourceDuration)

	seekCues := make(chan emby.Cue, 8)
	seekSession, err := srv.Open(context.Background(), Source{
		URL: files.URL + "/av-sync.mkv", AudioIndex: -1, AudioTranscode: true, Start: 30 * time.Second,
		OnCues: func(_ int, cues []emby.Cue) {
			for _, cue := range cues {
				seekCues <- cue
			}
		},
		MediaSource: &emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{
			{Index: 0, Type: emby.StreamVideo, Codec: "h264"},
			{Index: 1, Type: emby.StreamAudio, Codec: "dts", IsDefault: true},
			{Index: 2, Type: emby.StreamSubtitle, Codec: "subrip"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer seekSession.Close()
	seekIndex := seekSession.segmentAt(30 * time.Second)
	_ = get(t, fmt.Sprintf("%s%d.m4s", strings.TrimSuffix(seekSession.PlaylistURL, "master.m3u8"), seekIndex))
	avAssertNormalizedCues(t, "seek-30s", seekCues, []time.Duration{32600 * time.Millisecond})
}

func TestTranscodedAudioPreservesStreamOrigins(t *testing.T) {
	ffmpeg, ffprobe := avFFmpeg(t)
	cases := []struct {
		name, videoOffset, audioOffset, codec string
	}{
		{name: "zero-origin", videoOffset: "0", audioOffset: "0", codec: "dts"},
		{name: "audio-late", videoOffset: "0", audioOffset: "1.0", codec: "dts"},
		{name: "video-late", videoOffset: "1.0", audioOffset: "0", codec: "dts"},
		{name: "negative-opus-origin", videoOffset: "0", audioOffset: "0", codec: "libopus"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.mkv")
			avGenerateVariant(t, ffmpeg, source, tc.videoOffset, tc.audioOffset, tc.codec)
			files := httptest.NewServer(http.FileServer(http.Dir(dir)))
			defer files.Close()
			srv, err := NewServer()
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			session, err := srv.Open(context.Background(), Source{
				URL: files.URL + "/source.mkv", AudioIndex: -1, AudioTranscode: true,
				MediaSource: &emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "h264"},
					{Index: 1, Type: emby.StreamAudio, Codec: tc.codec, IsDefault: true},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			videoStart, audioStart := avStreamStarts(t, ffprobe, source)
			avAssertMediaDuration(t, ffprobe, session, source, videoStart, audioStart)
			base := strings.TrimSuffix(session.PlaylistURL, "master.m3u8")
			output := filepath.Join(dir, "output.mp4")
			data := get(t, base+"init.mp4")
			for i := range session.segs {
				data = append(data, get(t, fmt.Sprintf("%s%d.m4s", base, i))...)
			}
			if err := os.WriteFile(output, data, 0o600); err != nil {
				t.Fatal(err)
			}
			want := avMeasureEvents(t, ffmpeg, ffprobe, source, min(videoStart, audioStart))
			got := avMeasureEvents(t, ffmpeg, ffprobe, output, 0)
			avCompareEvents(t, tc.name, want, got, 0, 9*time.Second)
		})
	}
}

func avGenerateVariant(t *testing.T, ffmpeg, path, videoOffset, audioOffset, codec string) {
	t.Helper()
	video := "color=c=black:s=160x90:r=24:d=10," +
		"drawbox=x=0:y=0:w=iw:h=ih:color=white:t=fill:enable='between(t,2.1,2.2)'"
	audio := "between(t,2.1,2.2)*0.8*sin(2*PI*1000*t)"
	args := []string{"-v", "error", "-y"}
	if videoOffset != "" {
		args = append(args, "-itsoffset", videoOffset)
	}
	args = append(args, "-f", "lavfi", "-i", video)
	if audioOffset != "" {
		args = append(args, "-itsoffset", audioOffset)
	}
	args = append(args, "-f", "lavfi", "-i", "aevalsrc=exprs='"+audio+"':s=48000:d=10",
		"-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-preset", "ultrafast",
		"-crf", "38", "-pix_fmt", "yuv420p", "-g", "120", "-keyint_min", "120",
		"-sc_threshold", "0", "-bf", "3")
	if codec == "dts" {
		args = append(args, "-c:a", "dts", "-strict", "-2", "-b:a", "384k", "-ac", "2")
	} else {
		args = append(args, "-c:a", codec, "-b:a", "64k", "-ac", "2")
	}
	args = append(args, "-f", "matroska", path)
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v\n%s", codec, err, out)
	}
}

func avDump(t *testing.T, files ...string) {
	dir := os.Getenv("AVSYNC_DUMP")
	if dir == "" {
		return
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(file)), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func avGenerateSource(t *testing.T, ffmpeg, path string) {
	t.Helper()
	subtitlePath := filepath.Join(filepath.Dir(path), "av-sync.srt")
	subtitles := "1\n00:00:02,200 --> 00:00:02,500\nfirst\n\n" +
		"2\n00:00:32,600 --> 00:00:32,900\nsecond\n\n" +
		"3\n00:00:62,700 --> 00:00:63,000\nlast\n"
	if err := os.WriteFile(subtitlePath, []byte(subtitles), 0o600); err != nil {
		t.Fatal(err)
	}
	video := "color=c=black:s=320x180:r=24:d=72," +
		"drawbox=x=0:y=0:w=iw:h=ih:color=white:t=fill:" +
		"enable='between(t,2.100,2.200)+between(t,12.300,12.400)+between(t,32.500,32.600)+between(t,62.700,62.800)+between(t,70.700,70.800)'"
	audio := "(between(t,2.100,2.200)+between(t,12.300,12.400)+" +
		"between(t,32.500,32.600)+between(t,62.700,62.800)+between(t,70.700,70.800))*0.8*sin(2*PI*1000*t)"
	cmd := exec.Command(ffmpeg, "-v", "error", "-y",
		"-itsoffset", "1.234", "-f", "lavfi", "-i", video,
		"-itsoffset", "1.234", "-f", "lavfi", "-i", "aevalsrc=exprs='"+audio+"':s=48000:d=72",
		"-itsoffset", "1.234", "-f", "srt", "-i", subtitlePath,
		"-map", "0:v:0", "-map", "1:a:0", "-map", "2:s:0",
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "38",
		"-pix_fmt", "yuv420p", "-g", "240", "-keyint_min", "240", "-sc_threshold", "0", "-bf", "3",
		"-c:a", "dts", "-strict", "-2", "-b:a", "384k", "-ac", "2",
		"-c:s", "srt",
		"-f", "matroska", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate A/V sync source: %v\n%s", err, out)
	}
}

func avAssertNormalizedCues(t *testing.T, label string, cues <-chan emby.Cue, starts []time.Duration) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for _, want := range starts {
		select {
		case cue := <-cues:
			if delta := absDuration(cue.Start - want); delta > 5*time.Millisecond {
				t.Errorf("%s cue %q starts at %v, want %v (error %v); a non-zero media origin was applied twice",
					label, cue.Text, cue.Start, want, delta)
			} else {
				t.Logf("%s cue %q starts at %v", label, cue.Text, cue.Start)
			}
		case <-timer.C:
			t.Fatalf("%s: timed out waiting for cue at %v", label, want)
		}
	}
}

// avAssertMediaDuration checks both Session.Duration and the sum of EXTINF
// values against the actual content length (the container timeline minus its
// earliest stream timestamp). This catches a non-zero Matroska origin being
// left in the playlist even when early A/V events happen to line up.
func avAssertMediaDuration(t *testing.T, ffprobe string, session *Session, source string, videoStart, audioStart time.Duration) time.Duration {
	t.Helper()
	timing := avFormatTiming(t, ffprobe, source)
	content := timing.duration - min(videoStart, audioStart)
	if content <= 0 {
		t.Fatalf("%s: invalid inferred content duration %v (container %v, first media %v)",
			source, content, timing.duration, min(videoStart, audioStart))
	}
	if delta := absDuration(session.Duration - content); delta > 10*time.Millisecond {
		t.Errorf("session duration %v, want content duration %v from container duration %v minus non-zero origin %v (error %v)",
			session.Duration, content, timing.duration, min(videoStart, audioStart), delta)
	}
	base := strings.TrimSuffix(session.PlaylistURL, "master.m3u8")
	playlist := string(get(t, base+"media.m3u8"))
	var total time.Duration
	for _, line := range strings.Split(playlist, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "#EXTINF:")
		if !ok {
			continue
		}
		value, _, _ = strings.Cut(value, ",")
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil {
			t.Fatalf("parse EXTINF %q: %v", line, err)
		}
		total += time.Duration(seconds * float64(time.Second))
	}
	if delta := absDuration(total - content); delta > 2*time.Millisecond {
		t.Errorf("playlist duration %v, want content duration %v (error %v):\n%s", total, content, delta, playlist)
	}
	t.Logf("container duration=%v first-media=%v content=%v session=%v playlist=%v",
		timing.duration, min(videoStart, audioStart), content, session.Duration, total)
	return content
}

func avFormatTiming(t *testing.T, ffprobe, file string) avTiming {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=start_time,duration", "-of", "json", file)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe format timing: %v", err)
	}
	var doc struct {
		Format struct {
			Start    string `json:"start_time"`
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	return avTiming{start: avSeconds(doc.Format.Start), duration: avSeconds(doc.Format.Duration)}
}

func avMeasureEvents(t *testing.T, ffmpeg, ffprobe, file string, origin time.Duration) []avEventPair {
	t.Helper()
	videoStart, audioStart := avStreamStarts(t, ffprobe, file)
	flashes := avVideoEvents(t, ffmpeg, ffprobe, file, videoStart)
	pulses := avAudioEvents(t, ffmpeg, file, audioStart)
	t.Logf("%s flashes=%v pulses=%v starts video=%v audio=%v", file, flashes, pulses, videoStart, audioStart)
	if len(flashes) == 0 || len(pulses) == 0 {
		t.Fatalf("%s: no events, video=%v audio=%v", file, flashes, pulses)
	}
	var pairs []avEventPair
	for _, v := range flashes {
		v -= origin
		i := sort.Search(len(pulses), func(i int) bool { return pulses[i]-origin >= v-2*time.Second })
		if i >= len(pulses) || pulses[i]-origin > v+2*time.Second {
			continue
		}
		pairs = append(pairs, avEventPair{video: v, audio: pulses[i] - origin})
	}
	return pairs
}

func avStreamStarts(t *testing.T, ffprobe, file string) (time.Duration, time.Duration) {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries",
		"stream=index,codec_type,start_time", "-of", "json", file)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe streams: %v", err)
	}
	var doc struct {
		Streams []struct {
			Index     int    `json:"index"`
			CodecType string `json:"codec_type"`
			Start     string `json:"start_time"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	var video, audio time.Duration
	for _, stream := range doc.Streams {
		d := avSeconds(stream.Start)
		switch stream.CodecType {
		case "video":
			video = d
		case "audio":
			audio = d
		}
	}
	return video, audio
}

func avVideoEvents(t *testing.T, ffmpeg, ffprobe, file string, _ time.Duration) []time.Duration {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-map", "0:v:0",
		"-vf", "scale=1:1:flags=area,format=gray", "-vsync", "0", "-f", "rawvideo", "pipe:1")
	luma, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode video luma: %v", err)
	}
	cmd = exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_frames", "-show_entries", "frame=best_effort_timestamp_time", "-of", "json", file)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe video frames: %v", err)
	}
	var doc struct {
		Frames []struct {
			Time string `json:"best_effort_timestamp_time"`
		} `json:"frames"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	if len(luma) != len(doc.Frames) {
		t.Fatalf("%s: %d decoded video frames, %d timestamps", file, len(luma), len(doc.Frames))
	}
	var events []time.Duration
	flash := false
	for i, value := range luma {
		lit := value > 128
		if lit && !flash {
			events = append(events, avSeconds(doc.Frames[i].Time))
		}
		flash = lit
	}
	return events
}

func avAudioEvents(t *testing.T, ffmpeg, file string, start time.Duration) []time.Duration {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", file, "-map", "0:a:0",
		"-ac", "1", "-ar", "48000", "-f", "f32le", "pipe:1")
	pcm, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode audio PCM: %v", err)
	}
	var events []time.Duration
	active := false
	gap := 0
	const cooldown = 2400 // 50 ms at 48 kHz
	for i := 0; i+4 <= len(pcm); i += 4 {
		v := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[i:]))))
		if v > 0.1 {
			if !active && gap == 0 {
				events = append(events, start+time.Duration(i/4)*time.Second/48000)
				gap = cooldown
			}
			active = true
		} else {
			active = false
		}
		if gap > 0 {
			gap--
		}
	}
	return events
}

func avCompareEvents(t *testing.T, label string, source, output []avEventPair, from, to time.Duration) {
	t.Helper()
	var compared int
	for _, want := range source {
		if want.video < from || want.video > to {
			continue
		}
		var got *avEventPair
		for i := range output {
			if absDuration(output[i].video-want.video) < time.Second {
				got = &output[i]
				break
			}
		}
		if got == nil {
			t.Errorf("%s: output lost event video=%v audio=%v", label, want.video, want.audio)
			continue
		}
		deltaError := absDuration(got.delta() - want.delta())
		videoError := absDuration(got.video - want.video)
		audioError := absDuration(got.audio - want.audio)
		t.Logf("%s event source video=%v audio=%v delta=%v; output video=%v audio=%v delta=%v; errors video=%v audio=%v delta=%v",
			label, want.video, want.audio, want.delta(), got.video, got.audio, got.delta(), videoError, audioError, deltaError)
		if audioError > 30*time.Millisecond {
			t.Errorf("%s: audio event moved by %v", label, audioError)
		}
		if deltaError > 120*time.Millisecond {
			t.Errorf("%s: A/V event delta changed by %v", label, deltaError)
		}
		compared++
	}
	if compared == 0 {
		t.Errorf("%s: no events in comparison range", label)
	}
}

func avSeconds(s string) time.Duration {
	f, _ := strconv.ParseFloat(s, 64)
	return time.Duration(f * float64(time.Second))
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func avFFmpeg(t *testing.T) (ffmpeg, ffprobe string) {
	t.Helper()
	for _, name := range []string{"ffmpeg", "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if path, err := exec.LookPath(name); err == nil {
			probe := strings.TrimSuffix(path, "ffmpeg") + "ffprobe"
			if _, err := exec.LookPath(probe); err == nil {
				return path, probe
			}
		}
	}
	t.Skip("ffmpeg and ffprobe are required")
	return "", ""
}
