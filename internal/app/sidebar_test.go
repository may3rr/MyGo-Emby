package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSidebarToggleContentNavigationAndSearch(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	a.opaqueSidebar = true
	tt.SetVibrancy(true)
	titleBar := ui.TitleBar{Height: 52, Left: 76, Right: 76, Bottom: 34}
	tt.SetTitleBar(titleBar)

	lib := f.views[0].ID
	f.set(func(f *fakeServer) { f.children[lib] = fakeItems("sidebar-m", 120, "Movie") })
	a.navigate(pathLibrary(lib))
	settle(tt)
	a.waitFor(t, tt, "the sidebar library", func() bool { return tt.HasText("sidebar-m-0") })
	settle(tt)

	expandedCard, ok := tt.AccessByRole(ui.RoleButton, "sidebar-m-0")
	if !ok {
		t.Fatal("the expanded page has no first card")
	}
	if d := expandedCard.Bounds.X - (sidebarWidth + 1 + gutter); d < -1 || d > 1 {
		t.Fatalf("the expanded page starts at %v, want %d", expandedCard.Bounds.X, sidebarWidth+1+gutter)
	}

	tt.Scroll(900, 500, 0, 900)
	tt.Advance(time.Second)
	menu := a.pageMems[pathLibrary(lib)]
	if menu == nil || menu.grid.scroll.Y <= 0 {
		t.Fatalf("the page did not scroll: %+v", menu)
	}
	scrolled := menu.grid.scroll.Y

	if err := tt.Click(str.CollapseSidebar); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	settle(tt)
	if !a.settings.SidebarCollapsed {
		t.Fatal("the button did not collapse the sidebar")
	}
	if a.router.Path() != pathLibrary(lib) {
		t.Fatalf("collapsing changed the route to %q", a.router.Location())
	}
	if got := a.pageMems[pathLibrary(lib)].grid.scroll.Y; got <= 0 || got > scrolled {
		t.Errorf("collapsing lost the page position: %v, was %v", got, scrolled)
	}
	if !a.bar.titleIn {
		t.Error("collapsing moved the page title out of the tool bar")
	}
	if _, ok := tt.AccessByRole(ui.RoleButton, str.Home); ok {
		t.Error("the sidebar controls remain accessible while collapsed")
	}
	toggle, ok := tt.AccessByRole(ui.RoleButton, str.ExpandSidebar)
	if !ok {
		t.Fatal("the collapsed sidebar has no expand button")
	}
	if toggle.Bounds.X < titleBar.Left {
		t.Errorf("the toolbar button at %v overlaps the title bar controls", toggle.Bounds.X)
	}
	if got := tt.Image().RGBAAt(900, 700).A; got != 255 {
		t.Errorf("the collapsed content is not opaque: alpha %d", got)
	}
	collapsedCard, ok := tt.AccessByRole(ui.RoleButton, "sidebar-m-0")
	if !ok {
		t.Fatal("the collapsed page has no first card")
	}
	if d := collapsedCard.Bounds.X - gutter; d < -1 || d > 1 {
		t.Errorf("the collapsed page starts at %v, want %d", collapsedCard.Bounds.X, gutter)
	}

	if err := tt.Click(str.Back); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathHome {
		t.Fatalf("Back while collapsed went to %q", a.router.Location())
	}
	if err := tt.Click(str.Forward); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathLibrary(lib) {
		t.Fatalf("Forward while collapsed went to %q", a.router.Location())
	}
	if got := a.pageMems[pathLibrary(lib)].grid.scroll.Y; got <= 0 {
		t.Errorf("Forward returned the page at its top instead of its previous position")
	}
	if !a.bar.titleIn {
		t.Error("Forward returned the page without its scrolled title state")
	}
	if err := tt.Click(str.ExpandSidebar); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.settings.SidebarCollapsed {
		t.Fatal("the button did not expand the sidebar")
	}
	if got := a.pageMems[pathLibrary(lib)].grid.scroll.Y; got <= 0 {
		t.Errorf("expanding returned the page to its top")
	}
	if !a.bar.titleIn {
		t.Error("expanding lost the scrolled title state")
	}
	if err := tt.Click(str.Favorites); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if a.router.Path() != pathFavorites {
		t.Fatalf("the restored sidebar went to %q", a.router.Location())
	}

	tt.Key(sidebarShortcutMods(), ui.KeyS)
	settle(tt)
	if !a.settings.SidebarCollapsed {
		t.Fatal("the sidebar shortcut did not collapse")
	}
	tt.Key(ui.Cmd, ui.KeyF)
	settle(tt)
	if a.settings.SidebarCollapsed {
		t.Fatal("Find did not expand the sidebar")
	}
	tt.Type("sidebar")
	settle(tt)
	if a.router.Path() != "/search" || a.router.Query("q") != "sidebar" {
		t.Fatalf("Find did not focus search: at %q", a.router.Location())
	}
}

func TestSidebarScreenshots(t *testing.T) {
	dir := os.Getenv("MYGO_SIDEBAR_SHOTS")
	if dir == "" {
		t.Skip("set MYGO_SIDEBAR_SHOTS to write sidebar screenshots")
	}
	f := newFakeServer(t, "Sidebar")
	f.set(func(f *fakeServer) {
		f.resume = fakeItems("resume", 6, "Movie")
		f.latest[f.views[0].ID] = fakeItems("latest", 8, "Movie")
	})
	a := newTestApp(t)
	a.opaqueSidebar = true
	tt := ui.NewTester(a.view, 1280, 820)
	tt.SetVibrancy(true)
	tt.SetTitleBar(ui.TitleBar{Height: 52, Left: 76, Right: 76, Bottom: 34})
	a.signIn(t, tt, f)
	a.waitFor(t, tt, "the screenshot Home", func() bool { return homeReady(a.app) })
	tt.Advance(2 * time.Second)

	writeSidebarShot(t, tt, filepath.Join(dir, "sidebar-expanded.png"))
	if err := tt.Click(str.CollapseSidebar); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	settle(tt)
	writeSidebarShot(t, tt, filepath.Join(dir, "sidebar-collapsed.png"))
}

func writeSidebarShot(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, tt.Image()); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
