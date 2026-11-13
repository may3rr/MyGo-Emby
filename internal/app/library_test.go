package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

func TestLibraryQuery(t *testing.T) {
	movies := library{ID: "lib", Kind: emby.CollectionMovies}
	cases := []struct {
		name     string
		lib      library
		folder   string
		prefs    libPrefs
		parent   string
		types    []string
		rec      bool
		sortBy   []string
		order    emby.SortOrder
		filters  []string
		start, n int
	}{
		{"films by name", movies, "", libPrefs{}, "lib", []string{"Movie"}, true, []string{"SortName"}, emby.Ascending, nil, 0, 60},
		{"shows, newest", library{ID: "tv", Kind: emby.CollectionTVShows}, "", libPrefs{sort: sortAdded, desc: true}, "tv",
			[]string{"Series"}, true, []string{"DateCreated", "SortName"}, emby.Descending, nil, 60, 60},
		{"box sets by year", library{ID: "bs", Kind: emby.CollectionBoxSets}, "", libPrefs{sort: sortYear, desc: true}, "bs",
			[]string{"BoxSet"}, true, []string{"ProductionYear", "PremiereDate", "SortName"}, emby.Descending, nil, 0, 60},
		{"music videos, unplayed", library{ID: "mv", Kind: emby.CollectionMusicVideos}, "", libPrefs{filter: filterUnplayed}, "mv",
			[]string{"MusicVideo"}, true, []string{"SortName"}, emby.Ascending, []string{"IsUnplayed"}, 0, 60},
		{"folders, favorites by rating", library{ID: "f", Kind: emby.CollectionFolders}, "", libPrefs{sort: sortRating, desc: true, filter: filterFavorites}, "f",
			nil, false, []string{"CommunityRating", "SortName"}, emby.Descending, []string{"IsFavorite"}, 0, 60},
		{"an unknown kind is a folder", library{ID: "x", Kind: "somethingnew"}, "", libPrefs{}, "x", nil, false, []string{"SortName"}, emby.Ascending, nil, 0, 60},
		{"a folder of films", movies, "sub", libPrefs{}, "sub", nil, false, []string{"SortName"}, emby.Ascending, nil, 120, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := libraryQuery(tc.lib, tc.folder, tc.prefs, tc.start, tc.n)
			if q.ParentID != tc.parent || !slices.Equal(q.IncludeItemTypes, tc.types) || q.Recursive != tc.rec ||
				!slices.Equal(q.SortBy, tc.sortBy) || q.SortOrder != tc.order || !slices.Equal(q.Filters, tc.filters) ||
				q.StartIndex != tc.start || q.Limit != tc.n {
				t.Errorf("query %+v", q)
			}
		})
	}
}

// The library's filters and sorting reach the server.
func TestLibraryFiltersAndSorting(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	movies := f.views[0].ID
	if err := tt.Click("电影"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	a.waitFor(t, tt, "the films", func() bool { return tt.HasText("Home-m-0") })
	last := func() map[string]string {
		qs := f.itemQueries(movies)
		q := qs[len(qs)-1]
		return map[string]string{"Recursive": q.Get("Recursive"), "IncludeItemTypes": q.Get("IncludeItemTypes"),
			"SortBy": q.Get("SortBy"), "SortOrder": q.Get("SortOrder"), "Filters": q.Get("Filters"), "Limit": q.Get("Limit")}
	}
	want := map[string]string{"Recursive": "true", "IncludeItemTypes": "Movie", "SortBy": "SortName",
		"SortOrder": "Ascending", "Filters": "", "Limit": "60"}
	if got := last(); !mapsEqual(got, want) {
		t.Errorf("first page %v", got)
	}
	if !tt.HasText(itemCountText(8)) {
		t.Errorf("the count does not show: %q", tt.Texts())
	}

	if err := tt.Click(str.FilterUnplayed); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "unplayed", func() bool { return len(f.itemQueries(movies)) == 2 && !a.anyListLoading() })
	want["Filters"] = "IsUnplayed"
	if got := last(); !mapsEqual(got, want) {
		t.Errorf("unplayed %v", got)
	}

	// The sort menu, drawn in the window.
	if err := tt.Click(str.SortBy + "：" + str.SortName); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.SortYear); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "by year", func() bool { return len(f.itemQueries(movies)) == 3 && !a.anyListLoading() })
	want["SortBy"], want["SortOrder"] = "ProductionYear,PremiereDate,SortName", "Descending"
	if got := last(); !mapsEqual(got, want) {
		t.Errorf("by year %v", got)
	}
	if p := a.prefsFor(movies); p != (libPrefs{sort: sortYear, desc: true, filter: filterUnplayed}) {
		t.Errorf("prefs %+v", p)
	}
	// Each library keeps its own.
	if p := a.prefsFor(f.views[1].ID); p != (libPrefs{}) {
		t.Errorf("another library's prefs %+v", p)
	}
}

