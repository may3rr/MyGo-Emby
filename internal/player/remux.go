package player

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/remux"
)

// RemuxFallback returns a ServerSource.Fallback that plays what AVPlayer
// cannot read as it is (Matroska, MPEG-TS, hev1 MP4, a non-default audio
// track) through a local remux session of srv. Embedded text subtitles of
// the session reach the player through CueFeeds, one per track.
func RemuxFallback(srv *remux.Server) func(ctx context.Context, p *emby.Playback, r Request) (*Stream, error) {
	return func(ctx context.Context, p *emby.Playback, r Request) (*Stream, error) {
		want := -1
		if r.AudioIndex != nil {
			want = *r.AudioIndex
		}
		d := remux.Decide(&p.Source, want)
		switch {
		case d.Mode == remux.Unsupported:
			return nil, unsupportedRemuxError(d, &p.Source)
		case d.Mode == remux.DirectPlay:
			return &Stream{URL: p.StreamURL, Method: p.Method, Pipeline: str.PipeDirect, Playback: p, AudioIndex: d.AudioIndex, BurnedIn: -1}, nil
		}

		feeds := &cueFeeds{m: map[int]*CueFeed{}}
		// With no stream metadata Decide cannot know the audio codec. Allow
		// the session to use its optional decoder only if probing finds that
		// the track cannot be copied; copyable audio never starts FFmpeg.
		allowAudioTranscode := d.AudioTranscode || len(p.Source.MediaStreams) == 0
		sess, err := srv.Open(ctx, remux.Source{
			URL:            p.StreamURL,
			MediaSource:    &p.Source,
			AudioIndex:     d.AudioIndex,
			AudioTranscode: allowAudioTranscode,
			Duration:       p.Runtime(),
			Start:          r.Start,
			OnCues: func(track int, cues []emby.Cue) {
				feeds.get(track).Add(cues...)
			},
		})
		if err != nil {
			if errors.Is(err, remux.ErrAudioTranscoderUnavailable) {
				return nil, &UnplayableError{Reason: str.ErrAudioTranscoder}
			}
			if errors.Is(err, remux.ErrUnsupportedFormat) || errors.Is(err, remux.ErrUnsupportedVideo) ||
				errors.Is(err, remux.ErrUnsupportedAudio) {
				return nil, unsupportedRemuxSessionError(err, &p.Source)
			}
			return nil, err
		}
		local := localSubtitles(sess.Tracks)
		embedded := map[int]int{}
		for _, t := range sess.Tracks {
			if t.Kind != remux.KindSubtitle || !t.Supported {
				continue
			}
			if t.Index >= 0 {
				embedded[t.Index] = t.Index
			}
		}
		for _, t := range local {
			if t.Track.IsText {
				embedded[t.ID] = t.FeedIndex
			}
		}
		pipe := str.PipeLocalRemux
		if sess.AudioTranscoded() {
			pipe = str.PipeLocalRemuxAudio
		}
		return &Stream{
			URL:            sess.PlaylistURL,
			Method:         emby.DirectPlay, // the server sends the file as it is
			Pipeline:       pipe,
			Playback:       p,
			AudioIndex:     sess.AudioIndex,
			BurnedIn:       -1,
			Playlist:       true,
			localSubtitles: local,
			Embedded: func(index int) *CueFeed {
				feedIndex, ok := embedded[index]
				if !ok {
					return nil // an external subtitle: the server serves it
				}
				return feeds.get(feedIndex)
			},
			Close: func() {
				feeds.close()
				go sess.Close() // must not block the main thread
			},
		}, nil
	}
}

func unsupportedRemuxError(d remux.Decision, source *emby.MediaSource) *UnplayableError {
	cause := d.Err
	if cause != nil && d.Reason != "" {
		cause = fmt.Errorf("%w: %s", cause, d.Reason)
	}
	reason := str.ErrNotPlayable
	if errors.Is(d.Err, remux.ErrUnsupportedFormat) && isDiscImage(source) {
		reason = str.ErrDiscImage
	}
	return &UnplayableError{Reason: reason, Err: cause}
}

func unsupportedRemuxSessionError(err error, source *emby.MediaSource) *UnplayableError {
	reason := str.ErrNotPlayable
	if errors.Is(err, remux.ErrUnsupportedFormat) && isDiscImage(source) {
		reason = str.ErrDiscImage
	}
	return &UnplayableError{Reason: reason, Err: err}
}

func isDiscImage(source *emby.MediaSource) bool {
	if source == nil {
		return false
	}
	for _, container := range strings.Split(strings.ToLower(source.Container), ",") {
		switch strings.TrimSpace(container) {
		case "iso", "blurayiso", "dvdiso", "bluray", "dvd":
			return true
		}
	}
	return false
}

// localSubtitles converts the session's real container tracks to stable
// player-only entries. A session reports -1 for every subtitle when the
// server omitted its stream metadata; the cue feed still keys those tracks
// as -1-n in subtitle order, so both indexes are derived here.
func localSubtitles(tracks []remux.Track) []localSubtitle {
	var out []localSubtitle
	for _, t := range tracks {
		if t.Kind != remux.KindSubtitle {
			continue
		}
		n := len(out)
		feedIndex := t.Index
		if feedIndex < 0 {
			feedIndex = -1 - n
		}
		reason := ""
		if !t.Supported {
			reason = str.BurnInUnavailable
		}
		index := -2 - n
		out = append(out, localSubtitle{
			ID:        index,
			FeedIndex: feedIndex,
			Track: emby.Track{
				Index:          index,
				Type:           emby.StreamSubtitle,
				Codec:          t.Codec,
				Language:       t.Language,
				Title:          t.Title,
				IsDefault:      t.Default,
				IsForced:       t.Forced,
				IsText:         t.Supported,
				NeedsBurnIn:    !t.Supported,
				DeliveryMethod: emby.DeliveryEmbed,
			},
			Reason: reason,
		})
	}
	return out
}

// cueFeeds holds a session's feeds by track, made as the first cue of a
// track comes or the player first asks for it, whichever is first.
type cueFeeds struct {
	mu sync.Mutex
	m  map[int]*CueFeed
}

func (c *cueFeeds) get(track int) *CueFeed {
	c.mu.Lock()
	defer c.mu.Unlock()
	f := c.m[track]
	if f == nil {
		f = NewCueFeed()
		c.m[track] = f
	}
	return f
}

func (c *cueFeeds) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, f := range c.m {
		f.Close()
	}
}
