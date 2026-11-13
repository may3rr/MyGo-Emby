package emby

import (
	"bytes"
	"context"
	"errors"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cue is one subtitle cue with plain text (markup removed). Multi-line
// cues keep "\n" between lines.
type Cue struct {
	Start time.Duration
	End   time.Duration
	Text  string
}

// Subtitles is a parsed subtitle track: cues sorted by start time, with an
// index for fast lookup by playback position. The zero value is an empty
// track. It is immutable and safe for concurrent reads.
type Subtitles struct {
	Cues   []Cue
	maxEnd []time.Duration // maxEnd[i] = max(Cues[0..i].End)
}

// NewSubtitles sorts cues by start time and builds the lookup index.
// The slice is taken over by the result.
func NewSubtitles(cues []Cue) *Subtitles {
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].Start != cues[j].Start {
			return cues[i].Start < cues[j].Start
		}
		return cues[i].End < cues[j].End
	})
	s := &Subtitles{Cues: cues, maxEnd: make([]time.Duration, len(cues))}
	var m time.Duration
	for i, c := range cues {
		m = max(m, c.End)
		s.maxEnd[i] = m
	}
	return s
}

// Len returns the number of cues.
func (s *Subtitles) Len() int {
	if s == nil {
		return 0
	}
	return len(s.Cues)
}

