package remux

import (
	"net/http"
	"time"

	"mygo-emby/internal/emby"
)

// Source is a remote file to remux.
type Source struct {
	// URL is the file's address: Emby's static stream URL with the token
	// in its query. Redirects are followed once and the final address is
	// reused until it expires.
	URL string
	// Client makes the requests; nil uses a default client. Its Timeout
	// is ignored (reads are bounded by an idle timeout instead).
	Client *http.Client
	// Header is sent to URL (not to the redirect target), for servers
	// that want the token in a header. Usually nil.
	Header http.Header
	// MediaSource is Emby's description of the file. It maps container
	// tracks to MediaStream indexes; nil leaves Track.Index at -1 and
	// AudioIndex then counts container audio tracks from 0.
	MediaSource *emby.MediaSource
	// AudioIndex is the Emby MediaStream index of the audio track to play;
	// -1 plays the container's default audio track. Only one audio track
	// is remuxed; switching tracks means opening another session.
	AudioIndex int
	// AudioTranscode permits an optional local FFmpeg fallback when the
	// selected audio track cannot be copied into fMP4. Only that audio
	// track is decoded; video stays stream-copy. The fallback always
	// downmixes to AAC-LC, 48 kHz stereo and pipes each requested segment
	// in memory. A missing FFmpeg fails the session instead of silently
	// playing video without its audio.
	AudioTranscode bool
	// NoAudio plays the video alone (for files whose audio AVPlayer cannot
	// decode). Callers should leave this false when the source has audio;
	// silently dropping a present track is not a compatible fallback.
	NoAudio bool
	// Duration is the server's length of the item, used when the file
	// does not say.
	Duration time.Duration
	// Start is where playback will start: the segment holding it is read
	// ahead while AVPlayer loads the playlist.
	Start time.Duration
	// OnCues receives subtitle cues of embedded text subtitle tracks, by
	// the track's Emby MediaStream index (Track.Index; -1-n for the n-th
	// subtitle track when MediaSource is nil). It is called on the
	// session's goroutines and must not block.
	OnCues func(track int, cues []emby.Cue)
}

// Track is a track of the remuxed file.
type Track struct {
	Kind TrackKind
	// Index is the Emby MediaStream index, -1 when unknown.
	Index int
	// Codec in ffprobe's names: h264, hevc, aac, ac3, eac3, dts, subrip,
	// ass, mov_text, hdmv_pgs_subtitle…
	Codec    string
	Language string
	Title    string
	Default  bool
	Forced   bool
	// Playing marks the video and audio track in the stream.
	Playing bool
	// Supported is false for tracks this package cannot copy, transcode or
	// extract (for example graphic subtitles without burn-in); Reason says
	// why.
	Supported bool
	Reason    string
}

// Stats are counters of a session, for diagnostics.
type Stats struct {
	// UpstreamBytes and UpstreamRequests count reads of the remote file.
	UpstreamBytes    int64
	UpstreamRequests int64
	// Segments counts segments produced (including read-ahead), Served
	// the bytes sent to AVPlayer.
	Segments int64
	Served   int64
}

// Mode is how an item can play.
type Mode int

// Modes.
const (
	// Unsupported: the file holds codecs AVPlayer cannot decode.
	Unsupported Mode = iota
	// DirectPlay: AVPlayer opens the file as it is.
	DirectPlay
	// Remux: a Session makes it playable.
	Remux
)

func (m Mode) String() string {
	switch m {
	case DirectPlay:
		return "direct"
	case Remux:
		return "remux"
	}
	return "unsupported"
}

// Decision is what Decide found.
type Decision struct {
	Mode Mode
	// Reason explains Remux and Unsupported, in English, for logs; the UI
	// shows its own words.
	Reason string
	// AudioIndex is the audio track that will play (an Emby MediaStream
	// index), -1 for none.
	AudioIndex int
	// AudioUnsupported is set when the video plays but the chosen audio
	// track is not one the caller can transcode. AudioTranscode is the
	// compatible fallback for common unsupported codecs.
	AudioUnsupported bool
	// AudioTranscode is set when the selected audio can play through the
	// optional local FFmpeg audio-only fallback; AudioIndex is then the
	// selected track, never -1 when the source has audio.
	AudioTranscode bool
}
