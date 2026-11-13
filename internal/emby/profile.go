package emby

// DeviceProfile describes what the player can handle, so the server can
// decide between direct play, remux and transcode. Field names follow the
// Emby/Jellyfin DeviceProfile DTO.
type DeviceProfile struct {
	Name                             string               `json:"Name"`
	MaxStreamingBitrate              int64                `json:"MaxStreamingBitrate"`
	MaxStaticBitrate                 int64                `json:"MaxStaticBitrate"`
	MusicStreamingTranscodingBitrate int64                `json:"MusicStreamingTranscodingBitrate"`
	DirectPlayProfiles               []DirectPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles              []TranscodingProfile `json:"TranscodingProfiles"`
	ContainerProfiles                []ContainerProfile   `json:"ContainerProfiles"`
	CodecProfiles                    []CodecProfile       `json:"CodecProfiles"`
	SubtitleProfiles                 []SubtitleProfile    `json:"SubtitleProfiles"`
	ResponseProfiles                 []struct{}           `json:"ResponseProfiles"`
}

// DirectPlayProfile lists container/codec combinations played as-is.
type DirectPlayProfile struct {
	Container  string `json:"Container"`
	Type       string `json:"Type"` // Video, Audio
	VideoCodec string `json:"VideoCodec,omitempty"`
	AudioCodec string `json:"AudioCodec,omitempty"`
}

// TranscodingProfile describes the format the server should produce when
// it cannot direct play.
type TranscodingProfile struct {
	Container           string `json:"Container"`
	Type                string `json:"Type"`
	VideoCodec          string `json:"VideoCodec,omitempty"`
	AudioCodec          string `json:"AudioCodec,omitempty"`
	Protocol            string `json:"Protocol"` // "hls" or "http"
	Context             string `json:"Context"`  // "Streaming" or "Static"
	MaxAudioChannels    string `json:"MaxAudioChannels,omitempty"`
	MinSegments         int    `json:"MinSegments,omitempty"`
	BreakOnNonKeyFrames bool   `json:"BreakOnNonKeyFrames"`
	CopyTimestamps      bool   `json:"CopyTimestamps"`
}

// ContainerProfile restricts containers by condition.
type ContainerProfile struct {
	Type       string             `json:"Type"`
	Container  string             `json:"Container,omitempty"`
	Conditions []ProfileCondition `json:"Conditions"`
}

// CodecProfile restricts a codec by conditions (bit depth, level…).
type CodecProfile struct {
	Type       string             `json:"Type"` // Video, VideoAudio, Audio
	Codec      string             `json:"Codec,omitempty"`
	Container  string             `json:"Container,omitempty"`
	Conditions []ProfileCondition `json:"Conditions"`
}

// ProfileCondition is one condition of a codec or container profile.
type ProfileCondition struct {
	Condition  string `json:"Condition"` // Equals, NotEquals, LessThanEqual, GreaterThanEqual, EqualsAny
	Property   string `json:"Property"`  // VideoBitDepth, VideoLevel, VideoProfile, VideoCodecTag…
	Value      string `json:"Value"`
	IsRequired bool   `json:"IsRequired"`
}

// SubtitleProfile tells the server how a subtitle format is delivered.
type SubtitleProfile struct {
	Format string `json:"Format"`
	Method string `json:"Method"` // External, Embed, Encode, Hls
}

// DefaultMaxStreamingBitrate is used when PlaybackOptions.MaxStreamingBitrate
// is 0 (120 Mbps: effectively "original quality").
const DefaultMaxStreamingBitrate = 120_000_000

// Containers and codecs AVPlayer on macOS plays natively.
var (
	nativeContainers  = map[string]bool{"mp4": true, "m4v": true, "mov": true}
	nativeVideoCodecs = map[string]bool{"h264": true, "avc": true, "hevc": true, "h265": true}
	nativeAudioCodecs = map[string]bool{"aac": true, "ac3": true, "eac3": true, "mp3": true, "alac": true, "flac": true}
)

