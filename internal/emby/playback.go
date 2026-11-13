package emby

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PlayMethod is how a stream reaches the player. The value is reported back
// to the server in playback reports.
type PlayMethod string

// Play methods.
const (
	// DirectPlay: the original file, served statically.
	DirectPlay PlayMethod = "DirectPlay"
	// DirectStream: the original file streamed by the server without
	// conversion (container AVPlayer may not understand).
	DirectStream PlayMethod = "DirectStream"
	// Transcode: an HLS stream produced by the server (remux or transcode).
	Transcode PlayMethod = "Transcode"
)

// Index returns a pointer to i, for the optional stream index fields of
// PlaybackOptions and PlaybackReport.
func Index(i int) *int { return &i }

// PlaybackOptions are the parameters of a PlaybackInfo request.
type PlaybackOptions struct {
	// MediaSourceID selects a version; "" lets the server pick the first.
	MediaSourceID string
	// AudioStreamIndex selects an audio track; nil uses the server default.
	// AVPlayer cannot switch audio tracks itself, so selecting a
	// non-default track should be combined with ForceTranscode.
	AudioStreamIndex *int
	// SubtitleStreamIndex selects a subtitle; nil uses the server default,
	// Index(-1) disables subtitles. Selecting a bitmap subtitle makes the
	// server burn it in (forces a transcode).
	SubtitleStreamIndex *int
	// StartTimeTicks is where playback will start. HLS playlists always
	// cover the whole item, so the player still seeks after loading.
	StartTimeTicks int64
	// MaxStreamingBitrate in bits per second; 0 = DefaultMaxStreamingBitrate.
	MaxStreamingBitrate int64
	// ForceTranscode disables direct play and direct stream, so the server
	// delivers HLS (remuxed when possible). Use it to switch audio tracks or
	// when direct play failed in the player.
	ForceTranscode bool
	// Profile overrides AVPlayerProfile.
	Profile *DeviceProfile
}

// PlaybackInfoResponse is the raw answer of POST /Items/{id}/PlaybackInfo.
type PlaybackInfoResponse struct {
	MediaSources  []MediaSource `json:"MediaSources"`
	PlaySessionID string        `json:"PlaySessionId"`
	ErrorCode     string        `json:"ErrorCode"`
}

// Track is an audio or subtitle track as the player UI needs it.
type Track struct {
	Index             int
	Type              string // StreamAudio or StreamSubtitle
	Codec             string
	Language          string // ISO 639-2 code as reported, e.g. "chi", "eng"
	Title             string // the stream's own title, may be ""
	DisplayTitle      string // server-composed label, may be ""
	IsDefault         bool
	IsForced          bool
	IsExternal        bool
	IsHearingImpaired bool
	// IsText: text subtitle that the client renders from URL.
	IsText bool
	// NeedsBurnIn: bitmap subtitle; selecting it requires a new
	// PlaybackInfo request with SubtitleStreamIndex set (server burns it in).
	NeedsBurnIn    bool
	Channels       int
	DeliveryMethod string
	// URL is the absolute WebVTT URL of a text subtitle (with api_key),
	// "" for audio and bitmap subtitles.
	URL string
}

// Label returns a human-readable track name: the stream title combined
// with the server's display title, or a composed fallback.
func (t Track) Label() string {
	dt := strings.TrimSpace(t.DisplayTitle)
	title := strings.TrimSpace(t.Title)
	switch {
	case dt != "" && title != "" && !strings.Contains(dt, title):
		return title + " · " + dt
	case dt != "":
		return dt
	case title != "":
		return title
	}
	var parts []string
	if t.Language != "" && t.Language != "und" {
		parts = append(parts, t.Language)
	}
	if t.Codec != "" {
		parts = append(parts, strings.ToUpper(t.Codec))
	}
	if t.Channels > 0 {
		parts = append(parts, channelLabel(t.Channels))
	}
	if t.IsForced {
		parts = append(parts, "Forced")
	}
	if len(parts) == 0 {
		return "Track " + strconv.Itoa(t.Index)
	}
	return strings.Join(parts, " ")
}