// At returns the cues active at t (Start <= t < End), in start order.
// It runs in O(log n + k) using binary search over start times and the
// running maximum of end times, so overlapping cues are handled.
func (s *Subtitles) At(t time.Duration) []Cue {
	if s == nil || len(s.Cues) == 0 {
		return nil
	}
	// hi: first cue starting after t.
	hi := sort.Search(len(s.Cues), func(i int) bool { return s.Cues[i].Start > t })
	var out []Cue
	for i := hi - 1; i >= 0 && s.maxEnd[i] > t; i-- {
		if s.Cues[i].End > t {
			out = append(out, s.Cues[i])
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// TextAt returns the text of the cues active at t joined by newlines, or "".
func (s *Subtitles) TextAt(t time.Duration) string {
	cues := s.At(t)
	switch len(cues) {
	case 0:
		return ""
	case 1:
		return cues[0].Text
	}
	parts := make([]string, len(cues))
	for i, c := range cues {
		parts[i] = c.Text
	}
	return strings.Join(parts, "\n")
}

// ErrNoCues is returned by ParseSubtitles when the input has no cues.
// Some servers answer a bare "WEBVTT" header when they cannot extract an
// embedded subtitle.
var ErrNoCues = errors.New("emby: subtitle has no cues")

// ParseSubtitles parses WebVTT or SRT (detected automatically) into a
// Subtitles track. Markup (<i>, <b>, <font>, <c.x>, <v Name>, karaoke
// timestamps, ASS override blocks such as {\an8}) and cue settings
// (position:, align:…) are stripped; HTML entities are decoded. Malformed
// cues are skipped. It returns ErrNoCues (and an empty track) when nothing
// usable was found.
func ParseSubtitles(data []byte) (*Subtitles, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var cues []Cue
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "-->") {
			continue
		}
		// Skip WebVTT NOTE/STYLE/REGION blocks: they never contain "-->"
		// on a line of their own except by accident, so no special casing
		// is needed beyond requiring a valid timing line.
		start, end, ok := parseTimingLine(line)
		if !ok {
			continue
		}
		var body []string
		for i+1 < len(lines) {
			next := lines[i+1]
			if strings.TrimSpace(next) == "" {
				break
			}
			if strings.Contains(next, "-->") {
				if _, _, ok := parseTimingLine(strings.TrimSpace(next)); ok {
					break // cue without blank separator (sloppy SRT)
				}
			}
			body = append(body, next)
			i++
		}
		// Sloppy SRT without blank separator leaves the next cue's numeric
		// id as the last body line; drop it.
		if n := len(body); n > 0 && i+1 < len(lines) && strings.Contains(lines[i+1], "-->") {
			if _, err := strconv.Atoi(strings.TrimSpace(body[n-1])); err == nil {
				body = body[:n-1]
			}
		}
		txt := cleanCueText(strings.Join(body, "\n"))
		if txt == "" || end <= start {
			continue
		}
		cues = append(cues, Cue{Start: start, End: end, Text: txt})
	}
	s := NewSubtitles(cues)
	if len(cues) == 0 {
		return s, ErrNoCues
	}
	return s, nil
}

// parseTimingLine parses "00:01.000 --> 00:04.000 align:start" (WebVTT) or
// "00:00:01,000 --> 00:00:04,000 X1:…" (SRT).
func parseTimingLine(line string) (start, end time.Duration, ok bool) {
	a, b, found := strings.Cut(line, "-->")
	if !found {
		return 0, 0, false
	}
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if f := strings.Fields(b); len(f) > 0 {
		b = f[0] // drop cue settings
	} else {
		return 0, 0, false
	}
	if f := strings.Fields(a); len(f) > 0 {
		a = f[len(f)-1]
	} else {
		return 0, 0, false
	}
	start, ok1 := parseTimestamp(a)
	end, ok2 := parseTimestamp(b)
	return start, end, ok1 && ok2
}

// parseTimestamp parses [hh:]mm:ss[.,]fff with 1-3+ fraction digits.
func parseTimestamp(s string) (time.Duration, bool) {
	s = strings.ReplaceAll(s, ",", ".")
	main, frac, _ := strings.Cut(s, ".")
	parts := strings.Split(main, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var h, m, sec int
	var err error
	nums := make([]int, len(parts))
	for i, p := range parts {
		if nums[i], err = strconv.Atoi(strings.TrimSpace(p)); err != nil || nums[i] < 0 {
			return 0, false
		}
	}
	if len(nums) == 3 {
		h, m, sec = nums[0], nums[1], nums[2]
	} else {
		m, sec = nums[0], nums[1]
	}
	var ms int
	if frac != "" {
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		if ms, err = strconv.Atoi(frac); err != nil {
			return 0, false
		}
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute +
		time.Duration(sec)*time.Second + time.Duration(ms)*time.Millisecond, true
}

// cleanCueText strips markup from cue text and normalizes whitespace.
func cleanCueText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		switch s[i] {
		case '<':
			if j := strings.IndexByte(s[i:], '>'); j > 0 {
				i += j + 1
				continue
			}
		case '{':
			// ASS override block like {\an8} or {\i1}.
			if i+1 < len(s) && s[i+1] == '\\' {
				if j := strings.IndexByte(s[i:], '}'); j > 0 {
					i += j + 1
					continue
				}
			}
		case '\\':
			// ASS escapes surviving conversion: \N \n hard break, \h space.
			if i+1 < len(s) {
				switch s[i+1] {
				case 'N', 'n':
					b.WriteByte('\n')
					i += 2
					continue
				case 'h':
					b.WriteByte(' ')
					i += 2
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	out := html.UnescapeString(b.String())
	out = strings.Map(func(r rune) rune {
		switch r {
		case '\u200e', '\u200f', '\ufeff':
			return -1
		case '\u00a0':
			return ' '
		}
		return r
	}, out)
	lines := strings.Split(out, "\n")
	kept := lines[:0]
	for _, l := range lines {
		l = strings.Join(strings.Fields(l), " ")
		if l != "" {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

// maxSubtitleBytes caps subtitle downloads.
const maxSubtitleBytes = 16 << 20

// FetchSubtitles downloads a subtitle URL (as found in Track.URL or built by
// SubtitleURL) and parses it. A track the server could not extract yields
// ErrNoCues together with an empty, usable *Subtitles.
func (c *Client) FetchSubtitles(ctx context.Context, subtitleURL string) (*Subtitles, error) {
	if subtitleURL == "" {
		return nil, &Error{Kind: KindInvalidURL, Op: "fetch subtitles", Message: "empty subtitle url"}
	}
	data, err := c.fetchRaw(ctx, subtitleURL, maxSubtitleBytes)
	if err != nil {
		return nil, err
	}
	return ParseSubtitles(data)
}