// AVPlayerProfile returns the device profile for AVPlayer on macOS:
//
//   - Direct play: mp4/m4v/mov containing H.264 (8-bit) or HEVC
//     (Main/Main 10, tagged hvc1/dvh1) with AAC, AC3, E-AC3, MP3, ALAC or FLAC.
//   - Everything else: HLS with MPEG-TS segments, H.264 video and
//     AAC/AC3/E-AC3 audio (stream copy when the source already matches, so
//     H.264 MKVs are only remuxed).
//   - Subtitles: every text format (SRT, ASS/SSA, WebVTT, mov_text, TTML…)
//     delivered externally as WebVTT, which the client draws, so choosing
//     one never makes the server transcode the video to burn it in; only
//     bitmap formats (PGS, VobSub, DVB) are burned in.
//
// HEVC is deliberately not offered for HLS: Apple requires fMP4 segments for
// HEVC, and fMP4 HLS output is not reliable across Emby and Jellyfin
// versions, so HEVC sources that cannot be direct played are transcoded to
// H.264. maxBitrate <= 0 means DefaultMaxStreamingBitrate.
func AVPlayerProfile(maxBitrate int64) DeviceProfile {
	if maxBitrate <= 0 {
		maxBitrate = DefaultMaxStreamingBitrate
	}
	return DeviceProfile{
		Name:                             "MyGo Emby (AVPlayer)",
		MaxStreamingBitrate:              maxBitrate,
		MaxStaticBitrate:                 maxBitrate,
		MusicStreamingTranscodingBitrate: 320_000,
		DirectPlayProfiles: []DirectPlayProfile{
			{Container: "mp4,m4v,mov", Type: "Video", VideoCodec: "h264,hevc", AudioCodec: "aac,ac3,eac3,mp3,alac,flac"},
			{Container: "mp3", Type: "Audio", AudioCodec: "mp3"},
			{Container: "m4a,m4b,mp4", Type: "Audio", AudioCodec: "aac,alac"},
			{Container: "flac", Type: "Audio", AudioCodec: "flac"},
		},
		TranscodingProfiles: []TranscodingProfile{
			{
				Container: "ts", Type: "Video", VideoCodec: "h264", AudioCodec: "aac,ac3,eac3",
				Protocol: "hls", Context: "Streaming", MaxAudioChannels: "6",
				MinSegments: 1, BreakOnNonKeyFrames: true,
			},
			{Container: "aac", Type: "Audio", AudioCodec: "aac", Protocol: "http", Context: "Streaming", MaxAudioChannels: "2"},
		},
		ContainerProfiles: []ContainerProfile{},
		CodecProfiles: []CodecProfile{
			{Type: "Video", Codec: "h264", Conditions: []ProfileCondition{
				{Condition: "LessThanEqual", Property: "VideoBitDepth", Value: "8"},
				{Condition: "LessThanEqual", Property: "VideoLevel", Value: "52"},
			}},
			{Type: "Video", Codec: "hevc", Conditions: []ProfileCondition{
				{Condition: "LessThanEqual", Property: "VideoBitDepth", Value: "10"},
				{Condition: "EqualsAny", Property: "VideoProfile", Value: "main|main 10"},
				{Condition: "EqualsAny", Property: "VideoCodecTag", Value: "hvc1|dvh1"},
			}},
		},
		SubtitleProfiles: avPlayerSubtitleProfiles(),
		ResponseProfiles: []struct{}{},
	}
}

// Subtitle formats by how the player shows them. Text formats are fetched
// as WebVTT and drawn by the client over the picture, so the server never
// transcodes a video to burn text in; only bitmap formats, which the
// client cannot draw, are burned in (Encode), which needs a transcode.
var (
	externalSubtitleFormats = []string{
		"vtt", "webvtt", "srt", "subrip", "ass", "ssa", "mov_text", "tx3g",
		"ttml", "dfxp", "sami", "smi", "microdvd", "mpl2", "subviewer", "text",
		"jacosub", "realtext", "stl",
	}
	burnInSubtitleFormats = []string{
		"pgssub", "pgs", "hdmv_pgs_subtitle", "sup", "dvdsub", "dvd_subtitle",
		"vobsub", "sub", "dvbsub", "dvb_subtitle", "dvb_teletext", "xsub",
	}
)

// avPlayerSubtitleProfiles lists every text format as External and every
// bitmap format as Encode.
func avPlayerSubtitleProfiles() []SubtitleProfile {
	out := make([]SubtitleProfile, 0, len(externalSubtitleFormats)+len(burnInSubtitleFormats))
	for _, f := range externalSubtitleFormats {
		out = append(out, SubtitleProfile{Format: f, Method: DeliveryExternal})
	}
	for _, f := range burnInSubtitleFormats {
		out = append(out, SubtitleProfile{Format: f, Method: DeliveryEncode})
	}
	return out
}
