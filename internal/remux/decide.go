package remux

import (
	"strings"

	"mygo-emby/internal/emby"
)

// Containers AVPlayer opens itself, containers a Session reads, and
// containers neither handles.
var (
	nativeContainers = map[string]bool{"mp4": true, "m4v": true, "mov": true}
	remuxContainers  = map[string]bool{"mkv": true, "matroska": true, "webm": true, "ts": true,
		"mpegts": true, "m2ts": true, "mts": true, "m2t": true, "mpeg": true}
	deadContainers = map[string]bool{"iso": true, "blurayiso": true, "dvdiso": true, "bluray": true,
		"dvd": true, "vob": true, "avi": true, "wmv": true, "asf": true, "rm": true, "rmvb": true,
		"flv": true, "3gp": true, "ogg": true, "ogv": true, "divx": true, "strm": true}
	// AVPlayer decodes these in an MP4 it opens itself.
	nativeAudio = map[string]bool{"aac": true, "ac3": true, "eac3": true, "mp3": true, "alac": true, "flac": true}
	// A Session copies these into fragmented MP4.
	remuxAudio = map[string]bool{"aac": true, "ac3": true, "eac3": true}
)

// Decide tells how an Emby media source can play with AVPlayer: as it is,
// remuxed by a Session, or not at all. audioIndex is the Emby MediaStream
// index of the wanted audio track, -1 for the source's default.
//
// When the wanted audio track cannot be copied, Decide first picks another
// copyable track if the caller left the choice to it (audioIndex -1). If
// none exists, the selected track is marked AudioTranscode, for the caller
// to enable the optional local audio-only FFmpeg fallback.
//
// Sources without stream information (Emby has not probed the file) get
// Remux: the session finds out what the file is when it opens.
func Decide(ms *emby.MediaSource, audioIndex int) Decision {
	if ms == nil {
		return Decision{Mode: Unsupported, Reason: "no media source", AudioIndex: -1}
	}
	containers := strings.Split(strings.ToLower(ms.Container), ",")
	for i := range containers {
		containers[i] = strings.TrimSpace(containers[i])
	}
	native, dead := false, false
	for _, c := range containers {
		native = native || nativeContainers[c]
		dead = dead || deadContainers[c]
	}
	if dead && !native {
		return Decision{Mode: Unsupported, Reason: "container " + ms.Container + " cannot be remuxed", AudioIndex: -1}
	}

	video := mainVideo(ms)
	if video == nil {
		if len(ms.MediaStreams) == 0 {
			return Decision{Mode: Remux, Reason: "no stream information; the file is probed", AudioIndex: audioIndex}
		}
		return Decision{Mode: Unsupported, Reason: "no video stream", AudioIndex: -1}
	}
	vcodec := strings.ToLower(video.Codec)
	switch vcodec {
	case "h264", "avc":
		if video.BitDepth > 8 || strings.Contains(strings.ToLower(video.Profile), "10") ||
			strings.Contains(video.Profile, "4:2:2") || strings.Contains(video.Profile, "4:4:4") {
			return Decision{Mode: Unsupported, Reason: "H.264 " + video.Profile + " is not decoded by AVPlayer", AudioIndex: -1}
		}
	case "hevc", "h265":
	default:
		return Decision{Mode: Unsupported, Reason: "video codec " + video.Codec + " needs transcoding", AudioIndex: -1}
	}

	// The audio track.
	var audios []*emby.MediaStream
	for i := range ms.MediaStreams {
		if s := &ms.MediaStreams[i]; s.Type == emby.StreamAudio && !s.IsExternal {
			audios = append(audios, s)
		}
	}
	avplayerAudio := defaultAudio(ms, audios) // what AVPlayer plays of a file
	var audio *emby.MediaStream
	if audioIndex >= 0 {
		audio = ms.Stream(audioIndex)
		if audio != nil && audio.Type != emby.StreamAudio {
			audio = nil
		}
	} else {
		audio = avplayerAudio
	}
	d := Decision{AudioIndex: -1}
	if audio != nil {
		d.AudioIndex = audio.Index
	}
	audioCodec := ""
	if audio != nil {
		audioCodec = strings.ToLower(audio.Codec)
	}

	tag := strings.ToLower(video.CodecTag)
	directVideo := native && (tag == "avc1" || tag == "hvc1" || tag == "dvh1")
	if directVideo && (audio == nil || audio == avplayerAudio && nativeAudio[audioCodec]) {
		d.Mode = DirectPlay
		return d
	}
	d.Mode = Remux
	switch {
	case !native:
		d.Reason = "container " + ms.Container
	case !directVideo:
		d.Reason = "video tag " + video.CodecTag
	default:
		d.Reason = "audio track is not the file's default"
	}
	if audio == nil || remuxAudio[audioCodec] {
		return d
	}
	if audioIndex < 0 {
		for _, a := range audios {
			if remuxAudio[strings.ToLower(a.Codec)] {
				d.AudioIndex = a.Index
				d.Reason += "; default audio " + audio.Codec + " replaced"
				return d
			}
		}
	}
	d.AudioTranscode = true
	d.Reason += "; audio " + audio.Codec + " can be decoded locally to AAC"
	return d
}

// mainVideo is the first video stream that is not cover art.
func mainVideo(ms *emby.MediaSource) *emby.MediaStream {
	for i := range ms.MediaStreams {
		s := &ms.MediaStreams[i]
		if s.Type != emby.StreamVideo || s.IsExternal {
			continue
		}
		switch strings.ToLower(s.Codec) {
		case "mjpeg", "png", "bmp", "gif", "jpeg", "webp":
			continue
		}
		return s
	}
	return nil
}

// defaultAudio is the audio track a player picks by itself: the one
// flagged default, else the first.
func defaultAudio(ms *emby.MediaSource, audios []*emby.MediaStream) *emby.MediaStream {
	for _, a := range audios {
		if a.IsDefault {
			return a
		}
	}
	if ms.DefaultAudioStreamIndex != nil {
		for _, a := range audios {
			if a.Index == *ms.DefaultAudioStreamIndex {
				return a
			}
		}
	}
	if len(audios) > 0 {
		return audios[0]
	}
	return nil
}
