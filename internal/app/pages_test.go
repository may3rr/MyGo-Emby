package app

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// withSeries gives the fake server a series of two seasons, the second's
// first episode under way, a cast, and the people's works.
func withSeries(f *fakeServer) {
	f.set(func(f *fakeServer) {
		shows := f.views[1].ID
		f.children[shows] = []emby.Item{{
			ID: "ser", Name: "繁花", Type: emby.TypeSeries, IsFolder: true, ProductionYear: 2023,
			Overview: "九十年代的上海。", Genres: []string{"剧情"},
			People: []emby.Person{{ID: "p1", Name: "胡歌", Role: "阿宝", Type: "Actor"}, {ID: "p2", Name: "王家卫", Type: "Director"}},
		}}
		f.seasons["ser"] = []emby.Item{
			{ID: "s1", Name: "第 1 季", Type: emby.TypeSeason, SeriesID: "ser", IndexNumber: ptr(1)},
			{ID: "s2", Name: "第 2 季", Type: emby.TypeSeason, SeriesID: "ser", IndexNumber: ptr(2)},
		}
		f.episodes["s1"] = []emby.Item{
			{ID: "e11", Name: "开端", Type: emby.TypeEpisode, SeriesID: "ser", SeriesName: "繁花", SeasonID: "s1",
				ParentIndexNumber: ptr(1), IndexNumber: ptr(1), UserData: &emby.UserData{Played: true}},
		}
		f.episodes["s2"] = []emby.Item{
			{ID: "e21", Name: "重逢", Type: emby.TypeEpisode, SeriesID: "ser", SeriesName: "繁花", SeasonID: "s2",
				ParentIndexNumber: ptr(2), IndexNumber: ptr(1), RunTimeTicks: 2700 * emby.TicksPerSecond,
				Overview: "阿宝回到黄河路。", UserData: &emby.UserData{PlaybackPositionTicks: 900 * emby.TicksPerSecond}},
		}
		f.nextUp = []emby.Item{f.episodes["s2"][0]}
		f.persons = []emby.Item{{ID: "p1", Name: "胡歌", Type: emby.TypePerson, Overview: "演员。"}}
	})
}

func TestSeriesPage(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	withSeries(f)
	a.navigate(pathItem("ser"))
	settle(tt)
	a.waitFor(t, tt, "the series", func() bool { return tt.HasText("九十年代的上海。") })
	a.waitFor(t, tt, "the episodes", func() bool { return tt.HasText("1. 重逢") })
	settle(tt)
	// It opens on the season of the episode up next, which Play resumes.
	resume := "继续播放 第 2 季第 1 集"
	for _, text := range []string{"繁花", "2023", "剧情", resume, "第 1 季", "第 2 季", "阿宝回到黄河路。", str.Cast, "胡歌", "阿宝", "王家卫"} {
		if !tt.HasText(text) {
			t.Errorf("the series' page shows no %q", text)
		}
	}
	if tt.HasText(str.Similar) {
		t.Error("an empty similar shelf shows")
	}
	if err := tt.Click(resume); err != nil {
		t.Fatal(err)
	}
	if a.lastPlay == nil || a.lastPlay.item.ID != "e21" || a.lastPlay.opts.FromStart {
		t.Errorf("Play asked %+v", a.lastPlay)
	}
	// Another season shows its episodes.
	if err := tt.Click("第 1 季"); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "the first season", func() bool { return tt.HasText("1. 开端") })
	if tt.HasText("1. 重逢") {
		t.Error("the second season's episodes stay")
	}
	// A person opens their page.
	if err := tt.Click("胡歌"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != "/person/p1" {
		t.Errorf("the cast opened %q", a.router.Path())
	}
}

func TestItemTracksReachPlay(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		m := &f.children[f.views[0].ID][0]
		m.MediaSources = []emby.MediaSource{{
			ID: "src1", Name: "1080p", Container: "mkv",
			DefaultAudioStreamIndex: ptr(1),
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo},
				{Index: 1, Type: emby.StreamAudio, DisplayTitle: "国语 AAC"},
				{Index: 2, Type: emby.StreamAudio, DisplayTitle: "粤语 AC3"},
				{Index: 3, Type: emby.StreamSubtitle, DisplayTitle: "简体中文"},
			},
		}, {ID: "src2", Name: "4K"}}
	})
	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "the item", func() bool { return tt.HasText("国语 AAC") })
	for _, text := range []string{str.Version, "1080p", str.Audio, str.Subtitles, str.SubtitlesOff} {
		if !tt.HasText(text) {
			t.Errorf("the tracks show no %q", text)
		}
	}
	if err := tt.Click(str.Audio + "：国语 AAC"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("粤语 AC3"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.Subtitles + "：" + str.SubtitlesOff); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("简体中文"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.Play); err != nil {
		t.Fatal(err)
	}
	o := a.lastPlay.opts
	if o.AudioIndex == nil || *o.AudioIndex != 2 || o.SubtitleIndex == nil || *o.SubtitleIndex != 3 {
		t.Errorf("played with %+v", o)
	}
}

