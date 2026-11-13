package remux

import (
	"html"
	"strings"
	"time"

	"mygo-emby/internal/emby"
)

// defaultCueDuration is used for subtitle blocks that carry no duration.
const defaultCueDuration = 3 * time.Second

// makeCue turns a subtitle packet into a plain-text cue.
func makeCue(p *packet) (emby.Cue, bool) {
	var text string
	switch p.t.codec {
	case "ass", "ssa":
		text = assText(string(p.data))
	case "mov_text":
		text = string(p.data)
	default:
		text = stripTags(string(p.data))
	}
	text = tidyLines(text)
	if text == "" {
		return emby.Cue{}, false
	}
	dur := p.dur
	if dur <= 0 {
		dur = defaultCueDuration
	}
	return emby.Cue{Start: p.pts, End: p.pts + dur, Text: text}, true
}

// assText returns the text of a Matroska ASS/SSA event ("ReadOrder,
// Layer, Style, Name, MarginL, MarginR, MarginV, Effect, Text"), without
// override tags and drawings.
func assText(ev string) string {
	for i := 0; i < 8; i++ {
		j := strings.IndexByte(ev, ',')
		if j < 0 {
			return ""
		}
		ev = ev[j+1:]
	}
	var b strings.Builder
	drawing := false
	for len(ev) > 0 {
		switch {
		case ev[0] == '{':
			end := strings.IndexByte(ev, '}')
			if end < 0 {
				ev = ""
				continue
			}
			tag := ev[1:end]
			if k := strings.Index(tag, `\p`); k >= 0 && k+2 < len(tag) {
				drawing = tag[k+2] != '0'
			}
			ev = ev[end+1:]
		case strings.HasPrefix(ev, `\N`), strings.HasPrefix(ev, `\n`):
			if !drawing {
				b.WriteByte('\n')
			}
			ev = ev[2:]
		case strings.HasPrefix(ev, `\h`):
			if !drawing {
				b.WriteByte(' ')
			}
			ev = ev[2:]
		default:
			if !drawing {
				b.WriteByte(ev[0])
			}
			ev = ev[1:]
		}
	}
	return b.String()
}

// stripTags removes HTML-like tags (<i>, <font …>, <c.x>) and ASS-style
// {…} overrides that SubRip files sometimes carry, and decodes entities.
func stripTags(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		switch s[0] {
		case '<':
			if end := strings.IndexByte(s, '>'); end > 0 {
				s = s[end+1:]
				continue
			}
		case '{':
			if end := strings.IndexByte(s, '}'); end > 0 && strings.HasPrefix(s, `{\`) {
				s = s[end+1:]
				continue
			}
		}
		b.WriteByte(s[0])
		s = s[1:]
	}
	return html.UnescapeString(b.String())
}

// tidyLines normalizes line ends and trims blank lines and spaces.
func tidyLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
