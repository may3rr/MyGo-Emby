package emby

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleVTT = "\xef\xbb\xbfWEBVTT - Some title\r\n" +
	"Kind: captions\r\n\r\n" +
	"STYLE\r\n::cue { color: yellow }\r\n\r\n" +
	"NOTE this is a comment\r\nspanning lines\r\n\r\n" +
	"1\r\n00:00:01.000 --> 00:00:04.000 position:10%,line-left align:left size:35%\r\n" +
	"<i>Hello</i> <b>world</b> &amp; friends\r\n\r\n" +
	"intro-2\r\n00:03.500 --> 00:05.250\r\n" +
	"<v Roger Bingham>We are in New York City</v>\r\n<c.yellow>second</c> line\r\n\r\n" +
	"01:00:00.000 --> 01:00:02.000 line:0\r\n{\\an8}Top &lt;sign&gt;\r\n\r\n" +
	"00:00:10.000 --> 00:00:09.000\r\nbackwards, dropped\r\n\r\n" +
	"00:00:11.000 --> 00:00:12.000\r\n<00:00:11.500>karaoke<00:00:11.800>text\r\n"

func TestParseWebVTT(t *testing.T) {
	s, err := ParseSubtitles([]byte(sampleVTT))
	if err != nil {
		t.Fatal(err)
	}
	want := []Cue{
		{time.Second, 4 * time.Second, "Hello world & friends"},
		{3500 * time.Millisecond, 5250 * time.Millisecond, "We are in New York City\nsecond line"},
		{11 * time.Second, 12 * time.Second, "karaoketext"},
		{time.Hour, time.Hour + 2*time.Second, "Top <sign>"},
	}
	if len(s.Cues) != len(want) {
		t.Fatalf("got %d cues: %#v", len(s.Cues), s.Cues)
	}
	for i := range want {
		if s.Cues[i] != want[i] {
			t.Errorf("cue %d = %#v\nwant %#v", i, s.Cues[i], want[i])
		}
	}
}

const sampleSRT = `1
00:00:01,600 --> 00:00:04,200
{\an8}<font color="#ffff00">Top line</font>

2
00:00:05,900 --> 00:00:07,999 X1:100 X2:200 Y1:10 Y2:20
- Who's there?\N- Me.
3
00:00:08,1 --> 00:00:09,50
Short fractions

`

func TestParseSRT(t *testing.T) {
	s, err := ParseSubtitles([]byte(sampleSRT))
	if err != nil {
		t.Fatal(err)
	}
	want := []Cue{
		{1600 * time.Millisecond, 4200 * time.Millisecond, "Top line"},
		{5900 * time.Millisecond, 7999 * time.Millisecond, "- Who's there?\n- Me."},
		{8100 * time.Millisecond, 9500 * time.Millisecond, "Short fractions"},
	}
	if len(s.Cues) != len(want) {
		t.Fatalf("got %d cues: %#v", len(s.Cues), s.Cues)
	}
	for i := range want {
		if s.Cues[i] != want[i] {
			t.Errorf("cue %d = %#v\nwant %#v", i, s.Cues[i], want[i])
		}
	}
}

func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "WEBVTT\n\n", "garbage\nno cues"} {
		s, err := ParseSubtitles([]byte(in))
		if !errors.Is(err, ErrNoCues) || s == nil || s.Len() != 0 || s.At(time.Second) != nil {
			t.Errorf("%q: %v %v", in, s, err)
		}
	}
	var nilSubs *Subtitles
	if nilSubs.At(0) != nil || nilSubs.Len() != 0 {
		t.Error("nil Subtitles should be empty")
	}
}

func TestSubtitlesAt(t *testing.T) {
	sec := func(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }
	s := NewSubtitles([]Cue{
		{sec(10), sec(12), "C"},
		{sec(0), sec(100), "long"}, // overlaps everything
		{sec(1), sec(3), "A"},
		{sec(2), sec(4), "B"},
		{sec(20), sec(21), "D"},
	})
	cases := []struct {
		t    float64
		want []string
	}{
		{0.5, []string{"long"}},
		{1, []string{"long", "A"}},
		{2.5, []string{"long", "A", "B"}},
		{3, []string{"long", "B"}},
		{11.999, []string{"long", "C"}},
		{12, []string{"long"}},
		{20.5, []string{"long", "D"}},
		{100, nil},
		{-1, nil},
	}
	for _, c := range cases {
		got := s.At(sec(c.t))
		var names []string
		for _, q := range got {
			names = append(names, q.Text)
		}
		if len(names) != len(c.want) {
			t.Errorf("At(%v) = %v, want %v", c.t, names, c.want)
			continue
		}
		for i := range names {
			if names[i] != c.want[i] {
				t.Errorf("At(%v) = %v, want %v", c.t, names, c.want)
				break
			}
		}
	}
	if got := s.TextAt(sec(2.5)); got != "long\nA\nB" {
		t.Errorf("TextAt = %q", got)
	}

	// Brute-force cross-check on a larger random-ish track.
	var cues []Cue
	for i := range 500 {
		st := time.Duration((i*7919)%3000) * 100 * time.Millisecond
		cues = append(cues, Cue{st, st + time.Duration(1+i%37)*time.Second, "x"})
	}
	big := NewSubtitles(cues)
	for ms := 0; ms < 310_000; ms += 777 {
		at := time.Duration(ms) * time.Millisecond
		n := 0
		for _, c := range big.Cues {
			if c.Start <= at && at < c.End {
				n++
			}
		}
		if got := len(big.At(at)); got != n {
			t.Fatalf("At(%v) returned %d cues, brute force %d", at, got, n)
		}
	}
}

func TestFetchSubtitles(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("api_key") + "|" + r.URL.Query().Get("ApiKey") + "|" + r.Header.Get("X-Emby-Token")
		if r.URL.Path == "/Videos/i1/ms1/Subtitles/3/Stream.vtt" {
			w.Header().Set("Content-Type", "text/vtt")
			io.WriteString(w, "WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n")
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c, _ := RestoreClient(Session{ServerURL: srv.URL, UserID: "u", Token: "tk"}, testDevice)
	s, err := c.FetchSubtitles(context.Background(), c.SubtitleURL("i1", "ms1", 3))
	if err != nil || s.Len() != 1 || s.TextAt(1500*time.Millisecond) != "hi" {
		t.Fatalf("FetchSubtitles = %v, %v", s, err)
	}
	if gotToken != "tk|tk|tk" {
		t.Errorf("token transport = %q", gotToken)
	}
	if _, err := c.FetchSubtitles(context.Background(), c.SubtitleURL("i1", "ms1", 4)); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing subtitle: %v", err)
	}
}
