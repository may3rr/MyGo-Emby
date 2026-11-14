package player

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"mygo-emby/internal/emby"
)

func selectionSource(t *testing.T, handler func(http.ResponseWriter, *http.Request)) *ServerSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(srv.Close)
	c, err := emby.RestoreClient(emby.Session{ServerURL: srv.URL, UserID: "user", Token: "test"},
		emby.DeviceInfo{Client: "test", Device: "test", DeviceID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return &ServerSource{Client: c}
}

func selectionSources() []emby.MediaSource {
	return []emby.MediaSource{
		{ID: "iso", Container: "blurayiso", SupportsDirectPlay: true},
		{ID: "mkv", Container: "mkv", SupportsDirectPlay: true, MediaStreams: []emby.MediaStream{
			{Index: 0, Type: emby.StreamVideo, Codec: "hevc"},
			{Index: 7, Type: emby.StreamAudio, Codec: "aac", Language: "chi", IsDefault: true},
			{Index: 9, Type: emby.StreamSubtitle, Codec: "subrip", Language: "chi"},
		}},
	}
}

func TestServerSourceAutoSelectionRetriesNextVersion(t *testing.T) {
	var calls, tried []string
	s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("MediaSourceId")
		calls = append(calls, id)
		_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{
			MediaSources: selectionSources(), PlaySessionID: "session-" + id,
		})
	})
	s.Fallback = func(_ context.Context, pb *emby.Playback, r Request) (*Stream, error) {
		tried = append(tried, pb.MediaSourceID)
		if pb.MediaSourceID == "iso" {
			return nil, &UnplayableError{Reason: "ISO remux unsupported"}
		}
		return &Stream{URL: pb.StreamURL, Playback: pb, AudioIndex: 7, BurnedIn: -1}, nil
	}
	st, err := s.Open(context.Background(), Request{Item: movie("item", 0)})
	if err != nil {
		t.Fatalf("empty MediaSourceID should retry MKV after ISO fails; tried=%v; calls=%v; got=%v", tried, calls, err)
	}
	if !slices.Equal(calls, []string{"", "mkv"}) || !slices.Equal(tried, []string{"iso", "mkv"}) {
		t.Fatalf("calls=%v, tried=%v", calls, tried)
	}
	if st.Playback.MediaSourceID != "mkv" || st.Playback.PlaySessionID != "session-mkv" {
		t.Fatalf("wrong final playback: %+v", st.Playback)
	}
	if len(st.Playback.AudioTracks) != 1 || st.Playback.AudioTracks[0].Index != 7 ||
		len(st.Playback.SubtitleTracks) != 1 || st.Playback.SubtitleTracks[0].Index != 9 {
		t.Fatalf("tracks do not belong to the selected version: %+v", st.Playback)
	}
	report := st.Playback.Report(0, false)
	if report.MediaSourceID != "mkv" || report.PlaySessionID != "session-mkv" {
		t.Fatalf("wrong report: %+v", report)
	}
}

func TestServerSourceAutoSelectionSkipsSourceWithoutPlaybackPath(t *testing.T) {
	var calls []string
	s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("MediaSourceId"))
		sources := selectionSources()
		sources[0].SupportsDirectPlay = false
		_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{MediaSources: sources, PlaySessionID: "final"})
	})
	s.Fallback = func(_ context.Context, pb *emby.Playback, _ Request) (*Stream, error) {
		return &Stream{URL: pb.StreamURL, Playback: pb, AudioIndex: 7, BurnedIn: -1}, nil
	}
	st, err := s.Open(context.Background(), Request{Item: movie("item", 0)})
	if err != nil || st.Playback.MediaSourceID != "mkv" || !slices.Equal(calls, []string{"", "mkv"}) {
		t.Fatalf("unusable first source hid the next version: stream=%+v, err=%v, calls=%v", st, err, calls)
	}
}

