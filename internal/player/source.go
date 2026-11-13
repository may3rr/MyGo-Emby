package player

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mygo-emby/internal/emby"
)

// A Source turns an item into something AVPlayer opens. The player asks it
// on a goroutine, with a context it cancels when the user leaves or asks
// for another stream, and handles three kinds of answer:
//
//   - a Stream whose URL is the file itself (direct play) or a server's
//     HLS transcode;
//   - a Stream whose URL is a playlist served on this machine (a local
//     remux of a container AVPlayer does not read), with a Close that ends
//     the session, and, for its embedded text subtitles, CueFeeds that it
//     fills as it reads;
//   - an error: an *UnplayableError (errors.Is ErrUnplayable) when the
//     item cannot play here, which the player shows with its reason, or
//     any other error of the request.
type Source interface {
	Open(ctx context.Context, r Request) (*Stream, error)
}

// Request is what the player asks of a Source.
type Request struct {
	Item *emby.Item
	// Start is where playback starts. A server that transcodes starts
	// there; the player seeks a stream that starts elsewhere.
	Start time.Duration
	// MediaSourceID chooses a version; "" lets the server choose.
	MediaSourceID string
	// AudioIndex is the audio track to play; nil plays the source's
	// default one.
	AudioIndex *int
	// BurnIn is a bitmap subtitle to burn into the picture (only a server
	// transcode can); nil for none. Text subtitles are the player's own.
	BurnIn *int
	// MaxBitrate caps a server's transcode, in bits per second; 0 = none.
	MaxBitrate int64
}

// Stream is what the player plays.
type Stream struct {
	// URL is what AVPlayer opens: a file, an HLS playlist of the server,
	// or one served on this machine.
	URL string
	// Method is reported to the server: DirectPlay for the file, also
	// when this machine remuxes it, Transcode for the server's HLS.
	Method emby.PlayMethod
	// Playback is the server's answer, for the reports and the track
	// lists.
	Playback *emby.Playback
	// AudioIndex is the audio track the stream plays; BurnedIn the
	// subtitle burned in, -1 for none.
	AudioIndex int
	BurnedIn   int
	// ServerWork tells that seeking makes the server work (a transcode
	// restarts), so the player spaces seeks out.
	ServerWork bool
	// Playlist tells the URL is an HLS playlist (a server transcode or a
	// local remux). AVFoundation reads such an asset's tracks without
	// waiting, so the player shows it at once (see videoLoaded).
	Playlist bool
	// Embedded returns the feed of an embedded text subtitle that the
	// stream extracts itself, or nil when the server serves it (the
	// track's URL). Nil for streams that extract none.
	Embedded func(index int) *CueFeed
	// localSubtitles are real container subtitles found while opening this
	// stream. They fill in for missing server metadata; their IDs never
	// leave the player.
	localSubtitles []localSubtitle
	// Close releases what the stream holds on this machine; nil for none.
	// The player calls it once, on the main thread, after closing
	// AVPlayer; it must not block.
	Close func()
}

// localSubtitle is a subtitle enumerated by a local source. ID is an
// internal menu/selection index below -1; feedIndex is the index the source
// uses in its cue feed, which may differ when server metadata is missing.
type localSubtitle struct {
	ID        int
	FeedIndex int
	Track     emby.Track
	Reason    string
}

// subtitleChoices returns the tracks the menu should offer. Server metadata
// stays authoritative when it has any subtitle tracks, which preserves
// external URLs and avoids duplicate entries. Local container tracks are
// used only as the fallback for a source with no reported subtitle tracks.
func subtitleChoices(st *Stream, pb *emby.Playback) (tracks []emby.Track, fromServer bool) {
	if pb != nil && len(pb.SubtitleTracks) > 0 {
		return pb.SubtitleTracks, true
	}
	if st != nil && len(st.localSubtitles) > 0 {
		return localTrackList(st.localSubtitles), false
	}
	if pb != nil {
		return pb.SubtitleTracks, true
	}
	return nil, false
}

func localTrackList(local []localSubtitle) []emby.Track {
	tracks := make([]emby.Track, len(local))
	for i := range local {
		tracks[i] = local[i].Track
	}
	return tracks
}

func (s *Stream) localSubtitle(index int) (localSubtitle, bool) {
	if s == nil || index >= -1 {
		return localSubtitle{}, false
	}
	for i := range s.localSubtitles {
		if s.localSubtitles[i].ID == index {
			return s.localSubtitles[i], true
		}
	}
	return localSubtitle{}, false
}

func (s *Stream) close() {
	if s != nil && s.Close != nil {
		f := s.Close
		s.Close = nil
		f()
	}
}

// ErrUnplayable is what errors.Is finds in an *UnplayableError.
var ErrUnplayable = errors.New("player: not playable")

