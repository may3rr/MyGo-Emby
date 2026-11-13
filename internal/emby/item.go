package emby

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TicksPerSecond is the number of 100 ns ticks in one second.
const TicksPerSecond = 10_000_000

// TicksToDuration converts server ticks (100 ns units) to a Duration.
func TicksToDuration(ticks int64) time.Duration { return time.Duration(ticks) * 100 }

// DurationToTicks converts a Duration to server ticks (100 ns units).
func DurationToTicks(d time.Duration) int64 { return int64(d / 100) }

// FormatRuntime formats a length as "1h 42m", "42m" or "35s" (for very
// short items). Zero or negative durations yield "".
func FormatRuntime(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d/time.Second))
	}
	m := int((d + 30*time.Second) / time.Minute) // round to nearest minute
	h, m := m/60, m%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// FormatClock formats a position as "1:02:03" or "2:03" (player time labels).
func FormatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d / time.Second)
	h, m, sec := s/3600, (s/60)%60, s%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// Runtime returns the item's length.
func (it *Item) Runtime() time.Duration { return TicksToDuration(it.RunTimeTicks) }

// RuntimeText returns the item's length formatted by FormatRuntime.
func (it *Item) RuntimeText() string { return FormatRuntime(it.Runtime()) }

// ResumePosition returns the saved playback position (0 if none).
func (it *Item) ResumePosition() time.Duration {
	if it.UserData == nil {
		return 0
	}
	return TicksToDuration(it.UserData.PlaybackPositionTicks)
}

// CanResume reports whether there is a saved position to resume from.
func (it *Item) CanResume() bool {
	return it.UserData != nil && it.UserData.PlaybackPositionTicks > 0
}

// PlayedPercentage returns progress in [0,100]: the server's value when
// present, else computed from position and runtime.
func (it *Item) PlayedPercentage() float64 {
	if it.UserData == nil {
		return 0
	}
	p := it.UserData.PlayedPercentage
	if p <= 0 && it.UserData.PlaybackPositionTicks > 0 && it.RunTimeTicks > 0 {
		p = float64(it.UserData.PlaybackPositionTicks) / float64(it.RunTimeTicks) * 100
	}
	return min(max(p, 0), 100)
}

// Progress returns PlayedPercentage as a fraction in [0,1].
func (it *Item) Progress() float64 { return it.PlayedPercentage() / 100 }

// Remaining returns runtime minus resume position (0 if unknown).
func (it *Item) Remaining() time.Duration {
	r := it.Runtime() - it.ResumePosition()
	if r < 0 || it.RunTimeTicks == 0 {
		return 0
	}
	return r
}

// IsPlayed reports the user's played flag.
func (it *Item) IsPlayed() bool { return it.UserData != nil && it.UserData.Played }

// IsFavorite reports the user's favorite flag.
func (it *Item) IsFavorite() bool { return it.UserData != nil && it.UserData.IsFavorite }

// UnplayedCount returns the number of unplayed children (series, seasons,
// folders), 0 if unknown.
func (it *Item) UnplayedCount() int {
	if it.UserData == nil {
		return 0
	}
	return it.UserData.UnplayedItemCount
}

// IsFolderLike reports whether opening the item should browse into it
// rather than show a playable detail page: libraries, folders, box sets,
// seasons, playlists… Series count as folder-like too (they have children)
// although the UI usually gives them a detail page.
func (it *Item) IsFolderLike() bool {
	switch it.Type {
	case TypeMovie, TypeEpisode, TypeVideo, TypeMusicVideo, TypeTrailer, TypeAudio, TypePerson, TypeTvChannel:
		return false
	case TypeSeries, TypeSeason, TypeBoxSet, TypeFolder, TypeCollectionFolder, TypeUserView,
		TypePlaylist, TypeMusicAlbum, TypeMusicArtist, TypeChannel:
		return true
	}
	return it.IsFolder
}

// IsPlayable reports whether the item itself can be played as video.
func (it *Item) IsPlayable() bool {
	if it.IsFolder {
		return false
	}
	switch it.Type {
	case TypeMovie, TypeEpisode, TypeVideo, TypeMusicVideo, TypeTrailer:
		return true
	}
	return it.MediaType == "Video"
}

// IsVirtual reports a placeholder for a missing or unaired episode.
func (it *Item) IsVirtual() bool { return it.LocationType == "Virtual" }