func TestItemFiltersSourcesThatNeedUnavailableTranscode(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		m := &f.children[f.views[0].ID][0]
		m.MediaSources = []emby.MediaSource{
			{
				ID: "disc", Name: "70.0G", Container: "blurayiso",
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "hevc", IsDefault: true},
					{Index: 1, Type: emby.StreamAudio, Codec: "truehd", IsDefault: true},
				},
			},
			{
				ID: "mkv", Name: "69.2G", Container: "mkv",
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "hevc", IsDefault: true},
					{Index: 1, Type: emby.StreamAudio, Codec: "eac3", IsDefault: true},
				},
			},
			{
				ID: "mp4", Name: "7.8G", Container: "mp4",
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "h264", CodecTag: "avc1", IsDefault: true},
					{Index: 1, Type: emby.StreamAudio, Codec: "aac", IsDefault: true},
				},
			},
		}
	})
	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "the playable version menu", func() bool {
		return tt.HasText(str.Version + "：69.2G")
	})
	if tt.HasText("70.0G") {
		t.Fatal("an unsupported disc image appears in the version menu")
	}
	if err := tt.Click(str.Version + "：69.2G"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("70.0G") {
		t.Fatal("an unsupported disc image appears in the opened version menu")
	}
	if !tt.HasText("7.8G") {
		t.Fatal("a supported alternate version is missing")
	}
	if err := tt.Click("7.8G"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.Play); err != nil {
		t.Fatal(err)
	}
	if a.lastPlay == nil || a.lastPlay.opts.MediaSourceID != "mp4" {
		t.Fatalf("Play used %+v, want the selected MP4 version", a.lastPlay)
	}
}

func TestFavoriteAndPlayedButtons(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "the item", func() bool { return tt.HasText(str.AddFavorite) })
	if err := tt.Click(str.AddFavorite); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(str.Favorited) {
		t.Error("the favorite does not show at once")
	}
	a.waitFor(t, tt, "the server", func() bool { return len(f.dataCalls) == 1 })
	if !strings.HasPrefix(f.dataCalls[0], "POST /Users/"+f.userID+"/FavoriteItems/Home-m-0") {
		t.Errorf("asked %q", f.dataCalls)
	}
	// A change the server refuses goes back, saying why.
	f.set(func(f *fakeServer) { f.dataFail = true })
	if err := tt.Click(str.MarkPlayed); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(str.PlayedDone) {
		t.Error("watched does not show at once")
	}
	a.waitFor(t, tt, "the refusal", func() bool { return tt.HasText(str.MarkPlayed) })
	if !tt.HasText(fmtServerErr(500)) {
		t.Errorf("the refusal is not told: %q", tt.Texts())
	}
}

func fmtServerErr(code int) string {
	return strings.Replace(str.ErrServerFmt, "%d", itoa(code), 1)
}

// The card menu marks an item watched, and its card and the others showing
// it change at once.
func TestCardMenu(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	a.navigate(pathLibrary(f.views[0].ID))
	settle(tt)
	a.waitFor(t, tt, "the films", func() bool { return tt.HasText("Home-m-0") })
	settle(tt)
	if err := tt.RightClick("Home-m-1"); err != nil {
		t.Fatal(err)
	}
	menu := strings.Join(tt.Menu(), "|")
	for _, item := range []string{str.Play, str.MarkPlayed, str.AddFavorite} {
		if !strings.Contains(menu, item) {
			t.Errorf("the menu has no %q: %s", item, menu)
		}
	}
	if err := tt.ChooseMenuItem(str.MarkPlayed); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	// The card's check is inside its button, which screen readers read as
	// one: the state it is drawn from changed at once.
	for _, ls := range a.lists {
		for _, it := range ls.items {
			if it.ID == "Home-m-1" && !it.IsPlayed() {
				t.Error("the card does not show it watched")
			}
		}
	}
	a.waitFor(t, tt, "the server", func() bool { return len(f.dataCalls) == 1 })
	if err := tt.RightClick("Home-m-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(tt.Menu(), "|"), str.MarkUnplayed) {
		t.Error("the menu does not offer to undo")
	}
	tt.CloseMenu()
}

func TestPersonPage(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	withSeries(f)
	a.navigate(pathPerson("p1"))
	settle(tt)
	a.waitFor(t, tt, "the person", func() bool { return tt.HasText("演员。") })
	a.waitFor(t, tt, "the works", func() bool { return tt.HasText("繁花") })
	// A server that cannot list the works still shows the person.
	f.set(func(f *fakeServer) { f.personFail = true })
	a.refresh()
	a.waitFor(t, tt, "the person again", func() bool {
		return !a.people["p1"].loading && a.people["p1"].worksErr != nil
	})
	settle(tt)
	if !tt.HasText("演员。") || !tt.HasText(str.WorksUnsupported) || tt.HasText(str.ErrorTitle) {
		t.Errorf("without works the page shows %q", tt.Texts())
	}
}

