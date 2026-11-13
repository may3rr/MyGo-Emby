package emby

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Shapes below are trimmed from real responses: Emby 4.9 (an Emby-compatible
// cloud proxy and a classic Emby transcode answer) and Jellyfin 12.4.

const embyDirectJSON = `{
 "MediaSources":[{
  "Id":"ms_itm_29691e53","Name":"Movie.2018","Container":"mp4","Protocol":"Http",
  "SupportsDirectPlay":true,"SupportsDirectStream":true,"SupportsTranscoding":false,
  "DirectStreamUrl":"/Videos/itm_133a/stream?MediaSourceId=ms_itm_29691e53&api_key=servertoken",
  "DefaultAudioStreamIndex":1,"RunTimeTicks":65221440000,
  "MediaStreams":[
   {"Index":0,"Type":"Video","Codec":"h264","IsDefault":true},
   {"Index":1,"Type":"Audio","Codec":"aac","Language":"und","DisplayTitle":"AAC stereo (默认)","IsDefault":true,"Channels":2}
  ]},{
  "Id":"ms_itm_133a","Name":"Movie.2018 - 31.2G","Container":"matroska",
  "SupportsDirectPlay":true,"SupportsDirectStream":true,"SupportsTranscoding":false,
  "DirectStreamUrl":"/Videos/itm_133a/stream?MediaSourceId=ms_itm_133a&api_key=servertoken",
  "DefaultAudioStreamIndex":2,"DefaultSubtitleStreamIndex":8,
  "MediaStreams":[
   {"Index":0,"Type":"Video","Codec":"h264"},
   {"Index":2,"Type":"Audio","Codec":"dts","DisplayTitle":"DTS 5.1(side)","IsDefault":true,"Channels":6},
   {"Index":5,"Type":"Audio","Codec":"ac3","Title":"东森台配","DisplayTitle":"AC3 stereo","Language":"chi"},
   {"Index":7,"Type":"Subtitle","Codec":"hdmv_pgs_subtitle","IsTextSubtitleStream":true},
   {"Index":8,"Type":"Subtitle","Codec":"ass","Title":"简日双语","DisplayTitle":"ASS","IsTextSubtitleStream":true,"IsDefault":true}
  ]}],
 "PlaySessionId":"ps:user:itm_133a"}`

const embyTranscodeJSON = `{
 "MediaSources":[{
  "Id":"mediasource_4242","Container":"mkv","Protocol":"File","RunTimeTicks":100,
  "SupportsDirectPlay":false,"SupportsDirectStream":false,"SupportsTranscoding":true,
  "TranscodingUrl":"/videos/4242/master.m3u8?DeviceId=dev-123&MediaSourceId=mediasource_4242&PlaySessionId=abc&api_key=old&VideoCodec=h264&AudioCodec=aac,ac3&TranscodeReasons=ContainerNotSupported",
  "TranscodingSubProtocol":"hls","TranscodingContainer":"ts",
  "DefaultAudioStreamIndex":1,"DefaultSubtitleStreamIndex":-1,
  "MediaStreams":[
   {"Index":0,"Type":"Video","Codec":"h264"},
   {"Index":1,"Type":"Audio","Codec":"aac","Language":"eng","DisplayTitle":"English AAC stereo (Default)","IsDefault":true},
   {"Index":2,"Type":"Subtitle","Codec":"subrip","Language":"eng","DisplayTitle":"English (SUBRIP)","IsTextSubtitleStream":true,"IsExternal":true,
    "DeliveryMethod":"External","DeliveryUrl":"/videos/4242/mediasource_4242/Subtitles/2/0/Stream.vtt?api_key=old"},
   {"Index":3,"Type":"Subtitle","Codec":"PGSSUB","Language":"chi","DisplayTitle":"Chinese (PGSSUB)","DeliveryMethod":"Encode","IsForced":true}
  ]}],
 "PlaySessionId":"abc"}`

const jellyfinDirectJSON = `{
 "MediaSources":[{
  "Protocol":"File","Id":"1056b7d34f0fa0b8ce635428e8b27f37","Path":"/media/Movies/Caminandes.mp4",
  "Container":"mov","ETag":"e1","RunTimeTicks":1000,
  "SupportsTranscoding":true,"SupportsDirectStream":true,"SupportsDirectPlay":true,
  "TranscodingSubProtocol":"http",
  "DefaultAudioStreamIndex":1,"DefaultSubtitleStreamIndex":-1,
  "MediaStreams":[
   {"Codec":"h264","Index":0,"Type":"Video","BitDepth":8},
   {"Codec":"aac","Index":1,"Type":"Audio","DisplayTitle":"English - AAC - Stereo - Default","Language":"eng","IsDefault":true,"Channels":2},
   {"Codec":"subrip","Index":2,"Type":"Subtitle","Language":"eng","DisplayTitle":"English - SUBRIP","IsTextSubtitleStream":true,
    "DeliveryMethod":"External","DeliveryUrl":"/Videos/1056b7d3-4f0f-a0b8-ce63-5428e8b27f37/1056b7d34f0fa0b8ce635428e8b27f37/Subtitles/2/0/Stream.vtt?ApiKey=jtok"}
  ]}],
 "PlaySessionId":"4045534b6df64e45a2fae194d20b1be3"}`

const jellyfinTranscodeJSON = `{
 "MediaSources":[{
  "Protocol":"File","Id":"788ae1cdf3816e3441e29dd3a96ddfe1","Container":"mov",
  "SupportsTranscoding":true,"SupportsDirectStream":false,"SupportsDirectPlay":false,
  "TranscodingUrl":"/videos/788ae1cd-f381-6e34-41e2-9dd3a96ddfe1/master.m3u8?DeviceId=dev-123&MediaSourceId=788ae1cdf3816e3441e29dd3a96ddfe1&VideoCodec=h264&AudioCodec=aac,ac3,eac3&PlaySessionId=585c&ApiKey=jtok&SegmentContainer=ts&TranscodeReasons=VideoCodecNotSupported,AudioCodecNotSupported",
  "TranscodingSubProtocol":"hls","TranscodingContainer":"ts",
  "DefaultAudioStreamIndex":2,"DefaultSubtitleStreamIndex":-1,
  "MediaStreams":[
   {"Index":0,"Type":"Data"},
   {"Codec":"mpeg4","Index":1,"Type":"Video"},
   {"Codec":"mp3","Index":2,"Type":"Audio","Language":"eng","IsDefault":true}
  ]}],
 "PlaySessionId":"585c"}`

func resolve(t *testing.T, base, token, body string, opts PlaybackOptions) *Playback {
	t.Helper()
	c, err := RestoreClient(Session{ServerURL: base, UserID: "u", Token: token}, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	var resp PlaybackInfoResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	p, err := c.resolvePlayback("item", &resp, opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func mustQuery(t *testing.T, raw string) (string, url.Values) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Scheme + "://" + u.Host + u.Path, u.Query()
}

func TestResolveEmbyDirect(t *testing.T) {
	p := resolve(t, "https://emby.example.com/emby", "tok", embyDirectJSON, PlaybackOptions{})
	if p.Method != DirectPlay || !p.NativelyPlayable || p.IsHLS {
		t.Errorf("method %v native %v hls %v", p.Method, p.NativelyPlayable, p.IsHLS)
	}
	path, q := mustQuery(t, p.StreamURL)
	if path != "https://emby.example.com/emby/Videos/itm_133a/stream" {
		t.Errorf("stream path %q", path)
	}
	if q.Get("api_key") != "tok" || q.Get("ApiKey") != "tok" || len(q["api_key"]) != 1 || q.Get("MediaSourceId") != "ms_itm_29691e53" {
		t.Errorf("stream query %v", q)
	}
	if p.MediaSourceID != "ms_itm_29691e53" || p.PlaySessionID != "ps:user:itm_133a" || p.AudioStreamIndex != 1 || p.SubtitleStreamIndex != -1 {
		t.Errorf("ids %+v", p)
	}
	if len(p.AudioTracks) != 1 || p.AudioTracks[0].Label() != "AAC stereo (默认)" || len(p.MediaSources) != 2 {
		t.Errorf("tracks %+v", p.AudioTracks)
	}

	// Second version: MKV the server claims to direct play but cannot
	// transcode. The client flags it as not natively playable.
	p = resolve(t, "https://emby.example.com", "tok", embyDirectJSON, PlaybackOptions{MediaSourceID: "ms_itm_133a"})
	if p.Method != DirectPlay || p.NativelyPlayable || p.MediaSourceID != "ms_itm_133a" {
		t.Errorf("mkv: method %v native %v ms %v", p.Method, p.NativelyPlayable, p.MediaSourceID)
	}
	if p.AudioStreamIndex != 2 || p.SubtitleStreamIndex != 8 {
		t.Errorf("mkv defaults %d %d", p.AudioStreamIndex, p.SubtitleStreamIndex)
	}
	if l := p.AudioTrack(5).Label(); l != "东森台配 · AC3 stereo" {
		t.Errorf("audio label %q", l)
	}
	pgs, ass := p.SubtitleTrack(7), p.SubtitleTrack(8)
	if pgs.IsText || !pgs.NeedsBurnIn || pgs.URL != "" {
		t.Errorf("pgs track %+v", pgs)
	}
	if !ass.IsText || ass.NeedsBurnIn {
		t.Errorf("ass track %+v", ass)
	}
	if want := "https://emby.example.com/Videos/item/ms_itm_133a/Subtitles/8/Stream.vtt?api_key=tok&ApiKey=tok"; ass.URL != want {
		t.Errorf("ass url %q\nwant %q", ass.URL, want)
	}

	// Forcing a transcode on a server that cannot: falls back, flagged.
	p = resolve(t, "https://emby.example.com", "tok", embyDirectJSON, PlaybackOptions{MediaSourceID: "ms_itm_133a", ForceTranscode: true, AudioStreamIndex: Index(5)})
	if !p.TranscodeUnavailable || p.Method != DirectPlay || p.AudioStreamIndex != 5 {
		t.Errorf("forced: %+v", p)
	}
}

func TestResolveEmbyTranscode(t *testing.T) {
	p := resolve(t, "http://10.0.0.2:8096/emby", "tok", embyTranscodeJSON, PlaybackOptions{})
	if p.Method != Transcode || !p.IsHLS || !p.NativelyPlayable {
		t.Errorf("method %v hls %v", p.Method, p.IsHLS)
	}
	path, q := mustQuery(t, p.StreamURL)
	if path != "http://10.0.0.2:8096/emby/videos/4242/master.m3u8" {
		t.Errorf("path %q", path)
	}
	if q.Get("api_key") != "tok" || q.Get("ApiKey") != "tok" || q.Get("AudioCodec") != "aac,ac3" || q.Get("PlaySessionId") != "abc" {
		t.Errorf("query %v", q)
	}
	if strings.Contains(p.StreamURL, "old") {
		t.Errorf("stale token kept: %s", p.StreamURL)
	}
	if len(p.TranscodeReasons) != 1 || p.TranscodeReasons[0] != "ContainerNotSupported" {
		t.Errorf("reasons %v", p.TranscodeReasons)
	}
	srt, pgs := p.SubtitleTrack(2), p.SubtitleTrack(3)
	if want := "http://10.0.0.2:8096/emby/videos/4242/mediasource_4242/Subtitles/2/0/Stream.vtt?api_key=tok&ApiKey=tok"; srt.URL != want {
		t.Errorf("srt url %q", srt.URL)
	}
	if !srt.IsExternal || srt.DeliveryMethod != DeliveryExternal || srt.Label() != "English (SUBRIP)" {
		t.Errorf("srt %+v", srt)
	}
	if pgs.IsText || !pgs.NeedsBurnIn || !pgs.IsForced {
		t.Errorf("pgs %+v", pgs)
	}
	if p.SubtitleStreamIndex != -1 || p.AudioStreamIndex != 1 {
		t.Errorf("indexes %d %d", p.AudioStreamIndex, p.SubtitleStreamIndex)
	}
}

func TestResolveJellyfinDirect(t *testing.T) {
	p := resolve(t, "https://demo.jellyfin.org/stable", "jtok", jellyfinDirectJSON, PlaybackOptions{})
	if p.Method != DirectPlay || !p.NativelyPlayable {
		t.Errorf("method %v", p.Method)
	}
	path, q := mustQuery(t, p.StreamURL)
	if path != "https://demo.jellyfin.org/stable/Videos/item/stream.mov" {
		t.Errorf("path %q", path)
	}
	for k, v := range map[string]string{"Static": "true", "MediaSourceId": "1056b7d34f0fa0b8ce635428e8b27f37",
		"DeviceId": "dev-123", "PlaySessionId": "4045534b6df64e45a2fae194d20b1be3", "Tag": "e1", "api_key": "jtok", "ApiKey": "jtok"} {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
	sub := p.SubtitleTrack(2)
	if want := "https://demo.jellyfin.org/stable/Videos/1056b7d3-4f0f-a0b8-ce63-5428e8b27f37/1056b7d34f0fa0b8ce635428e8b27f37/Subtitles/2/0/Stream.vtt?api_key=jtok&ApiKey=jtok"; sub.URL != want {
		t.Errorf("sub url %q\nwant %q", sub.URL, want)
	}
	if p.SubtitleStreamIndex != -1 {
		t.Errorf("subtitle index %d", p.SubtitleStreamIndex)
	}
}

func TestResolveJellyfinTranscode(t *testing.T) {
	p := resolve(t, "https://demo.jellyfin.org/stable", "jtok", jellyfinTranscodeJSON, PlaybackOptions{})
	if p.Method != Transcode || !p.IsHLS {
		t.Errorf("method %v", p.Method)
	}
	path, q := mustQuery(t, p.StreamURL)
	if path != "https://demo.jellyfin.org/stable/videos/788ae1cd-f381-6e34-41e2-9dd3a96ddfe1/master.m3u8" {
		t.Errorf("path %q", path)
	}
	if q.Get("ApiKey") != "jtok" || q.Get("api_key") != "jtok" || len(q["ApiKey"]) != 1 || q.Get("SegmentContainer") != "ts" {
		t.Errorf("query %v", q)
	}
	if len(p.TranscodeReasons) != 2 || p.AudioStreamIndex != 2 {
		t.Errorf("reasons %v audio %d", p.TranscodeReasons, p.AudioStreamIndex)
	}
}

func TestResolveURLs(t *testing.T) {
	c, _ := RestoreClient(Session{ServerURL: "https://h/emby", UserID: "u", Token: "t k"}, testDevice)
	cases := map[string]string{
		"/videos/1/master.m3u8?a=1":           "https://h/emby/videos/1/master.m3u8?a=1&api_key=t+k&ApiKey=t+k",
		"videos/1/x":                          "https://h/emby/videos/1/x?api_key=t+k&ApiKey=t+k",
		"/emby/videos/1/x?API_KEY=z&b=2":      "https://h/emby/videos/1/x?b=2&api_key=t+k&ApiKey=t+k",
		"https://h/emby/videos/1/x":           "https://h/emby/videos/1/x?api_key=t+k&ApiKey=t+k",
		"https://cdn.example.com/f.mp4?sig=1": "https://cdn.example.com/f.mp4?sig=1",
	}
	for in, want := range cases {
		if got := c.withToken(c.absURL(in)); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestResolveErrors(t *testing.T) {
	c, _ := RestoreClient(Session{ServerURL: "http://h", UserID: "u", Token: "t"}, testDevice)
	_, err := c.resolvePlayback("i", &PlaybackInfoResponse{ErrorCode: "NoCompatibleStream"}, PlaybackOptions{})
	if !errors.Is(err, ErrNotPlayable) || !strings.Contains(err.Error(), "NoCompatibleStream") {
		t.Errorf("err = %v", err)
	}
	_, err = c.resolvePlayback("i", &PlaybackInfoResponse{MediaSources: []MediaSource{{ID: "m"}}}, PlaybackOptions{})
	if !errors.Is(err, ErrNotPlayable) {
		t.Errorf("err = %v", err)
	}
}

func TestPlaybackInfoRequest(t *testing.T) {
	var gotQuery url.Values
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Items/itm/PlaybackInfo" {
			w.WriteHeader(404)
			return
		}
		gotQuery = r.URL.Query()
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, embyTranscodeJSON)
	}))
	defer srv.Close()
	c, _ := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "tok"}, testDevice)
	p, err := c.PlaybackInfo(context.Background(), "itm", PlaybackOptions{
		MediaSourceID: "mediasource_4242", AudioStreamIndex: Index(1), SubtitleStreamIndex: Index(-1),
		StartTimeTicks: 12345, MaxStreamingBitrate: 8_000_000, ForceTranscode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"UserId": "u1", "MediaSourceId": "mediasource_4242", "AudioStreamIndex": "1",
		"SubtitleStreamIndex": "-1", "StartTimeTicks": "12345", "MaxStreamingBitrate": "8000000",
		"EnableDirectPlay": "false", "EnableDirectStream": "false", "EnableTranscoding": "true", "IsPlayback": "true"} {
		if gotQuery.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, gotQuery.Get(k), v)
		}
	}
	prof, _ := gotBody["DeviceProfile"].(map[string]any)
	if prof == nil || gotBody["EnableDirectPlay"] != false || gotBody["AudioStreamIndex"] != float64(1) {
		t.Fatalf("body %v", gotBody)
	}
	if prof["MaxStreamingBitrate"] != float64(8_000_000) {
		t.Errorf("profile bitrate %v", prof["MaxStreamingBitrate"])
	}
	tps, _ := prof["TranscodingProfiles"].([]any)
	tp0, _ := tps[0].(map[string]any)
	if tp0["Container"] != "ts" || tp0["Protocol"] != "hls" || tp0["VideoCodec"] != "h264" {
		t.Errorf("transcoding profile %v", tp0)
	}
	for _, k := range []string{"ContainerProfiles", "ResponseProfiles", "CodecProfiles", "SubtitleProfiles", "DirectPlayProfiles"} {
		if _, ok := prof[k].([]any); !ok {
			t.Errorf("profile %s is not an array: %v", k, prof[k])
		}
	}
	if p.Method != Transcode || p.PlaySessionID != "abc" || p.StartTimeTicks != 12345 {
		t.Errorf("playback %+v", p)
	}

	rep := p.Report(TicksToDuration(5_000_000_0), true)
	b, _ := json.Marshal(rep)
	want := `{"ItemId":"itm","MediaSourceId":"mediasource_4242","PlaySessionId":"abc","PositionTicks":50000000,"IsPaused":true,"IsMuted":false,"VolumeLevel":100,"PlayMethod":"Transcode","AudioStreamIndex":1,"SubtitleStreamIndex":-1,"CanSeek":true}`
	if string(b) != want {
		t.Errorf("report json\n%s\nwant\n%s", b, want)
	}
}

