package player

import (
	"strings"

	"mygo-emby/internal/emby"
)

// Language preferences ("" = the server's default) are matched against a
// track's language code and title. Servers report ISO 639-2 codes ("chi",
// "zho", "eng"), sometimes 639-1 or tags ("zh", "zh-Hans"), sometimes
// "und" with the language only in the title ("简体中文", "English"). Both
// sides are reduced to one key per language.

// SubtitlesOff, as Config.SubtitleLang, turns subtitles off by default.
const SubtitlesOff = "off"

var langKeys = map[string]string{
	"zh": "zh", "chi": "zh", "zho": "zh", "chs": "zh", "cht": "zh", "cmn": "zh", "yue": "zh",
	"zh-cn": "zh", "zh-tw": "zh", "zh-hk": "zh", "zh-hans": "zh", "zh-hant": "zh", "chinese": "zh",
	"en": "en", "eng": "en", "english": "en",
	"ja": "ja", "jpn": "ja", "japanese": "ja",
	"ko": "ko", "kor": "ko", "korean": "ko",
	"fr": "fr", "fre": "fr", "fra": "fr", "french": "fr",
	"de": "de", "ger": "de", "deu": "de", "german": "de",
	"es": "es", "spa": "es", "spanish": "es",
	"it": "it", "ita": "it", "italian": "it",
	"ru": "ru", "rus": "ru", "russian": "ru",
	"pt": "pt", "por": "pt", "portuguese": "pt",
	"th": "th", "tha": "th", "thai": "th",
}

// titleHints find a language in a track's title when its code says none.
var titleHints = []struct{ hint, key string }{
	{"中文", "zh"}, {"简", "zh"}, {"繁", "zh"}, {"国语", "zh"}, {"國語", "zh"}, {"粤语", "zh"}, {"粵語", "zh"},
	{"普通话", "zh"}, {"中字", "zh"}, {"chs", "zh"}, {"cht", "zh"}, {"chinese", "zh"}, {"mandarin", "zh"}, {"cantonese", "zh"},
	{"english", "en"}, {"英语", "en"}, {"英文", "en"},
	{"japanese", "ja"}, {"日语", "ja"}, {"日文", "ja"},
	{"korean", "ko"}, {"韩语", "ko"}, {"韓語", "ko"},
}

// langKey reduces a language code or name to a comparable key; "" when
// unknown.
func langKey(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || code == "und" || code == "unk" {
		return ""
	}
	if k, ok := langKeys[code]; ok {
		return k
	}
	if base, _, found := strings.Cut(code, "-"); found {
		if k, ok := langKeys[base]; ok {
			return k
		}
	}
	return code
}

// trackLang returns the language key of a track: its code, else a hint in
// its titles.
func trackLang(t *emby.Track) string {
	if k := langKey(t.Language); k != "" {
		return k
	}
	title := strings.ToLower(t.Title + " " + t.DisplayTitle)
	for _, h := range titleHints {
		if strings.Contains(title, h.hint) {
			return h.key
		}
	}
	return ""
}

// pickAudio returns the index of the audio track to play: def, the
// stream's own track, when it speaks the preferred language or none does;
// else the first in that language, a default one first.
func pickAudio(tracks []emby.Track, pref string, def int) int {
	want := langKey(pref)
	if want == "" {
		return def
	}
	best := -1
	for i := range tracks {
		t := &tracks[i]
		if trackLang(t) != want {
			continue
		}
		if t.Index == def {
			return def // the default already speaks it
		}
		if best < 0 || t.IsDefault && !tracks[indexOf(tracks, best)].IsDefault {
			best = t.Index
		}
	}
	if best < 0 {
		return def
	}
	return best
}

// pickSubtitle returns the index of the subtitle to show, -1 for none:
// with pref SubtitlesOff none; with "" the server's default def; else the
// best track in the language: a text one over a bitmap one (burned in by
// the server, when it can transcode), full over forced, default first.
// Without a track in the language, only a forced default track shows,
// which carries the foreign lines of a film the viewer hears in its
// language.
func pickSubtitle(tracks []emby.Track, pref string, def int, canBurnIn bool) int {
	switch pref {
	case SubtitlesOff:
		return -1
	case "":
		if t := trackByIndex(tracks, def); t != nil && (t.IsText || canBurnIn) {
			return def
		}
		return -1
	}
	want := langKey(pref)
	best, bestScore := -1, -1
	for i := range tracks {
		t := &tracks[i]
		if trackLang(t) != want || !t.IsText && !canBurnIn {
			continue
		}
		score := 0
		if t.IsText {
			score += 4
		}
		if !t.IsForced {
			score += 2
		}
		if t.IsDefault {
			score++
		}
		if score > bestScore {
			best, bestScore = t.Index, score
		}
	}
	if bestScore >= 0 {
		return best
	}
	if t := trackByIndex(tracks, def); t != nil && t.IsForced && (t.IsText || canBurnIn) {
		return def
	}
	return -1
}

func trackByIndex(tracks []emby.Track, index int) *emby.Track {
	if index == -1 {
		return nil
	}
	for i := range tracks {
		if tracks[i].Index == index {
			return &tracks[i]
		}
	}
	return nil
}

func indexOf(tracks []emby.Track, index int) int {
	for i := range tracks {
		if tracks[i].Index == index {
			return i
		}
	}
	return 0
}
