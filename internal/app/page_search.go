package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Search (DESIGN.md §4.6): the sidebar's field shows this page as the user
// types (shell.go), and ⌘F puts the keyboard there. The server is asked
// once the typing pauses for searchDelay; the results show in groups:
// films, shows, episodes, people and box sets. Up and Down in the field
// choose a result, Enter opens it. An answer to a query the user typed
// past is dropped.

const (
	searchDelay = 300 * time.Millisecond
	searchLimit = 24
)

// searchState is the search's: what was asked, and what came.
type searchState struct {
	typed   string    // the query as typed, waiting for the pause
	due     time.Time // when it is asked
	query   string    // the query asked last
	res     *emby.SearchResults
	loading bool
	err     error
	gen     gen
	sel     int  // the result chosen with the keys, -1 for none
	moved   bool // the keys moved it: it scrolls into view
}

// searchHit is a result as the list shows it.
type searchHit struct {
	item  *emby.Item
	group string
}

// hits returns the results in the order they show.
func (s *searchState) hits() []searchHit {
	if s.res == nil {
		return nil
	}
	var out []searchHit
	add := func(group string, items []emby.Item) {
		for i := range items {
			out = append(out, searchHit{&items[i], group})
		}
	}
	add(str.SearchMovies, s.res.Movies)
	add(str.SearchSeries, s.res.Series)
	add(str.SearchEpisodes, s.res.Episodes)
	add(str.SearchPeople, s.res.People)
	add(str.SearchBoxSets, s.res.BoxSets)
	return out
}

// typeQuery follows the query in the page's path: a new one waits for the
// pause before it is asked.
func (a *app) typeQuery(q string, now time.Time) {
	s := a.found
	q = strings.TrimSpace(q)
	if q == s.typed {
		return
	}
	s.typed, s.due, s.sel = q, now.Add(searchDelay), -1
	if q == "" {
		s.gen.next() // what is under way answers no more
		s.query, s.res, s.loading, s.err = "", nil, false, nil
	}
}

// tickSearch asks the server for the query typed, once its pause is over.
func (a *app) tickSearch(c *ui.Context) {
	s := a.found
	if s.due.IsZero() || s.typed == "" {
		return
	}
	if wait := s.due.Sub(c.Now()); wait > 0 {
		c.After(wait)
		return
	}
	s.due = time.Time{}
	a.runSearch(s.typed)
}

func (a *app) runSearch(q string) {
	s := a.found
	s.query, s.loading, s.err = q, true, nil
	n := s.gen.next()
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) (*emby.SearchResults, error) {
		return cl.Search(ctx, q, searchLimit)
	}, func(res *emby.SearchResults, err error) {
		if a.epoch != epoch || a.found != s || !s.gen.is(n) {
			return // the user typed on
		}
		s.loading = false
		s.res, s.err, s.sel = res, err, -1
	})
}

// searchKey takes the keys of the sidebar's field meant for the results:
// Up and Down choose one, Enter opens it (the first without a choice).
func (a *app) searchKey(key ui.Key) bool {
	s := a.found
	if a.router.Path() != "/search" || s == nil {
		return false
	}
	hits := s.hits()
	switch key {
	case ui.KeyDown:
		if len(hits) > 0 {
			s.sel, s.moved = min(s.sel+1, len(hits)-1), true
		}
	case ui.KeyUp:
		if s.sel >= 0 {
			s.sel, s.moved = s.sel-1, true
		}
	case ui.KeyEnter:
		if len(hits) == 0 {
			return false
		}
		a.openHit(hits[max(s.sel, 0)])
	default:
		return false
	}
	a.redraw()
	return true
}

// openHit opens a result.
func (a *app) openHit(h searchHit) {
	if h.item.Type == emby.TypePerson {
		a.navigate(pathPerson(h.item.ID))
		return
	}
	a.openItem(h.item, "")
}

func (a *app) searchPage(c *ui.Context, r *ui.Route) {
	q := strings.TrimSpace(r.Query("q"))
	a.typeQuery(q, c.Now())
	a.tickSearch(c)
	s := a.found
	title := str.Search
	if q != "" {
		title = fmt.Sprintf("%s“%s”", str.Search, q)
	}
	a.scrollPage(c, r, pageSpec{Title: title}, func() {
		hits := s.hits()
		switch {
		case q == "":
			a.emptyState(c, emptySpec{Icon: iconSearch, Title: str.SearchHint})
		case s.err != nil && s.query == q:
			a.errorState(c, a.errText(s.err), func() { a.runSearch(q) })
		case s.res == nil || (s.loading && len(hits) == 0):
			a.loadingState(c)
		case len(hits) == 0 && !s.loading:
			a.emptyState(c, emptySpec{Icon: iconSearch, Title: fmt.Sprintf(str.SearchNoneFmt, s.query), Message: str.SearchNoneBody})
		default:
			a.searchResults(c, hits)
		}
	})
}

// searchResults lists the results in their groups.
func (a *app) searchResults(c *ui.Context, hits []searchHit) {
	p := a.pal
	s := a.found
	ui.Column(c.Key("results")).Gap(2).MaxWidth(820).Busy(s.loading).Children(func() {
		group := ""
		for i, h := range hits {
			if h.group != group {
				group = h.group
				n := 0
				for _, x := range hits {
					if x.group == group {
						n++
					}
				}
				ui.Text(c.Key("group-"+group), fmt.Sprintf("%s · %d", group, n)).FontSize(fsHeadline).
					FontWeight(wSemibold).TextColor(p.text).Margin(18, 0, 6, 0).SingleLine()
			}
			a.hitRow(c, h, i == s.sel)
		}
	})
	s.moved = false
}

// hitRow is a result: its picture, its name and a line under it.
func (a *app) hitRow(c *ui.Context, h searchHit, chosen bool) {
	p := a.pal
	it := h.item
	row := ui.ButtonBase(c.Key(it.ID)).Height(68).PaddingX(10).Gap(14).Radius(10).AlignItems(ui.Center).
		Justify(ui.Start).Label(it.Name).AccessSelected(chosen).Margin(0, -10)
	bg := ui.Transparent
	switch {
	case chosen:
		bg = p.accent.Alpha(0.14)
	case row.Pressed():
		bg = p.press
	case row.Hovered():
		bg = p.hover
	}
	row.Background(row.AnimateColor("bg", bg, quick))
	if chosen && a.found.moved {
		row.ScrollIntoView()
	}
	row.Children(func() {
		var w, h2, radius float32 = 38, 56, 6
		role := emby.RolePoster
		switch it.Type {
		case emby.TypeEpisode:
			w, h2, radius, role = 96, 54, 6, emby.RolePrimary
		case emby.TypePerson:
			w, h2, radius, role = 52, 52, 26, emby.RolePrimary
		}
		ui.Box(c.Key("pic")).Size(w, h2).Radius(radius).Shrink(0).Children(func() {
			a.artwork(c, a.itemImage(it, role, w), itemIcon(it), radius)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, it.Name).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine()
			sub := cardSubtitle(it, false)
			if it.Type == emby.TypeEpisode {
				sub = joinNonEmpty(" · ", it.SeriesName, episodeText(it))
			}
			if sub != "" {
				ui.Text(c, sub).FontSize(fsCaption).TextColor(p.text2).SingleLine()
			}
		})
	})
	if it.Type != emby.TypePerson {
		row.ContextMenu(a.itemMenu(it))
	}
	hit := h
	row.OnClick(func() { a.openHit(hit) })
}