func TestServerSourceSelectionDoesNotRetryExplicitTracksOrRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		err  error
	}{
		{"explicit source", Request{MediaSourceID: "iso"}, &UnplayableError{Reason: "ISO"}},
		{"explicit audio", Request{AudioIndex: emby.Index(2)}, &UnplayableError{Reason: "audio"}},
		{"explicit text subtitle", Request{SubtitleIndex: emby.Index(4)}, &UnplayableError{Reason: "subtitle"}},
		{"explicit burn in", Request{BurnIn: emby.Index(3)}, &UnplayableError{Reason: "subtitle"}},
		{"cancelled", Request{}, context.Canceled},
		{"timeout", Request{}, context.DeadlineExceeded},
		{"network", Request{}, &emby.Error{Kind: emby.KindUnreachable}},
		{"unauthorized", Request{}, &emby.Error{Kind: emby.KindUnauthorized, StatusCode: 401}},
		{"server error", Request{}, &emby.Error{Kind: emby.KindHTTP, StatusCode: 500}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{MediaSources: selectionSources()})
			})
			s.Fallback = func(context.Context, *emby.Playback, Request) (*Stream, error) { return nil, tc.err }
			tc.req.Item = movie("item", 0)
			_, err := s.Open(context.Background(), tc.req)
			if err == nil || calls != 1 {
				t.Fatalf("err=%v, calls=%d; want failure without switching sources", err, calls)
			}
			if tc.req.BurnIn == nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost original error: %v", err)
			}
		})
	}
}

func TestServerSourceSelectionIsBoundedAndKeepsFirstError(t *testing.T) {
	var calls []string
	s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Query().Get("MediaSourceId"))
		sources := append(selectionSources(), selectionSources()...)
		sources = append(sources, emby.MediaSource{})
		_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{MediaSources: sources})
	})
	firstErr := &UnplayableError{Reason: "ISO not supported"}
	s.Fallback = func(_ context.Context, p *emby.Playback, _ Request) (*Stream, error) {
		if p.MediaSourceID == "iso" {
			return nil, firstErr
		}
		return nil, &UnplayableError{Reason: "unsupported video"}
	}
	_, err := s.Open(context.Background(), Request{Item: movie("item", 0)})
	if err != firstErr || !slices.Equal(calls, []string{"", "mkv"}) {
		t.Fatalf("err=%v, calls=%v", err, calls)
	}
}

func TestServerSourceSelectionRejectsWrongVersion(t *testing.T) {
	var calls []string
	s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("MediaSourceId")
		calls = append(calls, id)
		sources := selectionSources()
		if id != "" {
			sources = sources[:1] // server ignored the requested MKV ID
		}
		_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{MediaSources: sources})
	})
	fallbacks := 0
	s.Fallback = func(context.Context, *emby.Playback, Request) (*Stream, error) {
		fallbacks++
		return nil, &UnplayableError{Reason: "ISO"}
	}
	_, err := s.Open(context.Background(), Request{Item: movie("item", 0)})
	if !errors.Is(err, emby.ErrBadResponse) || fallbacks != 1 || !slices.Equal(calls, []string{"", "mkv"}) {
		t.Fatalf("err=%v, calls=%v, fallbacks=%d", err, calls, fallbacks)
	}
}

func TestServerSourceTranscodeKeepsVersionAndRequestErrors(t *testing.T) {
	for _, status := range []int{200, 401, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls []string
			s := selectionSource(t, func(w http.ResponseWriter, r *http.Request) {
				id := r.URL.Query().Get("MediaSourceId")
				calls = append(calls, id)
				sources := selectionSources()
				sources[0].SupportsTranscoding = true
				if len(calls) == 2 {
					if status != 200 {
						http.Error(w, "failed", status)
						return
					}
					sources[0].SupportsDirectPlay = false
					sources[0].TranscodingURL = "/Videos/item/master.m3u8"
				}
				_ = json.NewEncoder(w).Encode(emby.PlaybackInfoResponse{MediaSources: sources})
			})
			localErr := &UnplayableError{Reason: "ISO unsupported"}
			s.Fallback = func(context.Context, *emby.Playback, Request) (*Stream, error) { return nil, localErr }
			st, err := s.Open(context.Background(), Request{Item: movie("item", 0)})
			if !slices.Equal(calls, []string{"", "iso"}) {
				t.Fatalf("transcode changed version or retried a request failure: %v", calls)
			}
			if status == 200 {
				if err != nil || st.Method != emby.Transcode {
					t.Fatalf("stream=%+v, err=%v", st, err)
				}
			} else if !errors.Is(err, localErr) || emby.StatusCode(err) != status {
				t.Fatalf("lost local or server error: %v", err)
			}
		})
	}
}
