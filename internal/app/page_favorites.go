package app

import (
	"context"
	"errors"
	"sync"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Favorites (DESIGN.md §4.7): the films, shows and episodes the user keeps,
// each kind a shelf. One taken out of the favorites, here or anywhere,
// leaves at once.

// favState is the favorites loaded: films, shows, episodes.
type favState struct {
	groups  [3][]emby.Item
	started bool
	loading bool
	loaded  bool
	err     error
	reload  int
	gen     gen
}

var favTypes = [3]string{emby.TypeMovie, emby.TypeSeries, emby.TypeEpisode}

// ensureFavorites loads the favorites unless they are loaded, or loading,
// since the last ⌘R.
func (a *app) ensureFavorites() {
	f := a.favs
	if a.client == nil || f.loading || (f.started && f.reload == a.reload) {
		return
	}
	f.started, f.loading, f.reload = true, true, a.reload
	n := f.gen.next()
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) ([3][]emby.Item, error) {
		var (
			out  [3][]emby.Item
			errs [3]error
			wg   sync.WaitGroup
		)
		for i, typ := range favTypes {
			wg.Go(func() { out[i], errs[i] = cl.Favorites(ctx, typ) })
		}
		wg.Wait()
		return out, errors.Join(errs[:]...)
	}, func(groups [3][]emby.Item, err error) {
		if a.epoch != epoch || a.favs != f || !f.gen.is(n) {
			return
		}
		f.loading = false
		if err != nil && !f.loaded {
			f.err = err
			return
		}
		f.groups, f.loaded, f.err = groups, true, nil
	})
}

func (a *app) favoritesPage(c *ui.Context, r *ui.Route) {
	a.ensureFavorites()
	f := a.favs
	spec := pageSpec{Title: str.Favorites, Actions: []barAction{{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh}}}
	a.scrollPage(c, r, spec, func() {
		switch {
		case !f.loaded && f.err != nil:
			a.errorState(c, a.errText(f.err), func() { f.started = false; a.ensureFavorites() })
		case !f.loaded:
			ui.Column(c.Key("skeleton")).Gap(shelfGap).Children(func() {
				a.skeletonShelf(c, "s1", false)
				a.skeletonShelf(c, "s2", true)
			})
		default:
			titles := [3]string{str.SearchMovies, str.SearchSeries, str.SearchEpisodes}
			shown := 0
			ui.Column(c.Key("favorites")).Gap(shelfGap).Children(func() {
				for i, items := range f.groups {
					var kept []emby.Item
					for _, it := range items {
						if it.IsFavorite() {
							kept = append(kept, it)
						}
					}
					shown += len(kept)
					a.itemShelf(c, "fav-"+favTypes[i], titles[i], kept, "", i == 2, false, nil)
				}
				if shown == 0 {
					a.emptyState(c, emptySpec{Icon: iconHeart, Title: str.FavoritesEmpty, Message: str.FavoritesEmptyBody})
				}
			})
		}
	})
}
