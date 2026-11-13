package emby

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestTicks(t *testing.T) {
	if d := TicksToDuration(65221440000); d != 6522144*time.Millisecond {
		t.Errorf("TicksToDuration = %v", d)
	}
	if tk := DurationToTicks(90 * time.Second); tk != 900_000_000 {
		t.Errorf("DurationToTicks = %d", tk)
	}
	if TicksPerSecond != DurationToTicks(time.Second) {
		t.Error("TicksPerSecond mismatch")
	}
}

func TestFormatting(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                "",
		35 * time.Second:                 "35s",
		42*time.Minute + 10*time.Second:  "42m",
		60 * time.Minute:                 "1h",
		108*time.Minute + 40*time.Second: "1h 49m",
		2*time.Hour + 29*time.Second:     "2h",
	}
	for d, want := range cases {
		if got := FormatRuntime(d); got != want {
			t.Errorf("FormatRuntime(%v) = %q, want %q", d, got, want)
		}
	}
	if s := FormatClock(3723 * time.Second); s != "1:02:03" {
		t.Errorf("FormatClock = %q", s)
	}
	if s := FormatClock(65 * time.Second); s != "1:05" {
		t.Errorf("FormatClock = %q", s)
	}
}

const episodeJSON = `{
  "Id":"ep1","Name":"Pilot","Type":"Episode","MediaType":"Video","IsFolder":false,
  "IndexNumber":2,"ParentIndexNumber":1,"SeriesId":"s1","SeriesName":"Show",
  "SeriesPrimaryImageTag":"sp","SeasonId":"se1","RunTimeTicks":26000000000,
  "ProductionYear":2019,"CommunityRating":8.25,"OfficialRating":"TV-14",
  "PremiereDate":"2019-03-01T00:00:00.0000000Z",
  "ImageTags":{"Primary":"epp"},
  "ParentBackdropItemId":"s1","ParentBackdropImageTags":["sb0","sb1"],
  "ParentThumbItemId":"s1","ParentThumbImageTag":"st",
  "ParentLogoItemId":"s1","ParentLogoImageTag":"sl",
  "UserData":{"PlaybackPositionTicks":13000000000,"PlayCount":0,"IsFavorite":true,"Played":false},
  "SomethingNew":{"x":1}
}`

func TestItemHelpers(t *testing.T) {
	var it Item
	if err := json.Unmarshal([]byte(episodeJSON), &it); err != nil {
		t.Fatal(err)
	}
	if got := it.DisplayTitle(); got != "S1:E2 · Pilot" {
		t.Errorf("DisplayTitle = %q", got)
	}
	if it.Year() != 2019 || it.YearText() != "2019" || it.RatingText() != "8.2" && it.RatingText() != "8.3" {
		t.Errorf("year %d %q rating %q", it.Year(), it.YearText(), it.RatingText())
	}
	if it.ResumePosition() != 1300*time.Second || !it.CanResume() {
		t.Errorf("resume %v", it.ResumePosition())
	}
	if p := it.PlayedPercentage(); p != 50 {
		t.Errorf("PlayedPercentage = %v", p)
	}
	if it.Remaining() != 1300*time.Second || it.RuntimeText() != "43m" {
		t.Errorf("remaining %v runtime %q", it.Remaining(), it.RuntimeText())
	}
	if !it.IsFavorite() || it.IsPlayed() || it.IsFolderLike() || !it.IsPlayable() {
		t.Error("flags wrong")
	}

	special := Item{Type: TypeEpisode, Name: "Special", IndexNumber: Index(0), ParentIndexNumber: Index(0)}
	if got := special.DisplayTitle(); got != "S0:E0 · Special" {
		t.Errorf("special DisplayTitle = %q", got)
	}
	double := Item{Type: TypeEpisode, Name: "Two", IndexNumber: Index(3), IndexNumberEnd: Index(4)}
	if got := double.DisplayTitle(); got != "E3-4 · Two" {
		t.Errorf("double DisplayTitle = %q", got)
	}
	movie := Item{Type: TypeMovie, Name: "M", PremiereDate: Date{time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}}
	if movie.DisplayTitle() != "M" || movie.Year() != 2001 {
		t.Errorf("movie %q %d", movie.DisplayTitle(), movie.Year())
	}
	series := Item{Type: TypeSeries, ProductionYear: 2015, Status: "Ended", EndDate: Date{time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)},
		UserData: &UserData{UnplayedItemCount: 7}}
	if series.YearText() != "2015–2019" || series.UnplayedCount() != 7 || !series.IsFolderLike() {
		t.Errorf("series %q %d", series.YearText(), series.UnplayedCount())
	}
	cont := Item{Type: TypeSeries, ProductionYear: 2020, Status: "Continuing"}
	if cont.YearText() != "2020–" {
		t.Errorf("continuing %q", cont.YearText())
	}
	computed := Item{RunTimeTicks: 1000, UserData: &UserData{PlaybackPositionTicks: 250}}
	if computed.PlayedPercentage() != 25 {
		t.Errorf("computed pct %v", computed.PlayedPercentage())
	}
	if (&Item{Type: "Unknown", IsFolder: true}).IsFolderLike() != true {
		t.Error("unknown folder type should be folder-like")
	}
}

func TestCastAndCrew(t *testing.T) {
	it := Item{People: []Person{
		{Name: "A", Type: "Actor"}, {Name: "D", Type: "Director"}, {Name: "O", Type: "Other", Role: "Pongo (voice)"},
		{Name: "G", Type: "GuestStar"}, {Name: "W", Type: "Writer"},
	}}
	var cast, crew []string
	for _, p := range it.Cast() {
		cast = append(cast, p.Name)
	}
	for _, p := range it.Crew() {
		crew = append(crew, p.Name)
	}
	if fmt.Sprint(cast) != "[A O G]" || fmt.Sprint(crew) != "[D W]" || fmt.Sprint(it.Directors()) != "[D]" {
		t.Errorf("cast %v crew %v directors %v", cast, crew, it.Directors())
	}
}

func TestSubtitleClassification(t *testing.T) {
	cases := []struct {
		s    MediaStream
		text bool
	}{
		{MediaStream{Type: StreamSubtitle, Codec: "subrip"}, true},
		{MediaStream{Type: StreamSubtitle, Codec: "ass", IsTextSubtitleStream: true}, true},
		{MediaStream{Type: StreamSubtitle, Codec: "hdmv_pgs_subtitle", IsTextSubtitleStream: true}, false},
		{MediaStream{Type: StreamSubtitle, Codec: "PGSSUB"}, false},
		{MediaStream{Type: StreamSubtitle, Codec: "dvd_subtitle"}, false},
		{MediaStream{Type: StreamSubtitle, Codec: "weird", IsTextSubtitleStream: true}, true},
		{MediaStream{Type: StreamAudio, Codec: "srt"}, false},
	}
	for _, c := range cases {
		if got := c.s.IsTextSubtitle(); got != c.text {
			t.Errorf("%s IsTextSubtitle = %v, want %v", c.s.Codec, got, c.text)
		}
	}
}
