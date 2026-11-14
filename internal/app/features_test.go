package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

func TestSearchHistoryRecordReplayAndClear(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	withSeries(f)

	// Search "繁花" and open the result via Enter to record history.
	a.searchBox.Focus()
	tt.Frame()
	tt.Type("繁花")
	tt.Frame()
	tt.Advance(searchDelay + 50*time.Millisecond)
	a.waitFor(t, tt, "search results", func() bool { return tt.HasText(str.SearchSeries + " · 1") })
	tt.Key(0, ui.KeyEnter)
	settle(tt)
	if !slices.Equal(a.settings.SearchHistory, []string{"繁花"}) {
		t.Fatalf("SearchHistory = %v, want [繁花]", a.settings.SearchHistory)
	}

	// Navigating to empty /search (or calling focusSearch with empty box) shows recent searches.
	a.search = ""
	a.focusSearch()
	settle(tt)
	if a.router.Path() != "/search" {
		t.Fatalf("focusSearch with history went to %q, want /search", a.router.Path())
	}
	if !tt.HasText(str.SearchRecent) || !tt.HasText("繁花") {
		t.Fatalf("recent search UI missing: %q", tt.Texts())
	}

	// Clicking the recent search chip replays the query.
	if err := tt.Click("繁花"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(searchDelay + 50*time.Millisecond)
	a.waitFor(t, tt, "replayed results", func() bool { return tt.HasText(str.SearchSeries + " · 1") })

	// Clearing history hides recent chips on empty /search.
	a.search = ""
	a.router.Replace("/search")
	settle(tt)
	if err := tt.Click(str.ClearHistory); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.SearchHistory) != 0 {
		t.Fatalf("SearchHistory after clear = %v, want empty", a.settings.SearchHistory)
	}
	if !tt.HasText(str.SearchHint) {
		t.Fatalf("expected empty search hint after clearing, got %q", tt.Texts())
	}
}

func TestItemDetailSpecsAndChaptersStartAt(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 960)
	f.set(func(f *fakeServer) {
		it := &f.children[f.views[0].ID][0]
		it.Genres = []string{"科幻"}
		it.MediaSources = []emby.MediaSource{
			{
				ID:        "ms-4k",
				Name:      "4K HDR Remux",
				Container: "mkv",
				Size:      16 << 30,
				ETag:      "etag-4k",
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "hevc", Width: 3840, Height: 2160, VideoRangeType: "HDR10", IsDefault: true},
					{Index: 1, Type: emby.StreamAudio, Codec: "truehd", Channels: 8, Language: "eng", IsDefault: true},
				},
			},
		}
		it.Chapters = []emby.Chapter{
			{StartPositionTicks: 0, Name: "序幕"},
			{StartPositionTicks: emby.DurationToTicks(25 * time.Minute), Name: "高潮段落"},
		}
	})

	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "item specs and chapters", func() bool {
		return tt.HasText("4K") && tt.HasText(str.Chapters)
	})
	settle(tt)

	for _, badge := range []string{"4K", "HDR10", "HEVC", "TRUEHD 7.1", "MKV"} {
		if !tt.HasText(badge) {
			t.Errorf("missing media spec badge %q in %q", badge, tt.Texts())
		}
	}

	if err := tt.Click("高潮段落"); err != nil {
		t.Fatal(err)
	}
	if a.lastPlay == nil {
		t.Fatal("clicking chapter did not trigger play")
	}
	if !a.lastPlay.opts.HasStartAt || a.lastPlay.opts.StartAt != 25*time.Minute {
		t.Fatalf("chapter play opts = %+v, want StartAt=25m HasStartAt=true", a.lastPlay.opts)
	}
	if a.lastPlay.opts.MediaSourceID != "ms-4k" {
		t.Fatalf("chapter play MediaSourceID = %q, want ms-4k", a.lastPlay.opts.MediaSourceID)
	}
}

