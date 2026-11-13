package app

import "github.com/egoist/mygo/ui"

// pageMem is what a page keeps of itself: where it is scrolled, its grid's
// place, where its shelves are scrolled. It lives in the app, by the
// page's path, not in ui.Local state of the page's elements: while the
// player covers the window, frames build the player alone, and an element
// a frame does not build loses its state (the router's pages too), so the
// page would come back scrolled to its top.
type pageMem struct {
	scroll  ui.ScrollState
	grid    gridView
	shelves map[any]*ui.ScrollState

	// beforeCollapse is where the page stood at its expanded width, kept
	// because the wider grid can clamp the live offset before it expands
	// again.
	beforeCollapse      bool
	collapsedFromScroll float32
	collapsedFromGrid   float32
}

// maxPageMems bounds what pages keep: more than the router's history (ten
// pages either way), and the oldest go first.
const maxPageMems = 48

// pageMemFor returns the memory of the page at r's path, made the first
// time the page builds.
func (a *app) pageMemFor(r *ui.Route) *pageMem {
	key := r.Path()
	if m := a.pageMems[key]; m != nil {
		return m
	}
	if a.pageMems == nil {
		a.pageMems = map[string]*pageMem{}
	}
	if len(a.pageMemOrder) >= maxPageMems {
		delete(a.pageMems, a.pageMemOrder[0])
		a.pageMemOrder = a.pageMemOrder[1:]
	}
	m := &pageMem{}
	a.pageMems[key] = m
	a.pageMemOrder = append(a.pageMemOrder, key)
	return m
}

// shelfScroll returns where the shelf with key is scrolled, in the memory
// of the page building it, or nil outside a page (the gallery's own
// shelves keep theirs in their elements).
func (a *app) shelfScroll(key any) *ui.ScrollState {
	m := a.building
	if m == nil {
		return nil
	}
	if m.shelves == nil {
		m.shelves = map[any]*ui.ScrollState{}
	}
	st := m.shelves[key]
	if st == nil {
		st = &ui.ScrollState{}
		m.shelves[key] = st
	}
	return st
}