func (a *app) anyListLoading() bool {
	for _, ls := range a.lists {
		if ls.loading {
			return true
		}
	}
	return false
}

func mapsEqual(x, y map[string]string) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

// An answer for a sorting the user left is not shown (DESIGN.md §6).
func TestLibraryDropsAnswerOfOldSort(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	movies := f.views[0].ID
	f.set(func(f *fakeServer) { f.hold = true })
	a.navigate(pathLibrary(movies))
	settle(tt)
	a.waitFor(t, tt, "the first request", func() bool { return f.heldCount() == 1 })
	a.setSort(movies, sortYear)
	settle(tt)
	a.waitFor(t, tt, "the second request", func() bool { return f.heldCount() == 2 })
	// The server answers by name with other items than by year.
	f.set(func(f *fakeServer) { f.children[movies] = fakeItems("by-year", 3, emby.TypeMovie) })
	f.releaseAt(1)
	a.waitFor(t, tt, "by year", func() bool { return tt.HasText("by-year-0") })
	f.set(func(f *fakeServer) { f.children[movies] = fakeItems("by-name", 3, emby.TypeMovie) })
	f.releaseAt(0)
	time.Sleep(50 * time.Millisecond)
	a.waitFor(t, tt, "the old answer", func() bool { return a.runPosted() == 0 })
	settle(tt)
	if tt.HasText("by-name-0") || !tt.HasText("by-year-0") {
		t.Errorf("the page shows %q", tt.Texts())
	}
}