func TestReportingRequests(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" "+toString(body["ItemId"]))
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c, _ := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "tok"}, testDevice)
	ctx := context.Background()
	r := PlaybackReport{ItemID: "i1", PlaySessionID: "ps"}
	if err := c.ReportPlaybackStart(ctx, r); err != nil {
		t.Fatal(err)
	}
	c.ReportPlaybackProgress(ctx, r)
	c.ReportPlaybackStopped(ctx, r)
	c.StopTranscoding(ctx, "ps")
	want := []string{
		"POST /Sessions/Playing? i1",
		"POST /Sessions/Playing/Progress? i1",
		"POST /Sessions/Playing/Stopped? i1",
		"DELETE /Videos/ActiveEncodings?DeviceId=dev-123&PlaySessionId=ps ",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func TestUserDataCalls(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		fav := r.Method == http.MethodPost && strings.Contains(r.URL.Path, "Favorite")
		json.NewEncoder(w).Encode(map[string]any{"IsFavorite": fav, "Played": false, "PlayCount": 0})
	}))
	defer srv.Close()
	c, _ := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "tok"}, testDevice)
	ctx := context.Background()
	ud, err := c.SetFavorite(ctx, "i1", true)
	if err != nil || !ud.IsFavorite {
		t.Fatalf("SetFavorite: %+v %v", ud, err)
	}
	c.SetFavorite(ctx, "i1", false)
	c.SetPlayed(ctx, "i1", true)
	c.SetPlayed(ctx, "i1", false)
	want := "POST /Users/u1/FavoriteItems/i1,DELETE /Users/u1/FavoriteItems/i1,POST /Users/u1/PlayedItems/i1,DELETE /Users/u1/PlayedItems/i1"
	if strings.Join(calls, ",") != want {
		t.Errorf("calls %v", calls)
	}
}