func TestGenreChipNavigatesToGenrePage(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		f.children[f.views[0].ID][0].Genres = []string{"科幻", "冒险"}
		f.children[f.views[0].ID][1].Genres = []string{"科幻"}
		f.children[f.views[0].ID][2].Genres = []string{"剧情"}
	})

	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "genre chips", func() bool { return tt.HasText("科幻") })

	if err := tt.Click("科幻"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != "/genre/科幻" {
		t.Fatalf("navigated to %q, want /genre/科幻", a.router.Location())
	}
	a.waitFor(t, tt, "genre page items", func() bool { return tt.HasText("Home-m-1") })
	if tt.HasText("Home-m-2") {
		t.Fatalf("genre page leaked unrelated genre item Home-m-2: %q", tt.Texts())
	}
}

func TestExternalPlayerLaunchAndURLConstruction(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	a.extPlayers = []externalPlayer{{Name: "IINA", Kind: "iina", Path: "/Applications/IINA.app/Contents/MacOS/iina-cli"}}

	var gotEP externalPlayer
	var gotURL, gotTitle string
	var gotStart time.Duration
	a.launchExternal = func(ep externalPlayer, streamURL, title string, start time.Duration) error {
		gotEP, gotURL, gotTitle, gotStart = ep, streamURL, title, start
		return nil
	}

	f.set(func(f *fakeServer) {
		it := &f.children[f.views[0].ID][0]
		it.MediaSources = []emby.MediaSource{
			{
				ID:        "ms-iso",
				Name:      "Blu-ray MKV",
				Container: "mkv",
				ETag:      "etag-1",
				MediaStreams: []emby.MediaStream{
					{Index: 0, Type: emby.StreamVideo, Codec: "hevc", IsDefault: true},
				},
			},
		}
		it.UserData = &emby.UserData{PlaybackPositionTicks: emby.DurationToTicks(15 * time.Minute)}
	})

	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "external player button", func() bool { return tt.HasText("用 IINA 打开") })

	if err := tt.Click("用 IINA 打开"); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "external launch callback", func() bool { return gotURL != "" })

	if gotEP.Kind != "iina" || gotTitle != "Home-m-0" || gotStart != 15*time.Minute {
		t.Fatalf("unexpected external launch: ep=%+v title=%q start=%v", gotEP, gotTitle, gotStart)
	}
	if !strings.Contains(gotURL, "/Videos/Home-m-0/stream") || !strings.Contains(gotURL, "Static=true") || !strings.Contains(gotURL, "MediaSourceId=ms-iso") {
		t.Fatalf("unexpected direct stream URL: %q", gotURL)
	}
}

func TestSettingsCredentialStoreAndResetProgress(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		ud := &emby.UserData{PlaybackPositionTicks: emby.DurationToTicks(10 * time.Minute)}
		f.children[f.views[0].ID][0].UserData = ud
		it := f.children[f.views[0].ID][0]
		f.resume = []emby.Item{it}
	})
	a.refresh()
	settle(tt)

	// Home shows Home-m-0 in Continue Watching.
	a.waitFor(t, tt, "continue watching", func() bool {
		return len(a.home.data.resume) > 0 && tt.HasText("Home-m-0")
	})
	if len(a.home.data.resume) == 0 || a.home.data.resume[0].ID != "Home-m-0" {
		t.Fatalf("unexpected initial resume list: %+v", a.home.data.resume)
	}

	// Context menu on resumable card offers "清除播放进度" and clears resume immediately.
	if err := tt.RightClick("Home-m-0"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem(str.ResetProgress); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if slices.ContainsFunc(a.home.data.resume, func(it emby.Item) bool { return it.ID == "Home-m-0" }) {
		t.Fatalf("Home-m-0 remained in resume list after reset: %+v", a.home.data.resume)
	}

	// Settings About displays credential store state, including plaintext warning when fallback active.
	a.navigate(pathSettings)
	settle(tt)
	if !tt.HasText(str.CredentialStore) || !tt.HasText(str.CredentialKeychain) {
		t.Fatalf("settings missing keychain fact: %q", tt.Texts())
	}
	a.plainTokens = true
	tt.Frame()
	if !tt.HasText(str.CredentialPlaintext) {
		t.Fatalf("settings did not show plaintext token fallback warning: %q", tt.Texts())
	}
}