// A library of folders goes a level down on a click, and Back comes up.
func TestLibraryFolders(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	f.set(func(f *fakeServer) {
		f.views = append(f.views, emby.Item{ID: "home-vids", Name: "家庭视频", CollectionType: emby.CollectionFolders})
		f.children["home-vids"] = []emby.Item{
			{ID: "trip", Name: "旅行", Type: emby.TypeFolder, IsFolder: true},
			{ID: "clip", Name: "片段", Type: emby.TypeVideo},
		}
		f.children["trip"] = []emby.Item{{ID: "day1", Name: "第一天", Type: emby.TypeVideo}}
	})
	a.refresh()
	a.waitFor(t, tt, "the third library", func() bool { return len(a.libraries) == 3 })
	if err := tt.Click("家庭视频"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	a.waitFor(t, tt, "the folder", func() bool { return tt.HasText("旅行") })
	if q := f.itemQueries("home-vids")[0]; q.Get("Recursive") != "" || q.Get("IncludeItemTypes") != "" {
		t.Errorf("a folder library asked %v", q)
	}
	if err := tt.Click("旅行"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	a.waitFor(t, tt, "a level down", func() bool { return tt.HasText("第一天") })
	if a.router.Path() != "/library/home-vids/trip" || a.router.Query("name") != "旅行" {
		t.Errorf("a level down at %q", a.router.Location())
	}
	if a.section() != sectionLibPrefix+"home-vids" {
		t.Errorf("a level down, the sidebar shows %q", a.section())
	}
	if len(f.itemQueries("trip")) != 1 {
		t.Error("the folder's items were not asked by parent")
	}
	// A video opens its page; Back comes up a level.
	if err := tt.Click("第一天"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != "/item/day1" {
		t.Errorf("a video opened %q", a.router.Path())
	}
	a.back()
	a.back()
	settle(tt)
	if a.router.Path() != "/library/home-vids" || !tt.HasText("片段") {
		t.Errorf("back at %q", a.router.Path())
	}
}

func TestHomeShelves(t *testing.T) {
	f := newFakeServer(t, "Home")
	ep := emby.Item{ID: "ep1", Name: "采访", Type: emby.TypeEpisode, SeriesName: "气体人第一号", SeriesID: "s1",
		ParentIndexNumber: ptr(1), IndexNumber: ptr(1), RunTimeTicks: 600 * emby.TicksPerSecond,
		UserData: &emby.UserData{PlaybackPositionTicks: 300 * emby.TicksPerSecond}}
	next := emby.Item{ID: "ep9", Name: "终章", Type: emby.TypeEpisode, SeriesName: "另一部", SeriesID: "s2",
		ParentIndexNumber: ptr(2), IndexNumber: ptr(9)}
	latest := []emby.Item{
		{ID: "new1", Name: "冰川时代6", Type: emby.TypeMovie, ProductionYear: 2027, BackdropImageTags: []string{"b1"}, Overview: "一群史前动物。"},
		{ID: "new2", Name: "乐园追放", Type: emby.TypeMovie, ProductionYear: 2026, BackdropImageTags: []string{"b2"}},
		{ID: "new3", Name: "没有背景图", Type: emby.TypeMovie},
	}
	f.set(func(f *fakeServer) {
		f.resume = []emby.Item{ep}
		f.nextUp = []emby.Item{ep, next} // Emby lists the episode under way too
		f.latest[f.views[0].ID] = latest
	})
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	a.signIn(t, tt, f)
	a.waitFor(t, tt, "home", func() bool { return a.home.loaded })
	settle(tt)
	for _, text := range []string{str.ContinueWatching, str.NextUp, "最近添加 · 电影", "冰川时代6", "气体人第一号", "另一部"} {
		if !tt.HasText(text) {
			t.Errorf("home shows no %q", text)
		}
	}
	// A library with nothing new has no shelf.
	if tt.HasText("最近添加 · 剧集") {
		t.Error("an empty shelf shows")
	}
	if n := len(a.home.data.nextUp); n != 1 {
		t.Errorf("next up holds %d, the episode under way among them", n)
	}
	// The banner shows those with a backdrop, and turns by itself.
	if len(a.home.data.hero) != 2 || a.home.heroIndex != 0 {
		t.Fatalf("the banner has %d items", len(a.home.data.hero))
	}
	tt.Advance(heroInterval + time.Second)
	if a.home.heroIndex != 1 {
		t.Errorf("the banner did not turn: %d", a.home.heroIndex)
	}
	// Its buttons turn it by hand.
	if err := tt.Click(str.Next); err != nil {
		t.Fatal(err)
	}
	if a.home.heroIndex != 0 {
		t.Errorf("Next went to %d", a.home.heroIndex)
	}
	// Its play button goes to the player's entry.
	if err := tt.Click(str.Play); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.lastPlay == nil {
		t.Error("the play button did not reach the player's entry")
	}
	// ⌘R loads it again.
	before := len(f.requests("/Users/" + f.userID + "/Items/Resume"))
	a.refresh()
	a.waitFor(t, tt, "reloading", func() bool {
		return len(f.requests("/Users/"+f.userID+"/Items/Resume")) > before && !a.home.loading
	})
}

func TestHeroStillUnderReduceMotion(t *testing.T) {
	f := newFakeServer(t, "Home")
	f.set(func(f *fakeServer) {
		f.latest[f.views[0].ID] = []emby.Item{
			{ID: "a", Name: "A", Type: emby.TypeMovie, BackdropImageTags: []string{"x"}},
			{ID: "b", Name: "B", Type: emby.TypeMovie, BackdropImageTags: []string{"y"}},
		}
	})
	a := newTestApp(t)
	tt := ui.NewTesterWith(a.view, 1280, 820, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true, TextScale: 1}})
	a.signIn(t, tt, f)
	a.waitFor(t, tt, "home", func() bool { return a.home.loaded })
	settle(tt)
	tt.Advance(3 * heroInterval)
	if a.home.heroIndex != 0 {
		t.Errorf("under Reduce Motion the banner turned to %d", a.home.heroIndex)
	}
}

func TestHomeFailsWithRetry(t *testing.T) {
	f := newFakeServer(t, "Home")
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	a.signIn(t, tt, f)
	a.waitFor(t, tt, "home", func() bool { return a.home.loaded })
	// Home failing the first time shows what went wrong.
	a.resetData()
	f.set(func(f *fakeServer) { f.down = true })
	tt.Frame()
	a.waitFor(t, tt, "the error", func() bool { return a.home.err != nil })
	settle(tt)
	if !tt.HasText(str.ErrorTitle) || !tt.HasText(str.ErrUnreachable) {
		t.Fatalf("home failing shows %q", tt.Texts())
	}
	f.set(func(f *fakeServer) { f.down = false })
	if err := tt.Click(str.Retry); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "home again", func() bool { return a.home.loaded })
}