// Season returns the season number of an episode (or a season's own
// number), and false if unknown.
func (it *Item) Season() (int, bool) {
	if it.Type == TypeSeason && it.IndexNumber != nil {
		return *it.IndexNumber, true
	}
	if it.ParentIndexNumber != nil {
		return *it.ParentIndexNumber, true
	}
	return 0, false
}

// Episode returns the episode number, and false if unknown.
func (it *Item) Episode() (int, bool) {
	if it.IndexNumber != nil {
		return *it.IndexNumber, true
	}
	return 0, false
}

// EpisodeLabel returns "S1:E2" (or "S1:E2-3" for double episodes, "E2" when
// the season is unknown) for episodes, and "" for other types.
func (it *Item) EpisodeLabel() string {
	if it.Type != TypeEpisode {
		return ""
	}
	ep, ok := it.Episode()
	if !ok {
		return ""
	}
	e := "E" + strconv.Itoa(ep)
	if it.IndexNumberEnd != nil && *it.IndexNumberEnd > ep {
		e += "-" + strconv.Itoa(*it.IndexNumberEnd)
	}
	if s, ok := it.Season(); ok {
		return "S" + strconv.Itoa(s) + ":" + e
	}
	return e
}

// DisplayTitle returns "S1:E2 · Title" for episodes and Name otherwise.
func (it *Item) DisplayTitle() string {
	if l := it.EpisodeLabel(); l != "" {
		if it.Name == "" {
			return l
		}
		return l + " · " + it.Name
	}
	return it.Name
}

// Year returns ProductionYear, falling back to the premiere date's year;
// 0 when unknown.
func (it *Item) Year() int {
	if it.ProductionYear > 0 {
		return it.ProductionYear
	}
	if !it.PremiereDate.IsZero() {
		return it.PremiereDate.Year()
	}
	return 0
}

// YearText returns "2019" for most items and a range such as "2019–2023" or
// "2019–" for series (from EndDate and Status).
func (it *Item) YearText() string {
	y := it.Year()
	if y == 0 {
		return ""
	}
	s := strconv.Itoa(y)
	if it.Type != TypeSeries {
		return s
	}
	if !it.EndDate.IsZero() && it.EndDate.Year() > y {
		return s + "–" + strconv.Itoa(it.EndDate.Year())
	}
	if strings.EqualFold(it.Status, "Continuing") {
		return s + "–"
	}
	return s
}

// RatingText returns the community rating with one decimal ("8.3"), or "".
func (it *Item) RatingText() string {
	if it.CommunityRating <= 0 {
		return ""
	}
	return strconv.FormatFloat(it.CommunityRating, 'f', 1, 64)
}

// Directors returns the names of people with Type "Director".
func (it *Item) Directors() []string { return it.peopleOfType("Director") }

// crewTypes are People types that are not on-screen cast.
var crewTypes = map[string]bool{
	"Director": true, "Writer": true, "Producer": true, "Composer": true,
	"Editor": true, "Conductor": true, "Lyricist": true, "Arranger": true,
	"Engineer": true, "Mixer": true, "Creator": true, "Remixer": true,
}

// Cast returns actors and guest stars, in server order. People with an
// unknown or generic type ("Other", as some Emby servers report actors) count
// as cast; only known crew roles (Director, Writer, Producer…) are excluded.
func (it *Item) Cast() []Person {
	var out []Person
	for _, p := range it.People {
		if !crewTypes[p.Type] {
			out = append(out, p)
		}
	}
	return out
}

// Crew returns people with a known crew role (Director, Writer…).
func (it *Item) Crew() []Person {
	var out []Person
	for _, p := range it.People {
		if crewTypes[p.Type] {
			out = append(out, p)
		}
	}
	return out
}

func (it *Item) peopleOfType(t string) []string {
	var out []string
	for _, p := range it.People {
		if p.Type == t {
			out = append(out, p.Name)
		}
	}
	return out
}

// StudioNames returns the studio names.
func (it *Item) StudioNames() []string {
	out := make([]string, 0, len(it.Studios))
	for _, s := range it.Studios {
		out = append(out, s.Name)
	}
	return out
}

// Tagline returns the first tagline, or "".
func (it *Item) Tagline() string {
	if len(it.Taglines) > 0 {
		return it.Taglines[0]
	}
	return ""
}
