package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Navigation
//
// The pages are a ui.Router's: a page is a path, and the router keeps the
// history. It is chosen over a stack of the app's own because it already
// does what a Mac app's navigation needs: going back finds a page as it
// was, scrolled where it was and with the focus where it was (it keeps the
// state of the pages in its history, ten either way); pages fade into each
// other, or slide where one is deeper, and only fade under Reduce Motion;
// ⌘[ and ⌘], the mouse's side buttons and two-finger swipes go back and
// forward without any code of ours; and screen readers hear the title of
// each page. The app keeps nothing per page itself: what a page needs
// (where it is scrolled, its grid's place) lives in ui.Local state of its
// elements, which the router keeps and lets go with the page.
//
// Build paths with the helpers below and go there with a.navigate. To add
// a page, write page_<name>.go with a method
//
//	func (a *app) namePage(c *ui.Context, r *ui.Route)
//
// that builds it with a.scrollPage or a.gridPage (shell.go), and add its
// case to pages.

const (
	pathHome      = "/home"
	pathFavorites = "/favorites"
	pathSettings  = "/settings"
	pathGallery   = "/dev/gallery" // with EMBY_DEV=1 only
)

// pathSearch is the search page for q.
func pathSearch(q string) string { return "/search?" + url.Values{"q": {q}}.Encode() }

// pathLibrary is the page of the library with the ID id.
func pathLibrary(id string) string { return "/library/" + url.PathEscape(id) }

// pathItem is the detail page of an item: a movie, a series, an episode, …
func pathItem(id string) string { return "/item/" + url.PathEscape(id) }

// pathPerson is the page of an actor or a director.
func pathPerson(id string) string { return "/person/" + url.PathEscape(id) }

// pages builds the page of r: the one switch where every page is listed.
func (a *app) pages(c *ui.Context, r *ui.Route) {
	switch {
	case r.Match(pathHome):
		a.homePage(c, r)
	case r.Match(pathFavorites):
		a.favoritesPage(c, r)
	case r.Match("/search"):
		a.searchPage(c, r)
	case r.Match("/library/{id}"):
		a.libraryPage(c, r, r.Param("id"), "")
	case r.Match("/library/{id}/{folder}"):
		a.libraryPage(c, r, r.Param("id"), r.Param("folder"))
	case r.Match("/item/{id}"):
		a.itemPage(c, r, r.Param("id"))
	case r.Match("/person/{id}"):
		a.personPage(c, r, r.Param("id"))
	case r.Match(pathSettings):
		a.settingsPage(c, r)
	case r.Match(pathGallery) && a.dev:
		a.galleryPage(c, r)
	default:
		a.scrollPage(c, r, pageSpec{Title: str.NotFound}, func() {
			a.emptyState(c, emptySpec{Icon: iconInfo, Title: str.NotFound, Message: str.NotFoundBody,
				Action: str.Home, OnAction: func() { a.navigate(pathHome) }})
		})
	}
}

// placeholderPage stands for a page a later version brings.
func (a *app) placeholderPage(c *ui.Context, r *ui.Route, title string, icon *ui.SVG) {
	a.scrollPage(c, r, pageSpec{Title: title}, func() {
		a.emptyState(c, emptySpec{Icon: icon, Title: str.ComingLater, Message: fmt.Sprintf(str.ComingLaterFmt, title)})
	})
}

// navigate goes to the page at path, after the page shown, as a link does.
// A detail page (an item or a person) remembers the sidebar's section it
// was opened from, in its query, so the sidebar keeps showing where the
// user is, going back and forward too. Going to the page shown scrolls it
// to its top instead, as a second click on its sidebar row does.
func (a *app) navigate(path string) {
	if a.screen != screenMain {
		return
	}
	if isDetail(path) && !strings.Contains(path, "from=") {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + url.Values{"from": {a.section()}}.Encode()
	}
	if path == a.router.Location() {
		a.scrollToTop()
		return
	}
	a.router.Push(path)
	a.prefetch(path)
}

// prefetch starts loading what the page at path shows, unless it is loaded
// or loading: as the user goes there, and not only as the page builds. A
// window out of sight builds no frames (MyGo holds them while nothing of
// it shows), so a load the view alone asked for would wait until the
// window shows again. The pages ask too, as they build, for the ways of
// going to a page that do not come through here (the router's own keys
// and swipes); asking twice loads once.
func (a *app) prefetch(path string) {
	if a.client == nil {
		return
	}
	u, err := url.Parse(path)
	if err != nil {
		return
	}
	p := u.Path
	switch {
	case p == pathHome:
		a.ensureHome()
	case strings.HasPrefix(p, "/library/"):
		libID, folder, _ := strings.Cut(strings.TrimPrefix(p, "/library/"), "/")
		if lib, ok := a.libraryByID(libID); ok {
			a.list(lib, folder, a.prefsFor(lib.ID))
		}
	case strings.HasPrefix(p, "/item/"):
		a.detail(strings.TrimPrefix(p, "/item/"))
	case strings.HasPrefix(p, "/person/"):
		a.person(strings.TrimPrefix(p, "/person/"))
	case p == pathFavorites:
		a.ensureFavorites()
	}
}

// isDetail reports whether path is a page opened from another, not one of
// the sidebar's.
func isDetail(path string) bool {
	return strings.HasPrefix(path, "/item/") || strings.HasPrefix(path, "/person/")
}

// Sidebar sections: what a.section returns.
const (
	sectionHome      = "home"
	sectionFavorites = "favorites"
	sectionSearch    = "search"
	sectionSettings  = "settings"
	sectionGallery   = "gallery"
	sectionLibPrefix = "lib:"
)

// section returns the sidebar's entry the page shown belongs to:
// sectionHome, sectionFavorites, sectionLibPrefix and a library's ID, …, or
// "" for none.
func (a *app) section() string {
	path := a.router.Path()
	switch {
	case path == pathHome:
		return sectionHome
	case path == pathFavorites:
		return sectionFavorites
	case path == pathSettings:
		return sectionSettings
	case path == pathGallery:
		return sectionGallery
	case path == "/search":
		return sectionSearch
	case strings.HasPrefix(path, "/library/"):
		id, _, _ := strings.Cut(strings.TrimPrefix(path, "/library/"), "/")
		id, _ = url.PathUnescape(id)
		return sectionLibPrefix + id
	case isDetail(path):
		return a.router.Query("from")
	}
	return ""
}

// scrollRequest is a scroll the page shown takes as it next builds.
type scrollRequest struct {
	y        float32
	animated bool
}

// scrollToTop scrolls the page shown to its top, smoothly.
func (a *app) scrollToTop() { a.scrollReq = &scrollRequest{y: 0, animated: true} }

// scrollPageTo scrolls the page shown to y at once, as the screenshots do.
func (a *app) scrollPageTo(y float32) { a.scrollReq = &scrollRequest{y: y} }

// takeScroll applies a scroll asked of the page shown to its state.
func (a *app) takeScroll(st *ui.ScrollState) {
	req := a.scrollReq
	if req == nil {
		return
	}
	a.scrollReq = nil
	if req.animated {
		st.ScrollTo(st.X, req.y, ui.Smooth)
		return
	}
	st.Y = req.y
}