// Every text subtitle format is delivered as WebVTT for the client to draw
// (External), so choosing one never makes the server transcode the video
// to burn it in; bitmap formats are burned in.
func TestProfileSubtitleMethods(t *testing.T) {
	methods := map[string]string{}
	for _, sp := range AVPlayerProfile(0).SubtitleProfiles {
		if prev, dup := methods[sp.Format]; dup {
			t.Errorf("format %q listed twice (%s, %s)", sp.Format, prev, sp.Method)
		}
		methods[sp.Format] = sp.Method
	}
	for codec := range textSubtitleCodecs {
		if codec == "eia_608" {
			continue // closed captions travel inside the video stream
		}
		if methods[codec] != DeliveryExternal {
			t.Errorf("text format %q: method %q, want External", codec, methods[codec])
		}
	}
	for codec := range imageSubtitleCodecs {
		if methods[codec] != DeliveryEncode {
			t.Errorf("bitmap format %q: method %q, want Encode", codec, methods[codec])
		}
	}
}

// HEVC tagged hev1 in MP4 does not open in AVFoundation: such a source is
// not natively playable, while hvc1 and untagged HEVC are.
func TestAVPlayerCanPlayHEVCTags(t *testing.T) {
	src := func(tag string) *MediaSource {
		return &MediaSource{Container: "mov", MediaStreams: []MediaStream{
			{Index: 0, Type: StreamVideo, Codec: "hevc", CodecTag: tag, BitDepth: 10},
			{Index: 1, Type: StreamAudio, Codec: "aac"},
		}}
	}
	for tag, want := range map[string]bool{"hvc1": true, "": true, "hev1": false, "HEV1": false} {
		if got := avPlayerCanPlay(src(tag), 1); got != want {
			t.Errorf("hevc tagged %q: playable %v, want %v", tag, got, want)
		}
	}
}
