package player

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/remux"
)

func TestLiveGoodPlaceMediaProbe(t *testing.T) {
	server, user, token := os.Getenv("MYGO_LIVE_SERVER"), os.Getenv("MYGO_LIVE_USER"), os.Getenv("MYGO_LIVE_TOKEN")
	if server == "" || user == "" || token == "" {
		t.Skip("set MYGO_LIVE_SERVER, MYGO_LIVE_USER and MYGO_LIVE_TOKEN for the live media probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := emby.RestoreClient(emby.Session{
		ServerURL: server, UserID: user, Token: token, UserName: user,
	}, emby.DeviceInfo{Client: "MyGo Emby probe", Device: "Mac", DeviceID: "media-compat-probe", Version: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	results, err := client.Search(ctx, "善地", 6)
	if err != nil {
		t.Fatal(err)
	}
	var items []emby.Item
	items = append(items, results.Movies...)
	items = append(items, results.Episodes...)
	for _, series := range results.Series {
		seasons, err := client.Seasons(ctx, series.ID)
		if err != nil || len(seasons) == 0 {
			continue
		}
		episodes, err := client.Episodes(ctx, series.ID, seasons[0].ID)
		if err == nil && len(episodes) > 0 {
			items = append(items, episodes[0])
		}
	}
	if len(items) == 0 {
		t.Fatal("search returned no playable item")
	}
	for i, item := range items {
		if i >= 4 {
			break
		}
		t.Run(item.ID, func(t *testing.T) {
			p, err := client.PlaybackInfo(ctx, item.ID, emby.PlaybackOptions{SubtitleStreamIndex: emby.Index(-1)})
			if err != nil {
				t.Logf("PlaybackInfo skipped: %v", err)
				return
			}
			t.Logf("item %q type=%s method=%s selected-source=%s", item.DisplayTitle(), item.Type, p.Method, p.MediaSourceID)
			for _, source := range p.MediaSources {
				var streams []string
				for _, stream := range source.MediaStreams {
					streams = append(streams, fmt.Sprintf("%s:%s(tag=%s,default=%v)",
						stream.Type, stream.Codec, stream.CodecTag, stream.IsDefault))
				}
				t.Logf("source id=%s name=%q container=%s direct=%v transcode=%v streams=[%s]",
					source.ID, source.Name, source.Container, source.SupportsDirectStream,
					source.SupportsTranscoding, strings.Join(streams, ", "))
			}
			if p.StreamURL == "" {
				t.Fatal("no stream URL")
			}
			probeBytes := compatRangeBytes(t, ctx, p.StreamURL, token)
			probe := compatFFprobe(t)
			cmd := exec.Command(probe, "-v", "error", "-probesize", "4194304",
				"-analyzeduration", "1000000", "-show_entries",
				"format=format_name,start_time:stream=index,codec_name,codec_type,profile,codec_tag_string,pix_fmt,channels,sample_rate,start_time",
				"-of", "json", "pipe:0")
			cmd.Stdin = bytes.NewReader(probeBytes)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("ffprobe over %d range bytes: %v: %s", len(probeBytes), err, out)
			}
			t.Logf("actual file probe: %s", out)
			if os.Getenv("MYGO_LIVE_REMUX") == "1" {
				sourceFile := filepath.Join(t.TempDir(), "source-probe.mkv")
				if err := os.WriteFile(sourceFile, probeBytes, 0o600); err != nil {
					t.Fatal(err)
				}
				sourceProbe := compatProbe(t, sourceFile)
				srv, err := remux.NewServer(remux.WithLogf(t.Logf))
				if err != nil {
					t.Fatal(err)
				}
				defer srv.Close()
				stream, err := RemuxFallback(srv)(ctx, p, Request{})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				assembled := compatFetchHLSSegment(t, stream.URL, "0")
				file := filepath.Join(t.TempDir(), "good-place-segment.mp4")
				if err := os.WriteFile(file, assembled, 0o600); err != nil {
					t.Fatal(err)
				}
				outputProbe := compatProbe(t, file)
				t.Logf("live remux output probe: %+v", outputProbe)
				pcm := compatDecodeAudio(t, file)
				if len(pcm) == 0 {
					t.Fatal("live remux segment decoded no audio")
				}
				sourceOrigin, sourceAudio := compatStreamOrigin(sourceProbe)
				outputOrigin, outputAudio := compatStreamOrigin(outputProbe)
				if err := compatAbs((sourceAudio - sourceOrigin) - (outputAudio - outputOrigin)); err > 100*time.Millisecond {
					t.Fatalf("live audio origin moved by %v", err)
				}
			}
		})
	}
}

func compatStreamOrigin(probe compatProbeOutput) (time.Duration, time.Duration) {
	var origin, audio time.Duration
	found := false
	for _, stream := range probe.Streams {
		start := compatSeconds(stream.Start)
		if !found || start < origin {
			origin, found = start, true
		}
		if stream.CodecType == "audio" {
			audio = start
		}
	}
	return origin, audio
}

func compatSeconds(value string) time.Duration {
	seconds, _ := strconv.ParseFloat(value, 64)
	return time.Duration(seconds * float64(time.Second))
}

func compatAbs(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

func compatRangeBytes(t *testing.T, ctx context.Context, rawURL, token string) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-4194303")
	req.Header.Set("X-Emby-Token", token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("range probe: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Fatal(err)
	}
	magic := b
	if len(magic) > 16 {
		magic = magic[:16]
	}
	t.Logf("range response status=%s type=%q content-range=%q bytes=%d magic=%x",
		resp.Status, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Range"), len(b), magic)
	return b
}

func TestServerSourceFallsBackToServerTranscodeAfterLocalFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			EnableDirectPlay bool `json:"EnableDirectPlay"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		source := `{
			"Id":"ms","Container":"mkv","SupportsDirectPlay":true,
			"SupportsDirectStream":true,"SupportsTranscoding":true,
			"DirectStreamUrl":"/Videos/item/stream?Static=true&api_key=test",
			"RunTimeTicks":600000000,"MediaStreams":[
				{"Index":0,"Type":"Video","Codec":"hevc","CodecTag":"hvc1","BitDepth":10},
				{"Index":1,"Type":"Audio","Codec":"dts","IsDefault":true}
			]
		}`
		if body.EnableDirectPlay {
			fmt.Fprintf(w, `{"MediaSources":[%s],"PlaySessionId":"ps"}`, source)
			return
		}
		fmt.Fprintf(w, `{"MediaSources":[{
			"Id":"ms","Container":"mkv","SupportsDirectPlay":false,
			"SupportsDirectStream":false,"SupportsTranscoding":true,
			"TranscodingUrl":"/videos/item/master.m3u8?api_key=test",
			"TranscodingSubProtocol":"hls","RunTimeTicks":600000000,
			"MediaStreams":[
				{"Index":0,"Type":"Video","Codec":"hevc","CodecTag":"hvc1","BitDepth":10},
				{"Index":1,"Type":"Audio","Codec":"dts","IsDefault":true}
			]
		}],"PlaySessionId":"ps"}`)
	}))
	defer srv.Close()

	client, err := emby.RestoreClient(emby.Session{
		ServerURL: srv.URL, UserID: "user", Token: "token",
	}, emby.DeviceInfo{Client: "test", Device: "test", DeviceID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	localErr := &UnplayableError{Reason: "local remux failed"}
	fallbackCalls := 0
	src := &ServerSource{
		Client: client,
		Fallback: func(context.Context, *emby.Playback, Request) (*Stream, error) {
			fallbackCalls++
			return nil, localErr
		},
	}
	stream, err := src.Open(context.Background(), Request{Item: &emby.Item{ID: "item"}})
	if err != nil {
		t.Fatalf("Open after local fallback failed: %v", err)
	}
	if fallbackCalls != 1 || calls != 2 {
		t.Fatalf("fallback calls %d, PlaybackInfo calls %d; want 1 and 2", fallbackCalls, calls)
	}
	if stream.Method != emby.Transcode || !stream.Playlist || !stream.ServerWork ||
		!strings.Contains(stream.URL, "master.m3u8") {
		t.Fatalf("stream %+v, want the server HLS transcode", stream)
	}
}

func TestRemuxFallbackKeepsDTSAudioWithVideoCopy(t *testing.T) {
	requireMediaFFmpeg(t)
	mediaDir := filepath.Join("..", "remux", "testdata")
	files := httptest.NewServer(http.FileServer(http.Dir(mediaDir)))
	defer files.Close()

	srv, err := remux.NewServer(remux.WithLogf(t.Logf))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	p := &emby.Playback{
		ItemID: "good-place", Method: emby.DirectPlay,
		StreamURL:    files.URL + "/mkv_hevc_dts.mkv",
		RunTimeTicks: emby.DurationToTicks(10 * time.Second),
		Source: emby.MediaSource{
			ID: "ms", Container: "mkv", RunTimeTicks: emby.DurationToTicks(10 * time.Second),
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "hevc", CodecTag: "hvc1", BitDepth: 10},
				{Index: 1, Type: emby.StreamAudio, Codec: "dts", IsDefault: true},
			},
		},
	}
	stream, err := RemuxFallback(srv)(context.Background(), p, Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if stream.AudioIndex != 1 {
		t.Fatalf("audio index %d, want 1", stream.AudioIndex)
	}

	assembled := compatFetchHLS(t, stream.URL)
	out := filepath.Join(t.TempDir(), "output.mp4")
	if err := os.WriteFile(out, assembled, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := compatProbe(t, out)
	if len(probe.Streams) != 2 {
		t.Fatalf("output streams: %+v", probe.Streams)
	}
	var video, audio *compatProbeStream
	for i := range probe.Streams {
		switch probe.Streams[i].CodecType {
		case "video":
			video = &probe.Streams[i]
		case "audio":
			audio = &probe.Streams[i]
		}
	}
	if video == nil || video.CodecName != "hevc" || video.Profile != "Main 10" {
		t.Fatalf("video stream was not copied as HEVC Main 10: %+v", video)
	}
	if audio == nil || audio.CodecName != "aac" || audio.Channels != 2 {
		t.Fatalf("audio stream is not playable stereo AAC: %+v", audio)
	}
	cmd := exec.Command(compatFFmpeg(t), "-v", "error", "-i", out, "-map", "0:a:0", "-f", "null", "-")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg could not decode output audio: %v\n%s", err, b)
	}
	pcm := compatDecodeAudio(t, out)
	const channels = 2
	const rate = 48000
	frames := len(pcm) / 4 / channels
	if frames < 9*rate || frames > 11*rate {
		t.Fatalf("decoded audio has %d samples, want about %d", frames, 10*rate)
	}
	for _, boundary := range []int{2 * rate, 6 * rate} {
		if compatMaxPCM(pcm, boundary, 1024) < 0.01 {
			t.Fatalf("encoder delay or a silent gap at audio sample %d", boundary)
		}
		t.Logf("audio boundary sample %d: window peak %.4f, adjacent step %.4f",
			boundary, compatMaxPCM(pcm, boundary-512, 1024),
			compatMaxPCMStep(pcm, boundary-512, 1024))
		if step := compatMaxPCMStep(pcm, boundary-512, 1024); step > 0.05 {
			t.Fatalf("audio splice at sample %d has a %.4f discontinuity", boundary, step)
		}
	}

	seekStream, err := RemuxFallback(srv)(context.Background(), p, Request{
		Start: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	seekOut := filepath.Join(t.TempDir(), "seek.mp4")
	if err := os.WriteFile(seekOut, compatFetchHLSSegment(t, seekStream.URL, "1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if seekStream.Close != nil {
		seekStream.Close()
	}
	seekPCM := compatDecodeAudio(t, seekOut)
	seekFrames := len(seekPCM) / 4 / channels
	if seekFrames < 4*rate-2*1024 || seekFrames > 4*rate+2*1024 {
		t.Fatalf("seek segment decoded %d samples, want about %d", seekFrames, 4*rate)
	}
	if compatMaxPCM(seekPCM, 0, 1024) < 0.01 {
		t.Fatal("seek segment begins with encoder priming or silent audio")
	}
}

func TestRemuxFallbackExplainsMissingAudioTranscoder(t *testing.T) {
	srv, err := remux.NewServer(remux.WithFFmpeg(""))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	files := httptest.NewServer(http.FileServer(http.Dir(filepath.Join("..", "remux", "testdata"))))
	defer files.Close()
	p := &emby.Playback{
		StreamURL: files.URL + "/mkv_hevc_dts.mkv",
		Source: emby.MediaSource{
			Container: "mkv",
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "h264"},
				{Index: 1, Type: emby.StreamAudio, Codec: "dts", IsDefault: true},
			},
		},
	}
	_, err = RemuxFallback(srv)(context.Background(), p, Request{})
	var unplayable *UnplayableError
	if !errors.As(err, &unplayable) || unplayable.Reason != str.ErrAudioTranscoder {
		t.Fatalf("missing FFmpeg error = %v", err)
	}
}

func compatDecodeAudio(t *testing.T, file string) []byte {
	t.Helper()
	cmd := exec.Command(compatFFmpeg(t), "-v", "error", "-i", file, "-map", "0:a:0",
		"-ac", "2", "-ar", "48000", "-f", "f32le", "pipe:1")
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("decode AAC to PCM: %v", err)
	}
	return b
}

func compatMaxPCM(pcm []byte, frame, count int) float64 {
	var maximum float64
	for i := frame; i < frame+count; i++ {
		for ch := 0; ch < 2; ch++ {
			off := (i*2 + ch) * 4
			if off+4 > len(pcm) {
				return maximum
			}
			v := math.Abs(float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[off:]))))
			maximum = max(maximum, v)
		}
	}
	return maximum
}

func compatMaxPCMStep(pcm []byte, frame, count int) float64 {
	var maximum float64
	for i := frame + 1; i < frame+count; i++ {
		for ch := 0; ch < 2; ch++ {
			a := (i*2 + ch) * 4
			b := ((i-1)*2 + ch) * 4
			if b+4 > len(pcm) {
				return maximum
			}
			av := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[a:])))
			bv := float64(math.Float32frombits(binary.LittleEndian.Uint32(pcm[b:])))
			maximum = max(maximum, math.Abs(av-bv))
		}
	}
	return maximum
}

func compatFetchHLS(t *testing.T, playlist string) []byte {
	t.Helper()
	base := strings.TrimSuffix(playlist, "master.m3u8")
	media := string(compatGet(t, base+"media.m3u8"))
	re := regexp.MustCompile(`(?m)^([0-9]+)\.m4s$`)
	matches := re.FindAllStringSubmatch(media, -1)
	if len(matches) == 0 {
		t.Fatalf("media playlist has no segments:\n%s", media)
	}
	out := compatGet(t, base+"init.mp4")
	for _, match := range matches {
		out = append(out, compatGet(t, base+match[1]+".m4s")...)
	}
	return out
}

func compatFetchHLSSegment(t *testing.T, playlist, segment string) []byte {
	t.Helper()
	base := strings.TrimSuffix(playlist, "master.m3u8")
	out := compatGet(t, base+"init.mp4")
	return append(out, compatGet(t, base+segment+".m4s")...)
}

func compatGet(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %s", url, resp.Status)
	}
	return b
}

type compatProbeStream struct {
	CodecName string `json:"codec_name"`
	CodecType string `json:"codec_type"`
	Profile   string `json:"profile"`
	Channels  int    `json:"channels"`
	Start     string `json:"start_time"`
	Duration  string `json:"duration"`
}

type compatProbeOutput struct {
	Streams []compatProbeStream `json:"streams"`
}

func compatProbe(t *testing.T, file string) compatProbeOutput {
	t.Helper()
	cmd := exec.Command(compatFFprobe(t), "-v", "error", "-show_entries",
		"stream=codec_name,codec_type,profile,channels,start_time,duration", "-of", "json", file)
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	var out compatProbeOutput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func compatFFprobe(t *testing.T) string {
	t.Helper()
	probe := strings.TrimSuffix(compatFFmpeg(t), "ffmpeg") + "ffprobe"
	if _, err := exec.LookPath(probe); err != nil {
		t.Skip("ffprobe is not installed; media compatibility test skipped")
	}
	return probe
}

func requireMediaFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath(compatFFmpeg(t)); err != nil {
		t.Skip("ffmpeg is not installed; media compatibility test skipped")
	}
}

func compatFFmpeg(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"ffmpeg", "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("ffmpeg is not installed; media compatibility test skipped")
	return ""
}

func TestServerSourcePreservesLocalErrorWhenServerTranscodeAlsoFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EnableDirectPlay bool `json:"EnableDirectPlay"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.EnableDirectPlay {
			fmt.Fprint(w, `{"MediaSources":[{
				"Id":"ms","Container":"mkv","SupportsDirectPlay":true,
				"SupportsDirectStream":true,"SupportsTranscoding":true,
				"DirectStreamUrl":"/Videos/item/stream?Static=true&api_key=test",
				"MediaStreams":[
					{"Index":0,"Type":"Video","Codec":"hevc","CodecTag":"hvc1"},
					{"Index":1,"Type":"Audio","Codec":"dts","IsDefault":true}
				]
			}]}`)
			return
		}
		http.Error(w, "transcoder down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	client, err := emby.RestoreClient(emby.Session{
		ServerURL: srv.URL, UserID: "user", Token: "token",
	}, emby.DeviceInfo{Client: "test", Device: "test", DeviceID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	localErr := errors.New("local remux failed")
	src := &ServerSource{
		Client: client,
		Fallback: func(context.Context, *emby.Playback, Request) (*Stream, error) {
			return nil, localErr
		},
	}
	_, err = src.Open(context.Background(), Request{Item: &emby.Item{ID: "item"}})
	if err == nil {
		t.Fatal("Open succeeded")
	}
	if !strings.Contains(err.Error(), "local remux failed") ||
		!strings.Contains(err.Error(), "server transcode") {
		t.Fatalf("error lost a useful failure: %v", err)
	}
}