func TestSearch(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	withSeries(f)
	a.searchBox.Focus()
	tt.Frame()
	tt.Type("繁")
	tt.Frame()
	tt.Type("花")
	tt.Frame()
	// Nothing is asked before the typing pauses.
	if n := len(f.requests("/Persons")); n != 0 {
		t.Fatalf("asked %d times while typing", n)
	}
	tt.Advance(searchDelay + 50*time.Millisecond)
	a.waitFor(t, tt, "the results", func() bool { return tt.HasText(str.SearchSeries + " · 1") })
	if n := len(f.requests("/Persons")); n != 1 {
		t.Errorf("asked %d times for one query", n)
	}
	// The keys choose a result and open it.
	tt.Key(0, ui.KeyDown)
	if a.found.sel != 0 {
		t.Errorf("Down chose %d", a.found.sel)
	}
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	if a.router.Path() != "/item/ser" {
		t.Errorf("Enter opened %q", a.router.Location())
	}
}

// An answer to a query the user typed past is dropped.
func TestSearchDropsOldAnswer(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	withSeries(f)
	a.navigate(pathSearch("繁花"))
	settle(tt)
	tt.Advance(searchDelay + 50*time.Millisecond)
	a.found.typed = "胡歌" // the user typed on before the answer came
	a.runSearch("胡歌")
	a.waitFor(t, tt, "both answers", func() bool { return !a.found.loading })
	if a.found.query != "胡歌" || len(a.found.res.Series) != 0 || len(a.found.res.People) != 1 {
		t.Errorf("the results are %+v for %q", a.found.res, a.found.query)
	}
}

func TestFavoritesPage(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		f.children[f.views[0].ID][2].UserData = &emby.UserData{IsFavorite: true}
	})
	a.navigate(pathFavorites)
	settle(tt)
	a.waitFor(t, tt, "the favorites", func() bool { return tt.HasText("Home-m-2") })
	if !tt.HasText(str.SearchMovies) || tt.HasText(str.SearchEpisodes) {
		t.Errorf("the favorites show %q", tt.Texts())
	}
	if err := tt.RightClick("Home-m-2"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(str.RemoveFavorite); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if tt.HasText("Home-m-2") || !tt.HasText(str.FavoritesEmpty) {
		t.Errorf("taken out, it stays: %q", tt.Texts())
	}
}

func TestSettingsPage(t *testing.T) {
	a, tt, _ := signedInApp(t, 1280, 900)
	// The account's menu opens them, as ⌘, does from the app's menu.
	if err := tt.Click(str.AccountMenu); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.Settings); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathSettings {
		t.Fatalf("the menu went to %q", a.router.Path())
	}
	for _, text := range []string{str.Appearance, str.MaxBitrate, str.SubtitleLang, str.AudioLang, str.AutoNext, str.CacheSize, str.ClearCache, str.About, "Home", "jackie"} {
		if !tt.HasText(text) {
			t.Errorf("the settings show no %q", text)
		}
	}
	if err := tt.Click(str.Dark); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.ThemeMode != themeDark {
		t.Errorf("the theme is %q", a.settings.ThemeMode)
	}
	a.form.bitrate = "20 Mbps"
	a.settings.MaxBitrate = valueOf(bitrateChoices, a.form.bitrate)
	if a.settings.MaxBitrate != 20_000_000 {
		t.Errorf("20 Mbps keeps %d", a.settings.MaxBitrate)
	}
	sw, ok := tt.AccessByRole(ui.RoleSwitch, str.AutoNext)
	if !ok {
		t.Fatal("no switch for autoplay")
	}
	tt.ClickAt(sw.Bounds.X+sw.Bounds.W/2, sw.Bounds.Y+sw.Bounds.H/2)
	tt.Frame()
	if a.settings.AutoPlayNext {
		t.Error("the switch did not turn autoplay off")
	}
}

// The app's menu's shortcuts: back, forward, search, settings, refresh.
func TestShortcuts(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	a.navigate(pathLibrary(f.views[0].ID))
	settle(tt)
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	settle(tt)
	if a.router.Path() != pathHome {
		t.Errorf("⌘[ went to %q", a.router.Path())
	}
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	settle(tt)
	if a.router.Path() != pathLibrary(f.views[0].ID) {
		t.Errorf("⌘] went to %q", a.router.Path())
	}
	a.focusSearch()
	tt.Frame()
	tt.Type("x")
	settle(tt)
	if a.router.Path() != "/search" {
		t.Errorf("⌘F did not put the keyboard in the search: at %q", a.router.Path())
	}
	before := a.reload
	a.refresh()
	if a.reload != before+1 {
		t.Error("⌘R did not refresh")
	}
}
