package app

import (
	"context"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

const genreWorksLimit = 200

type genreState struct {
	items   []emby.Item
	started bool
	loading bool
	loaded  bool
	err     error
	reload  int
	gen     gen
}

// genre returns the cached or freshly loaded list of titles matching name.
func (a *app) genre(name string) *genreState {
	gs := a.genres[name]
	if gs == nil {
		gs = &genreState{}
		a.genres[name] = gs
	}
	if a.client != nil && !gs.loading && (!gs.started || gs.reload != a.reload) {
		a.loadGenre(gs, name)
	}
	return gs
}

func (a *app) loadGenre(gs *genreState, name string) {
	gs.started, gs.loading, gs.reload = true, true, a.reload
	n := gs.gen.next()
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) ([]emby.Item, error) {
		res, err := cl.Items(ctx, emby.ItemQuery{
			Recursive:        true,
			IncludeItemTypes: []string{emby.TypeMovie, emby.TypeSeries},
			Genres:           []string{name},
			SortBy:           []string{emby.SortName},
			SortOrder:        emby.Ascending,
			Limit:            genreWorksLimit,
		})
		if err != nil {
			return nil, err
		}
		return res.Items, nil
	}, func(items []emby.Item, err error) {
		if a.epoch != epoch || !gs.gen.is(n) {
			return
		}
		gs.loading = false
		if err != nil {
			gs.err = err
			return
		}
		gs.items, gs.err, gs.loaded = items, nil, true
	})
}

func (a *app) genrePage(c *ui.Context, r *ui.Route, name string) {
	gs := a.genre(name)
	spec := pageSpec{
		Title:   name,
		Eyebrow: str.Genre,
		Actions: []barAction{{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh}},
	}
	if !gs.loaded {
		a.scrollPage(c, r, spec, func() {
			if gs.err != nil {
				a.errorState(c, a.errText(gs.err), func() { a.loadGenre(gs, name) })
				return
			}
			a.loadingState(c)
		})
		return
	}
	items := gs.items
	a.gridPage(c, r, spec, gridSpec{
		Count:    len(items),
		MinWidth: 150,
		Key:      func(i int) any { return items[i].ID },
		Label:    func(i int) string { return items[i].Name },
		Item: func(i int, width float32) {
			a.itemCard(c, &items[i], cardOpts{shown: width})
		},
		Empty: func() {
			a.emptyState(c, emptySpec{Icon: iconFilm, Title: str.GenreEmpty})
		},
	})
}
