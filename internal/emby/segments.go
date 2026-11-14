package emby

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SegmentType classifies a skippable media segment.
type SegmentType string

const (
	SegmentIntro      SegmentType = "Intro"
	SegmentOutro      SegmentType = "Outro"
	SegmentRecap      SegmentType = "Recap"
	SegmentPreview    SegmentType = "Preview"
	SegmentCommercial SegmentType = "Commercial"
)

// MediaSegment is a timed region of an item (intro, credits, recap…).
type MediaSegment struct {
	ID         string      `json:"Id,omitempty"`
	ItemID     string      `json:"ItemId,omitempty"`
	Type       SegmentType `json:"Type"`
	StartTicks int64       `json:"StartTicks"`
	EndTicks   int64       `json:"EndTicks"`
}

// Start returns the segment start position.
func (s MediaSegment) Start() time.Duration { return TicksToDuration(s.StartTicks) }

// End returns the segment end position.
func (s MediaSegment) End() time.Duration { return TicksToDuration(s.EndTicks) }

// Contains reports whether pos lies inside [Start, End-500ms).
func (s MediaSegment) Contains(pos time.Duration) bool {
	start, end := s.Start(), s.End()
	if end <= start+time.Second {
		return false
	}
	return pos >= start && pos < end-500*time.Millisecond
}

// Skippable reports whether the segment represents a user-skippable section.
func (s MediaSegment) Skippable() bool {
	switch s.Type {
	case SegmentIntro, SegmentOutro, SegmentRecap, SegmentPreview, SegmentCommercial:
		return s.EndTicks > s.StartTicks+DurationToTicks(2*time.Second)
	}
	return false
}

// MediaSegments queries skippable segments for itemID (/MediaSegments/{id}).
// Servers that do not implement the endpoint (HTTP 400/404/405) return nil, nil.
func (c *Client) MediaSegments(ctx context.Context, itemID string) ([]MediaSegment, error) {
	if itemID == "" {
		return nil, nil
	}
	if _, err := c.requireUser("GET /MediaSegments/{id}"); err != nil {
		return nil, err
	}
	var env struct {
		Items []MediaSegment `json:"Items"`
	}
	err := c.do(ctx, http.MethodGet, "/MediaSegments/"+url.PathEscape(itemID), nil, nil, &env)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && (e.Kind == KindNotFound || e.StatusCode == http.StatusBadRequest || e.StatusCode == http.StatusMethodNotAllowed) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]MediaSegment, 0, len(env.Items))
	for _, seg := range env.Items {
		seg.Type = normalizeSegmentType(string(seg.Type))
		if seg.Skippable() {
			out = append(out, seg)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartTicks < out[j].StartTicks
	})
	return out, nil
}

func normalizeSegmentType(raw string) SegmentType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "intro", "introduction", "opening":
		return SegmentIntro
	case "outro", "credits", "ending", "endcredits":
		return SegmentOutro
	case "recap":
		return SegmentRecap
	case "preview":
		return SegmentPreview
	case "commercial":
		return SegmentCommercial
	default:
		return SegmentType(raw)
	}
}

// SegmentsFromChapters infers skippable segments from chapter markers when a
// server has no MediaSegments plugin. Each matched chapter ends at the next
// chapter's start (or runtime for the final chapter), subject to sanity bounds.
func SegmentsFromChapters(chapters []Chapter, runtime time.Duration) []MediaSegment {
	if len(chapters) == 0 {
		return nil
	}
	sorted := make([]Chapter, len(chapters))
	copy(sorted, chapters)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].StartPositionTicks < sorted[j].StartPositionTicks
	})

	var out []MediaSegment
	rtTicks := DurationToTicks(runtime)
	for i, ch := range sorted {
		typ, ok := classifyChapterSegment(ch.Name)
		if !ok {
			continue
		}
		start := ch.StartPositionTicks
		var end int64
		if i+1 < len(sorted) {
			end = sorted[i+1].StartPositionTicks
		} else if rtTicks > start {
			end = rtTicks
		}
		dur := TicksToDuration(end - start)
		if dur < 3*time.Second {
			continue
		}
		maxDur := 6 * time.Minute
		if typ == SegmentOutro {
			maxDur = 15 * time.Minute
		}
		if dur > maxDur {
			continue
		}
		out = append(out, MediaSegment{
			Type:       typ,
			StartTicks: start,
			EndTicks:   end,
		})
	}
	return out
}

func classifyChapterSegment(name string) (SegmentType, bool) {
	s := strings.TrimSpace(strings.ToLower(name))
	if s == "" {
		return "", false
	}
	for _, sub := range []string{"片头", "主题曲", "オープニング"} {
		if strings.Contains(s, sub) {
			return SegmentIntro, true
		}
	}
	for _, sub := range []string{"前情", "回顾", "アバン"} {
		if strings.Contains(s, sub) {
			return SegmentRecap, true
		}
	}
	for _, sub := range []string{"下集预告", "次回予告", "预告"} {
		if strings.Contains(s, sub) {
			return SegmentPreview, true
		}
	}
	for _, sub := range []string{"片尾", "演职员", "エンディング"} {
		if strings.Contains(s, sub) {
			return SegmentOutro, true
		}
	}

	words := splitChapterWords(s)
	hasWord := func(targets ...string) bool {
		for _, w := range words {
			for _, t := range targets {
				if w == t {
					return true
				}
			}
		}
		return false
	}
	switch {
	case hasWord("recap", "previously"):
		return SegmentRecap, true
	case hasWord("intro", "opening", "op", "op1", "op2"):
		return SegmentIntro, true
	case hasWord("outro", "ending", "ed", "ed1", "ed2", "credits"):
		return SegmentOutro, true
	case hasWord("preview"):
		return SegmentPreview, true
	}
	return "", false
}

func splitChapterWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}