func channelLabel(n int) string {
	switch n {
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return strconv.Itoa(n) + "ch"
}

// Playback is a resolved PlaybackInfo: everything the player needs.
type Playback struct {
	ItemID        string
	MediaSourceID string
	PlaySessionID string
	Method        PlayMethod
	// StreamURL is absolute and carries the token as api_key and ApiKey
	// query parameters, because AVPlayer cannot send headers.
	StreamURL string
	// IsHLS reports an .m3u8 stream.
	IsHLS bool
	// NativelyPlayable is the client's own judgement that AVPlayer can open
	// StreamURL. It is false only when the server offered nothing but a
	// direct stream of a container AVPlayer does not support (servers that
	// cannot transcode); the UI should warn instead of starting the player.
	NativelyPlayable bool
	// TranscodeUnavailable is set when ForceTranscode was requested but the
	// server offered no transcoding URL; StreamURL is then the direct one.
	TranscodeUnavailable bool
	RunTimeTicks         int64
	StartTimeTicks       int64
	// AudioStreamIndex and SubtitleStreamIndex are the effective
	// selections (-1: none or unknown). For direct play AVPlayer chooses
	// the container's default audio track, which may differ.
	AudioStreamIndex    int
	SubtitleStreamIndex int
	AudioTracks         []Track
	SubtitleTracks      []Track
	TranscodeReasons    []string
	// Source is the chosen media source; MediaSources are all versions.
	Source       MediaSource
	MediaSources []MediaSource
}

// Runtime returns the item length.
func (p *Playback) Runtime() time.Duration { return TicksToDuration(p.RunTimeTicks) }

// SubtitleTrack returns the subtitle track with index, or nil.
func (p *Playback) SubtitleTrack(index int) *Track {
	for i := range p.SubtitleTracks {
		if p.SubtitleTracks[i].Index == index {
			return &p.SubtitleTracks[i]
		}
	}
	return nil
}

// AudioTrack returns the audio track with index, or nil.
func (p *Playback) AudioTrack(index int) *Track {
	for i := range p.AudioTracks {
		if p.AudioTracks[i].Index == index {
			return &p.AudioTracks[i]
		}
	}
	return nil
}

// PlaybackInfo asks the server how to play itemID (POST
// /Items/{id}/PlaybackInfo with the AVPlayer DeviceProfile) and resolves
// the answer into a ready-to-play stream URL plus track lists.
func (c *Client) PlaybackInfo(ctx context.Context, itemID string, opts PlaybackOptions) (*Playback, error) {
	const op = "POST /Items/{id}/PlaybackInfo"
	uid, err := c.requireUser(op)
	if err != nil {
		return nil, err
	}
	bitrate := opts.MaxStreamingBitrate
	if bitrate <= 0 {
		bitrate = DefaultMaxStreamingBitrate
	}
	profile := AVPlayerProfile(bitrate)
	if opts.Profile != nil {
		profile = *opts.Profile
	}
	direct := !opts.ForceTranscode

	q := url.Values{}
	q.Set("UserId", uid)
	q.Set("StartTimeTicks", strconv.FormatInt(opts.StartTimeTicks, 10))
	q.Set("IsPlayback", "true")
	q.Set("AutoOpenLiveStream", "true")
	q.Set("MaxStreamingBitrate", strconv.FormatInt(bitrate, 10))
	q.Set("EnableDirectPlay", strconv.FormatBool(direct))
	q.Set("EnableDirectStream", strconv.FormatBool(direct))
	q.Set("EnableTranscoding", "true")
	q.Set("AllowVideoStreamCopy", "true")
	q.Set("AllowAudioStreamCopy", "true")
	body := map[string]any{
		"DeviceProfile":        profile,
		"UserId":               uid,
		"StartTimeTicks":       opts.StartTimeTicks,
		"IsPlayback":           true,
		"AutoOpenLiveStream":   true,
		"MaxStreamingBitrate":  bitrate,
		"EnableDirectPlay":     direct,
		"EnableDirectStream":   direct,
		"EnableTranscoding":    true,
		"AllowVideoStreamCopy": true,
		"AllowAudioStreamCopy": true,
	}
	if opts.MediaSourceID != "" {
		q.Set("MediaSourceId", opts.MediaSourceID)
		body["MediaSourceId"] = opts.MediaSourceID
	}
	if opts.AudioStreamIndex != nil {
		q.Set("AudioStreamIndex", strconv.Itoa(*opts.AudioStreamIndex))
		body["AudioStreamIndex"] = *opts.AudioStreamIndex
	}
	if opts.SubtitleStreamIndex != nil {
		q.Set("SubtitleStreamIndex", strconv.Itoa(*opts.SubtitleStreamIndex))
		body["SubtitleStreamIndex"] = *opts.SubtitleStreamIndex
	}

	var resp PlaybackInfoResponse
	if err := c.do(ctx, http.MethodPost, "/Items/"+url.PathEscape(itemID)+"/PlaybackInfo", q, body, &resp); err != nil {
		return nil, err
	}
	return c.resolvePlayback(itemID, &resp, opts)
}

// resolvePlayback turns a PlaybackInfo response into a Playback. It is
// pure (no network) and handles both server families:
//
//   - Emby returns DirectStreamUrl (relative, often already with api_key)
//     for sources it can stream statically, and TranscodingUrl otherwise.
//   - Jellyfin returns no DirectStreamUrl for direct play; the client builds
//     /Videos/{id}/stream.{container}?Static=true…. Its TranscodingUrl uses
//     the ApiKey parameter.
//   - All server URLs are relative to the server root, without the base
//     path; the client's base URL (which may include /emby or a reverse
//     proxy prefix) is prepended.
func (c *Client) resolvePlayback(itemID string, resp *PlaybackInfoResponse, opts PlaybackOptions) (*Playback, error) {
	const op = "POST /Items/{id}/PlaybackInfo"
	if len(resp.MediaSources) == 0 {
		msg := resp.ErrorCode
		if msg == "" {
			msg = "server returned no media sources"
		}
		return nil, &Error{Kind: KindNotPlayable, Op: op, Message: msg}
	}
	src := &resp.MediaSources[0]
	if opts.MediaSourceID != "" {
		for i := range resp.MediaSources {
			if resp.MediaSources[i].ID == opts.MediaSourceID {
				src = &resp.MediaSources[i]
				break
			}
		}
	}

	p := &Playback{
		ItemID:              itemID,
		MediaSourceID:       src.ID,
		PlaySessionID:       resp.PlaySessionID,
		RunTimeTicks:        src.RunTimeTicks,
		StartTimeTicks:      opts.StartTimeTicks,
		Source:              *src,
		MediaSources:        resp.MediaSources,
		AudioStreamIndex:    effectiveAudioIndex(src, opts.AudioStreamIndex),
		SubtitleStreamIndex: effectiveSubtitleIndex(src, opts.SubtitleStreamIndex),
		TranscodeReasons:    src.TranscodeReasons,
	}
	if p.MediaSourceID == "" {
		p.MediaSourceID = itemID
	}
	if len(p.TranscodeReasons) == 0 && src.TranscodingURL != "" {
		p.TranscodeReasons = transcodeReasonsFromURL(src.TranscodingURL)
	}

	native := avPlayerCanPlay(src, p.AudioStreamIndex)
	switch {
	case !opts.ForceTranscode && src.SupportsDirectPlay && (native || src.TranscodingURL == ""):
		p.Method = DirectPlay
		p.StreamURL = c.directStreamURL(itemID, src, resp.PlaySessionID)
		p.NativelyPlayable = native
	case src.TranscodingURL != "":
		p.Method = Transcode
		p.StreamURL = c.withToken(c.absURL(src.TranscodingURL))
		p.IsHLS = strings.EqualFold(src.TranscodingSubProtocol, "hls") ||
			strings.Contains(strings.ToLower(redactURL(src.TranscodingURL)), ".m3u8")
		p.NativelyPlayable = true
	case src.SupportsDirectStream || src.SupportsDirectPlay:
		p.Method = DirectStream
		if src.SupportsDirectPlay {
			p.Method = DirectPlay
		}
		p.StreamURL = c.directStreamURL(itemID, src, resp.PlaySessionID)
		p.NativelyPlayable = native
		p.TranscodeUnavailable = opts.ForceTranscode
	default:
		msg := resp.ErrorCode
		if msg == "" {
			msg = "server offers neither direct play nor transcoding for this item"
		}
		return nil, &Error{Kind: KindNotPlayable, Op: op, Message: msg}
	}
	if p.IsHLS {
		p.NativelyPlayable = true
	}

	for _, s := range src.MediaStreams {
		switch s.Type {
		case StreamAudio:
			p.AudioTracks = append(p.AudioTracks, trackFromStream(s))
		case StreamSubtitle:
			t := trackFromStream(s)
			t.IsText = s.IsTextSubtitle()
			t.NeedsBurnIn = !t.IsText
			if t.IsText {
				t.URL = c.subtitleURL(itemID, src.ID, s)
			}
			p.SubtitleTracks = append(p.SubtitleTracks, t)
		}
	}
	return p, nil
}

func trackFromStream(s MediaStream) Track {
	return Track{
		Index:             s.Index,
		Type:              s.Type,
		Codec:             s.Codec,
		Language:          s.Language,
		Title:             s.Title,
		DisplayTitle:      s.DisplayTitle,
		IsDefault:         s.IsDefault,
		IsForced:          s.IsForced,
		IsExternal:        s.IsExternal,
		IsHearingImpaired: s.IsHearingImpaired,
		Channels:          s.Channels,
		DeliveryMethod:    s.DeliveryMethod,
	}
}

func effectiveAudioIndex(src *MediaSource, requested *int) int {
	if requested != nil {
		return *requested
	}
	if src.DefaultAudioStreamIndex != nil && *src.DefaultAudioStreamIndex >= 0 {
		return *src.DefaultAudioStreamIndex
	}
	first := -1
	for _, s := range src.MediaStreams {
		if s.Type != StreamAudio {
			continue
		}
		if s.IsDefault {
			return s.Index
		}
		if first < 0 {
			first = s.Index
		}
	}
	return first
}

func effectiveSubtitleIndex(src *MediaSource, requested *int) int {
	if requested != nil {
		return *requested
	}
	if src.DefaultSubtitleStreamIndex != nil {
		return *src.DefaultSubtitleStreamIndex
	}
	return -1
}

// avPlayerCanPlay reports whether AVPlayer can open the source file as-is.
func avPlayerCanPlay(src *MediaSource, audioIndex int) bool {
	okContainer := false
	for _, c := range strings.Split(strings.ToLower(src.Container), ",") {
		if nativeContainers[strings.TrimSpace(c)] {
			okContainer = true
			break
		}
	}
	if !okContainer {
		return false
	}
	if v := src.VideoStream(); v != nil {
		codec := strings.ToLower(v.Codec)
		if !nativeVideoCodecs[codec] {
			return false
		}
		if (codec == "h264" || codec == "avc") && v.BitDepth > 8 {
			return false
		}
		// AVFoundation plays HEVC in MP4/MOV only when tagged hvc1 (or
		// dvh1 for Dolby Vision): hev1, with parameter sets in band, does
		// not open.
		if (codec == "hevc" || codec == "h265") && strings.EqualFold(v.CodecTag, "hev1") {
			return false
		}
	}
	var audio *MediaStream
	if audioIndex >= 0 {
		audio = src.Stream(audioIndex)
	}
	if audio == nil || audio.Type != StreamAudio {
		for i := range src.MediaStreams {
			if src.MediaStreams[i].Type == StreamAudio {
				audio = &src.MediaStreams[i]
				break
			}
		}
	}
	if audio != nil && audio.Type == StreamAudio && !nativeAudioCodecs[strings.ToLower(audio.Codec)] {
		return false
	}
	return true
}

func transcodeReasonsFromURL(u string) []string {
	i := strings.IndexByte(u, '?')
	if i < 0 {
		return nil
	}
	q, err := url.ParseQuery(u[i+1:])
	if err != nil {
		return nil
	}
	r := q.Get("TranscodeReasons")
	if r == "" {
		return nil
	}
	return strings.Split(r, ",")
}

// directStreamURL returns the static stream URL of a source: the server's
// DirectStreamUrl when given (Emby), else a constructed
// /Videos/{id}/stream.{ext}?Static=true… (Jellyfin).
func (c *Client) directStreamURL(itemID string, src *MediaSource, playSessionID string) string {
	if src.DirectStreamURL != "" {
		return c.withToken(c.absURL(src.DirectStreamURL))
	}
	p := "/Videos/" + url.PathEscape(itemID) + "/stream"
	if ext := firstContainer(src.Container); ext != "" {
		p += "." + ext
	}
	q := url.Values{}
	q.Set("Static", "true")
	q.Set("MediaSourceId", src.ID)
	q.Set("DeviceId", c.device.DeviceID)
	if playSessionID != "" {
		q.Set("PlaySessionId", playSessionID)
	}
	if src.ETag != "" {
		q.Set("Tag", src.ETag)
	}
	if src.LiveStreamID != "" {
		q.Set("LiveStreamId", src.LiveStreamID)
	}
	return c.withToken(c.apiURL(p, q))
}

// firstContainer returns the first entry of a container list such as
// "mov,mp4,m4a" if it is a plain token, else "".
func firstContainer(container string) string {
	c, _, _ := strings.Cut(strings.ToLower(container), ",")
	c = strings.TrimSpace(c)
	for _, r := range c {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return c
}

// subtitleURL returns an absolute URL serving a text subtitle as WebVTT:
// the server's DeliveryUrl when it is a VTT or SRT URL, else
// /Videos/{id}/{mediaSourceId}/Subtitles/{index}/Stream.vtt.
func (c *Client) subtitleURL(itemID, mediaSourceID string, s MediaStream) string {
	if s.DeliveryURL != "" && strings.EqualFold(s.DeliveryMethod, DeliveryExternal) {
		path := strings.ToLower(redactURL(s.DeliveryURL))
		if strings.HasSuffix(path, ".vtt") || strings.HasSuffix(path, ".srt") || strings.HasSuffix(path, ".subrip") {
			return c.withToken(c.absURL(s.DeliveryURL))
		}
	}
	if mediaSourceID == "" {
		mediaSourceID = itemID
	}
	return c.SubtitleURL(itemID, mediaSourceID, s.Index)
}

// SubtitleURL builds the WebVTT URL of subtitle stream index of a media
// source (/Videos/{id}/{mediaSourceId}/Subtitles/{index}/Stream.vtt), with
// the token as query parameters.
func (c *Client) SubtitleURL(itemID, mediaSourceID string, index int) string {
	p := "/Videos/" + url.PathEscape(itemID) + "/" + url.PathEscape(mediaSourceID) +
		"/Subtitles/" + strconv.Itoa(index) + "/Stream.vtt"
	return c.withToken(c.apiURL(p, nil))
}

// absURL makes a server-relative URL absolute. Server URLs are relative to
// the server root without the base path, so the full base URL is
// prepended, unless the URL already starts with the base path.
func (c *Client) absURL(u string) string {
	lu := strings.ToLower(u)
	if strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://") {
		return u
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	if bu, err := url.Parse(c.base); err == nil && bu.Path != "" {
		bp := strings.ToLower(bu.Path)
		if strings.HasPrefix(strings.ToLower(u), bp+"/") {
			return bu.Scheme + "://" + bu.Host + u
		}
	}
	return c.base + u
}

// withToken sets api_key (Emby, legacy Jellyfin) and ApiKey (Jellyfin with
// legacy authorization disabled) on a URL of this server, replacing any
// existing token parameters while keeping the rest of the query untouched.
// URLs on other hosts (remote sources, CDNs) are returned unchanged.
func (c *Client) withToken(raw string) string {
	tok := c.Token()
	if tok == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if bu, err := url.Parse(c.base); err == nil && u.Host != "" && !strings.EqualFold(u.Host, bu.Host) {
		return raw
	}
	var keep []string
	if u.RawQuery != "" {
		for _, kv := range strings.Split(u.RawQuery, "&") {
			k, _, _ := strings.Cut(kv, "=")
			if kv == "" || strings.EqualFold(k, "api_key") || strings.EqualFold(k, "apikey") {
				continue
			}
			keep = append(keep, kv)
		}
	}
	esc := url.QueryEscape(tok)
	keep = append(keep, "api_key="+esc, "ApiKey="+esc)
	u.RawQuery = strings.Join(keep, "&")
	return u.String()
}