func TestLibraryQueryNewSorts(t *testing.T) {
	movies := library{ID: "lib", Kind: emby.CollectionMovies}
	qPlayed := libraryQuery(movies, "", libPrefs{sort: sortPlayed, desc: true}, 0, 60)
	if !slices.Equal(qPlayed.SortBy, []string{emby.SortDatePlayed, emby.SortName}) || qPlayed.SortOrder != emby.Descending {
		t.Fatalf("sortPlayed query = %+v", qPlayed)
	}
	qRuntime := libraryQuery(movies, "", libPrefs{sort: sortRuntime, desc: true}, 0, 60)
	if !slices.Equal(qRuntime.SortBy, []string{emby.SortRuntime, emby.SortName}) || qRuntime.SortOrder != emby.Descending {
		t.Fatalf("sortRuntime query = %+v", qRuntime)
	}
}

func TestBoxSetDetailAndSmartCollectionPlay(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		box := emby.Item{
			ID:       "box-lotr",
			Name:     "指环王三部曲",
			Type:     emby.TypeBoxSet,
			IsFolder: true,
			Overview: "中土世界史诗三部曲合集。",
		}
		f.children[f.views[0].ID] = append(f.children[f.views[0].ID], box)
		f.children["box-lotr"] = []emby.Item{
			{
				ID: "lotr-1", Name: "护戒使者", Type: emby.TypeMovie, ProductionYear: 2001,
				RunTimeTicks: emby.DurationToTicks(178 * time.Minute),
				UserData:     &emby.UserData{Played: true},
			},
			{
				ID: "lotr-2", Name: "双塔奇兵", Type: emby.TypeMovie, ProductionYear: 2002,
				RunTimeTicks: emby.DurationToTicks(179 * time.Minute),
			},
		}
	})

	// Opening a BoxSet without a library ID (as from Search or Home) opens its detail page.
	a.openItem(&emby.Item{ID: "box-lotr", Name: "指环王三部曲", Type: emby.TypeBoxSet, IsFolder: true}, "")
	settle(tt)
	a.waitFor(t, tt, "boxset children", func() bool {
		return tt.HasText("双塔奇兵") && tt.HasText("中土世界史诗三部曲合集。")
	})
	if !tt.HasText("2 项") {
		t.Fatalf("missing collection item count: %q", tt.Texts())
	}

	// Clicking Play Collection skips already-watched film 1 and starts unwatched film 2.
	if err := tt.Click(str.PlayCollection); err != nil {
		t.Fatal(err)
	}
	if a.lastPlay == nil || a.lastPlay.item.ID != "lotr-2" {
		t.Fatalf("PlayCollection picked %+v, want lotr-2", a.lastPlay)
	}
}

func TestExternalMetadataLinksAndSchemeSafety(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	var openedURL string
	a.onOpenURL = func(u string) { openedURL = u }

	f.set(func(f *fakeServer) {
		it := &f.children[f.views[0].ID][0]
		it.ExternalURLs = []emby.ExternalURL{
			{Name: "IMDb", URL: "https://www.imdb.com/title/tt0133093/"},
			{Name: "TMDb", URL: "https://www.themoviedb.org/movie/603"},
			{Name: "Unsafe", URL: "javascript:alert(1)"},
		}
	})

	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "external links", func() bool { return tt.HasText("IMDb") })

	if tt.HasText("Unsafe") {
		t.Fatal("unsafe javascript: URL chip should not render")
	}
	if err := tt.Click("IMDb"); err != nil {
		t.Fatal(err)
	}
	if openedURL != "https://www.imdb.com/title/tt0133093/" {
		t.Fatalf("openedURL = %q", openedURL)
	}
}