// UnplayableError says why an item cannot play here, in words for the user.
type UnplayableError struct {
	Reason string
	// AudioUnavailable and BurnInUnavailable tell that the item plays,
	// but not with the audio track or burned-in subtitle asked for, which
	// the player then drops.
	AudioUnavailable, BurnInUnavailable bool
}

func (e *UnplayableError) Error() string        { return e.Reason }
func (e *UnplayableError) Is(target error) bool { return target == ErrUnplayable }

// ServerSource opens what the server offers: the file when AVPlayer reads
// it, else the server's HLS transcode, else what Fallback makes of it.
type ServerSource struct {
	Client *emby.Client
	// Fallback, when set, opens a source that AVPlayer cannot read and
	// the server does not transcode, as a local remux does; p is the
	// server's answer. Without it such an item is unplayable.
	Fallback func(ctx context.Context, p *emby.Playback, r Request) (*Stream, error)
}

// Open implements Source.
func (s *ServerSource) Open(ctx context.Context, r Request) (*Stream, error) {
	if r.Item == nil {
		return nil, &UnplayableError{Reason: str.ErrNoStream}
	}
	p, err := s.Client.PlaybackInfo(ctx, r.Item.ID, s.options(r, false))
	if err != nil {
		if errors.Is(err, emby.ErrNotPlayable) {
			return nil, &UnplayableError{Reason: str.ErrNoStream}
		}
		return nil, err
	}
	audioOK := r.AudioIndex == nil || *r.AudioIndex == nativeAudio(&p.Source)
	burn := r.BurnIn != nil && *r.BurnIn >= 0
	switch {
	case p.Method == emby.Transcode:
		return serverStream(p, r), nil
	case p.NativelyPlayable && audioOK && !burn:
		return &Stream{URL: p.StreamURL, Method: p.Method, Playback: p, AudioIndex: nativeAudio(&p.Source), BurnedIn: -1}, nil
	case !burn && s.Fallback != nil:
		stream, localErr := s.Fallback(ctx, p, r)
		if localErr == nil {
			return stream, nil
		}
		if p.Source.SupportsTranscoding {
			pt, err := s.Client.PlaybackInfo(ctx, r.Item.ID, s.options(r, true))
			if err != nil {
				return nil, fmt.Errorf("%w; server transcode also failed: %v", localErr, err)
			}
			if pt.Method == emby.Transcode {
				return serverStream(pt, r), nil
			}
		}
		return nil, localErr
	}
	// The file as it is will not do: ask for the server's transcode.
	if p.Source.SupportsTranscoding {
		pt, err := s.Client.PlaybackInfo(ctx, r.Item.ID, s.options(r, true))
		if err != nil {
			return nil, err
		}
		if pt.Method == emby.Transcode {
			return serverStream(pt, r), nil
		}
	}
	switch {
	case !p.NativelyPlayable:
		return nil, &UnplayableError{Reason: str.ErrNotPlayable}
	case burn:
		return nil, &UnplayableError{Reason: str.ErrNotPlayable, BurnInUnavailable: true}
	default:
		return nil, &UnplayableError{Reason: str.ErrNotPlayable, AudioUnavailable: true}
	}
}

func (s *ServerSource) options(r Request, force bool) emby.PlaybackOptions {
	o := emby.PlaybackOptions{
		MediaSourceID:       r.MediaSourceID,
		AudioStreamIndex:    r.AudioIndex,
		StartTimeTicks:      emby.DurationToTicks(r.Start),
		MaxStreamingBitrate: r.MaxBitrate,
		ForceTranscode:      force,
		// The player draws text subtitles itself: ask for none, so that a
		// default bitmap subtitle does not make the server burn it in.
		SubtitleStreamIndex: emby.Index(-1),
	}
	if r.BurnIn != nil && *r.BurnIn >= 0 {
		o.SubtitleStreamIndex = r.BurnIn
	}
	return o
}

func serverStream(p *emby.Playback, r Request) *Stream {
	burned := -1
	if r.BurnIn != nil && *r.BurnIn >= 0 {
		burned = *r.BurnIn
	}
	return &Stream{URL: p.StreamURL, Method: emby.Transcode, Playback: p, AudioIndex: p.AudioStreamIndex,
		BurnedIn: burned, ServerWork: true, Playlist: p.IsHLS}
}

// nativeAudio is the audio track AVPlayer plays of a file: the default
// one, else the first.
func nativeAudio(src *emby.MediaSource) int {
	first := -1
	for _, s := range src.MediaStreams {
		if s.Type != emby.StreamAudio {
			continue
		}
		if s.IsDefault {
			return s.Index
		}
		if first < 0 {
			first = s.Index
		}
	}
	if first < 0 && src.DefaultAudioStreamIndex != nil {
		return *src.DefaultAudioStreamIndex
	}
	return first
}