func TestItemPage(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	f.set(func(f *fakeServer) {
		f.children[f.views[0].ID][0].Overview = "一部电影的简介。"
		f.children[f.views[0].ID][0].UserData = &emby.UserData{PlaybackPositionTicks: 10 * emby.TicksPerSecond}
		f.children[f.views[0].ID][0].RunTimeTicks = 100 * emby.TicksPerSecond
	})
	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "the item", func() bool { return tt.HasText("一部电影的简介。") })
	for _, text := range []string{"Home-m-0", "2000", str.Resume, str.PlayFromStart} {
		if !tt.HasText(text) {
			t.Errorf("the item page shows no %q: %q", text, tt.Texts())
		}
	}
	if err := tt.Click(str.PlayFromStart); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.lastPlay == nil || !a.lastPlay.opts.FromStart {
		t.Errorf("playing from the start did not reach the player's entry: %+v", a.lastPlay)
	}
	a.navigate(pathItem("nothing"))
	settle(tt)
	a.waitFor(t, tt, "the error", func() bool { return tt.HasText(str.ErrNotFound) })
	if !strings.Contains(strings.Join(tt.Texts(), "|"), str.Retry) {
		t.Error("a missing item offers no retry")
	}
}

func ptr[T any](v T) *T { return &v }

// A card of an item without a picture shows its kind's icon and its name
// in the picture's place, not an empty block.
func TestCardWithoutPicture(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	a.navigate(pathLibrary(f.views[0].ID))
	settle(tt)
	a.waitFor(t, tt, "the films", func() bool { return tt.HasText("Home-m-0") })
	settle(tt)
	n := 0
	for _, text := range tt.Texts() {
		if text == "Home-m-0" {
			n++
		}
	}
	if n < 2 {
		t.Errorf("the name shows %d times, not in the picture's place too", n)
	}
}

// How a library is sorted and filtered outlives the app.
func TestLibraryPrefsKept(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	a := newAppWith(path, "", newMemSecrets())
	defer a.img.Close()
	a.setPrefs("lib1", libPrefs{sort: sortRating, desc: true, filter: filterFavorites})
	a.setPrefs("lib2", libPrefs{sort: sortAdded})
	if err := a.store.Flush(); err != nil {
		t.Fatal(err)
	}
	b := newAppWith(path, "", newMemSecrets())
	defer b.img.Close()
	if p := b.prefsFor("lib1"); p != (libPrefs{sort: sortRating, desc: true, filter: filterFavorites}) {
		t.Errorf("lib1 came back %+v", p)
	}
	if p := b.prefsFor("lib2"); p != (libPrefs{sort: sortAdded}) {
		t.Errorf("lib2 came back %+v", p)
	}
	if p := b.prefsFor("other"); p != (libPrefs{}) {
		t.Errorf("a library never sorted came back %+v", p)
	}
}
