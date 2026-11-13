package app

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// testApp is the app without a window: what update posts waits in a queue
// that the test runs, as the main thread would.
type testApp struct {
	*app
	mu     sync.Mutex
	posted []func()
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	ta := &testApp{app: newApp("", "")}
	ta.postFn = func(fn func()) {
		ta.mu.Lock()
		ta.posted = append(ta.posted, fn)
		ta.mu.Unlock()
	}
	t.Cleanup(ta.img.Close)
	return ta
}

// runPosted runs what goroutines posted so far.
func (ta *testApp) runPosted() int {
	ta.mu.Lock()
	fns := ta.posted
	ta.posted = nil
	ta.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
	return len(fns)
}

// settle runs frames until the router's transitions are over.
func settle(tt *ui.Tester) {
	tt.Frame()
	tt.Advance(time.Second)
}

// signedInApp is an app signed in to a fake server, showing Home.
func signedInApp(t *testing.T, w, h int) (*testApp, *ui.Tester, *fakeServer) {
	t.Helper()
	f := newFakeServer(t, "Home")
	a := newTestApp(t)
	tt := ui.NewTester(a.view, w, h)
	a.signIn(t, tt, f)
	return a, tt, f
}

func TestNavigationPushBackForward(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	movies := f.views[0].ID
	if a.router.Path() != pathHome || a.section() != sectionHome {
		t.Fatalf("starts at %q, section %q", a.router.Path(), a.section())
	}

	a.navigate(pathLibrary(movies))
	settle(tt)
	if a.router.Path() != pathLibrary(movies) || a.section() != sectionLibPrefix+movies {
		t.Fatalf("at %q, section %q", a.router.Path(), a.section())
	}

	a.navigate(pathItem("abc"))
	settle(tt)
	if !strings.HasPrefix(a.router.Location(), "/item/abc?from=") {
		t.Fatalf("item at %q", a.router.Location())
	}
	if a.section() != sectionLibPrefix+movies {
		t.Errorf("a detail page keeps its section: %q", a.section())
	}

	a.back()
	settle(tt)
	if a.router.Path() != pathLibrary(movies) || !a.router.CanGoForward() {
		t.Fatalf("back at %q, forward %v", a.router.Path(), a.router.CanGoForward())
	}
	a.forward()
	settle(tt)
	if a.router.Path() != "/item/abc" || a.section() != sectionLibPrefix+movies {
		t.Fatalf("forward at %q, section %q", a.router.Path(), a.section())
	}

	// The bar's back button and ⌘[ go back too.
	if err := tt.Click(str.Back); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathLibrary(movies) {
		t.Fatalf("the back button went to %q", a.router.Path())
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	settle(tt)
	if a.router.Path() != pathHome {
		t.Fatalf("⌘[ went to %q", a.router.Path())
	}
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	settle(tt)
	if a.router.Path() != pathLibrary(movies) {
		t.Fatalf("⌘] went to %q", a.router.Path())
	}
}

func TestSidebarFollowsRoute(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	shows := f.views[1].ID
	current := func(name string) bool {
		info, ok := tt.AccessByRole(ui.RoleButton, name)
		if !ok {
			t.Fatalf("no sidebar row %q", name)
		}
		return info.Current == ui.CurrentPage
	}
	if !current(str.Home) || current(str.Favorites) {
		t.Fatal("Home is not the current row")
	}
	if err := tt.Click("剧集"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathLibrary(shows) || !current("剧集") || current(str.Home) {
		t.Fatalf("clicking a library: at %q", a.router.Path())
	}
	a.navigate(pathItem("x"))
	settle(tt)
	if !current("剧集") {
		t.Error("a detail page opened from a library leaves its row current")
	}
	a.navigate(pathFavorites)
	settle(tt)
	if !current(str.Favorites) || current("剧集") {
		t.Error("Favorites is not current")
	}
	a.back()
	settle(tt)
	if !current("剧集") {
		t.Error("going back to the detail page makes its library current again")
	}
	// The gallery is the developer's alone.
	if _, ok := tt.AccessByRole(ui.RoleButton, str.Gallery); ok {
		t.Error("the gallery shows without EMBY_DEV")
	}
}

func TestSidebarSearch(t *testing.T) {
	a, tt, _ := signedInApp(t, 1280, 820)
	a.searchBox.Focus()
	tt.Frame()
	tt.Type("llama")
	settle(tt)
	if a.router.Path() != "/search" || a.router.Query("q") != "llama" {
		t.Fatalf("typing went to %q", a.router.Location())
	}
	tt.Type(" drama")
	settle(tt)
	if a.router.Query("q") != "llama drama" {
		t.Fatalf("typing on went to %q", a.router.Location())
	}
	tt.Key(0, ui.KeyEscape)
	settle(tt)
	if a.router.Path() != pathHome || a.search != "" {
		t.Fatalf("clearing went to %q, field %q", a.router.Location(), a.search)
	}
}

func TestPagesKeepScrollInHistory(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	f.set(func(f *fakeServer) { f.children[f.views[0].ID] = fakeItems("m", 120, "Movie") })
	a.navigate(pathLibrary(f.views[0].ID))
	settle(tt)
	a.waitFor(t, tt, "the library", func() bool { return tt.HasText("m-0") })
	settle(tt)
	tt.Scroll(800, 500, 0, 900)
	tt.Advance(time.Second)
	if !a.bar.titleIn {
		t.Fatal("scrolled, the bar shows no title")
	}
	a.navigate(pathFavorites)
	settle(tt)
	a.back()
	settle(tt)
	if r, _ := tt.Find("m-0"); r.H > 0 && r.Y > barHeight {
		t.Error("going back found the library at its top, not where it was scrolled")
	}
	if !a.bar.titleIn {
		t.Error("going back, the bar lost the page's title")
	}
}

// While the player plays, frames build it alone, not the router: a page
// comes back where it was scrolled, as do its grid and shelves, which keep
// their place in the app (pagemem.go), not in their elements.
func TestPagesKeepScrollWhileNotBuilt(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	f.set(func(f *fakeServer) { f.children[f.views[0].ID] = fakeItems("m", 120, "Movie") })
	a.navigate(pathLibrary(f.views[0].ID))
	settle(tt)
	a.waitFor(t, tt, "the library", func() bool { return tt.HasText("m-0") })
	settle(tt)
	tt.Scroll(800, 500, 0, 900)
	tt.Advance(time.Second)
	// Frames that build something else (the sign-in screen stands in for
	// the player, which needs a window), for a while.
	a.screen = screenLogin
	for range 30 {
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
	}
	if tt.HasText("m-0") {
		t.Fatal("the library still built while covered")
	}
	a.screen = screenMain
	settle(tt)
	if r, _ := tt.Find("m-0"); r.H > 0 && r.Y > barHeight {
		t.Error("after frames without the router, the library was back at its top")
	}
	if !a.bar.titleIn {
		t.Error("after frames without the router, the bar lost the page's title")
	}
}

// Every control has a name, for screen readers and tooltips.
func TestControlsAreNamed(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	check := func(where string) {
		for _, info := range tt.AccessAll() {
			if info.Role == ui.RoleButton && strings.TrimSpace(info.Name) == "" {
				t.Errorf("%s: a button at %v has no name", where, info.Bounds)
			}
		}
	}
	check("sign-in")
	f := newFakeServer(t, "Named")
	a.signIn(t, tt, f)
	for _, path := range []string{pathHome, pathLibrary(f.views[0].ID), pathFavorites} {
		a.navigate(path)
		settle(tt)
		a.waitFor(t, tt, path, func() bool { return true })
		check(path)
	}
	a.openServers()
	settle(tt)
	check("servers")
}

func TestDarkAndMinimumSize(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTesterWith(a.view, 960, 620, ui.TesterOptions{Dark: true})
	if !a.pal.dark || a.pal.bg != paletteFor(ui.DarkTheme()).bg {
		t.Error("the dark theme gave no dark palette")
	}
	// The sign-in panel fits the smallest window, whole.
	for _, text := range []string{str.LoginTitle, str.ServerAddress, str.SignIn} {
		if r, ok := tt.Find(text); !ok || r.Y < 0 || r.Y+r.H > 620 || r.X < 0 || r.X+r.W > 960 {
			t.Errorf("%q at %v, outside the window", text, r)
		}
	}
	tt.SetDark(false)
	tt.Frame()
	if a.pal.dark {
		t.Error("the palette did not follow to light")
	}
}

func TestGridCardsFillColumnsAndLoadMore(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	movies := f.views[0].ID
	f.set(func(f *fakeServer) { f.children[movies] = fakeItems("m", 150, "Movie") })
	a.navigate(pathLibrary(movies))
	settle(tt)
	a.waitFor(t, tt, "the first page", func() bool { return tt.HasText("m-0") })
	settle(tt) // the grid lays out from its own width after its first frame
	first, ok := tt.AccessByRole(ui.RoleButton, "m-0")
	if !ok {
		t.Fatal("no first card")
	}
	// In the content's gutter, after the sidebar and its hairline.
	if d := first.Bounds.X - (sidebarWidth + 1 + gutter); d < -1 || d > 1 {
		t.Errorf("the grid starts at %v, not in the gutter", first.Bounds.X)
	}
	second, _ := tt.AccessByRole(ui.RoleButton, "m-1")
	if gap := second.Bounds.X - first.Bounds.X - first.Bounds.W; gap < cardGap-1 || gap > cardGap+1 {
		t.Errorf("cards stand %v apart, not %d", gap, cardGap)
	}
	// The card's picture is 2:3 and its text fits under it in the cell.
	wantH := first.Bounds.W*1.5 + cardTextH
	if d := first.Bounds.H - wantH; d < -2 || d > 2 {
		t.Errorf("a card is %vx%v, want %v high", first.Bounds.W, first.Bounds.H, wantH)
	}

	// Scrolled to its end, the grid asks for the next pages, one at a
	// time, until it has them all.
	for range 3 {
		for range 30 {
			tt.Scroll(800, 500, 0, 3000)
		}
		tt.Advance(time.Second)
		a.waitFor(t, tt, "the next page", func() bool {
			ls := a.lists[listKey(a.libraries[0], "", libPrefs{})]
			return !ls.loading
		})
	}
	qs := f.itemQueries(movies)
	var starts []string
	for _, q := range qs {
		starts = append(starts, q.Get("StartIndex"))
	}
	if strings.Join(starts, ",") != ",60,120" {
		t.Errorf("asked for pages from %q, want \"\",60,120", starts)
	}
	if ls := a.lists[listKey(a.libraries[0], "", libPrefs{})]; len(ls.items) != 150 || ls.total != 150 {
		t.Errorf("the list has %d items of %d", len(ls.items), ls.total)
	}
}
