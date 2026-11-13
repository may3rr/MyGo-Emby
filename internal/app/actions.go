package app

import (
	"context"
	"slices"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// What the user does to an item, from its card's menu or its page: play
// it, mark it watched or not, add it to the favorites or take it out. The
// change shows at once, everywhere the item shows, and goes back if the
// server refuses it.

// itemMenu is the context menu of an item's card (DESIGN.md §4.9).
func (a *app) itemMenu(it *emby.Item) func(m *ui.Menu) {
	item := *it
	return func(m *ui.Menu) {
		cur := a.current(item)
		if (cur.IsPlayable() || cur.Type == emby.TypeSeries) && m.Item(str.Play).Chosen() {
			a.play(&cur, playOptions{})
		}
		m.Separator()
		played := str.MarkPlayed
		if cur.IsPlayed() {
			played = str.MarkUnplayed
		}
		if m.Item(played).Chosen() {
			a.setPlayed(&cur, !cur.IsPlayed())
		}
		fav := str.AddFavorite
		if cur.IsFavorite() {
			fav = str.RemoveFavorite
		}
		if m.Item(fav).Chosen() {
			a.setFavorite(&cur, !cur.IsFavorite())
		}
	}
}

// current returns the item as the app knows it now: a card's copy may
// predate a change made elsewhere.
func (a *app) current(it emby.Item) emby.Item {
	found := false
	a.eachItem(func(x *emby.Item) {
		if !found && x.ID == it.ID {
			it.UserData, found = x.UserData, true
		}
	})
	return it
}

// eachItem calls fn with every item the app holds, in Home, the lists, the
// pages of items and people, the favorites and the search.
func (a *app) eachItem(fn func(it *emby.Item)) {
	each := func(items []emby.Item) {
		for i := range items {
			fn(&items[i])
		}
	}
	if h := a.home; h != nil {
		each(h.data.resume)
		each(h.data.nextUp)
		for _, s := range h.data.latest {
			each(s.items)
		}
		for i := range h.data.hero {
			fn(&h.data.hero[i].item)
		}
	}
	for _, ls := range a.lists {
		each(ls.items)
	}
	for _, d := range a.details {
		if d.item != nil {
			fn(d.item)
		}
		if d.next != nil {
			fn(d.next)
		}
		each(d.similar)
		each(d.seasons)
		for _, eps := range d.episodes {
			each(eps.items)
		}
	}
	for _, p := range a.people {
		each(p.works)
	}
	if f := a.favs; f != nil {
		for _, g := range f.groups {
			each(g)
		}
	}
	if s := a.found; s != nil && s.res != nil {
		each(s.res.Movies)
		each(s.res.Series)
		each(s.res.Episodes)
		each(s.res.People)
		each(s.res.BoxSets)
	}
}

// applyUserData shows ud as the user's state of the item with the ID id,
// wherever it shows. An item watched to its end leaves Continue Watching.
func (a *app) applyUserData(id string, ud emby.UserData) {
	a.eachItem(func(it *emby.Item) {
		if it.ID == id {
			u := ud
			it.UserData = &u
		}
	})
	if h := a.home; h != nil && ud.Played {
		h.data.resume = slices.DeleteFunc(h.data.resume, func(it emby.Item) bool { return it.ID == id })
	}
	a.redraw() // a menu's choice comes as a frame is built
}

// userData returns the user's state of it, never nil.
func userData(it *emby.Item) emby.UserData {
	if it.UserData == nil {
		return emby.UserData{}
	}
	return *it.UserData
}

// setFavorite adds it to the favorites, or takes it out.
func (a *app) setFavorite(it *emby.Item, on bool) {
	before := userData(it)
	after := before
	after.IsFavorite = on
	a.changeUserData(it, before, after, func(ctx context.Context, cl *emby.Client) (*emby.UserData, error) {
		return cl.SetFavorite(ctx, it.ID, on)
	})
}

// setPlayed marks it watched, or not: a series or a season with all its
// episodes.
func (a *app) setPlayed(it *emby.Item, on bool) {
	before := userData(it)
	after := before
	after.Played = on
	after.PlaybackPositionTicks = 0
	after.PlayedPercentage = 0
	if on {
		after.UnplayedItemCount = 0
	}
	folder := it.IsFolderLike()
	a.changeUserData(it, before, after, func(ctx context.Context, cl *emby.Client) (*emby.UserData, error) {
		return cl.SetPlayed(ctx, it.ID, on)
	})
	if folder {
		a.staleEpisodes = true // the episodes changed with it
	}
}

// changeUserData shows after at once, asks the server, and shows what the
// server answers, or before again where it refused.
func (a *app) changeUserData(it *emby.Item, before, after emby.UserData, call func(ctx context.Context, cl *emby.Client) (*emby.UserData, error)) {
	id := it.ID
	a.applyUserData(id, after)
	load(a, func(ctx context.Context, cl *emby.Client) (*emby.UserData, error) {
		return call(ctx, cl)
	}, func(ud *emby.UserData, err error) {
		if err != nil {
			a.applyUserData(id, before)
			a.toast = a.errText(err)
			return
		}
		if ud != nil {
			got := *ud
			// Emby answers favorites with the item's state; keep what the
			// answer leaves out as shown.
			if got.Key == "" && got.ItemID == "" && got == (emby.UserData{}) {
				got = after
			}
			a.applyUserData(id, got)
		}
		if a.staleEpisodes {
			a.staleEpisodes = false
			for _, d := range a.details {
				d.episodes = map[string]*episodeList{}
			}
		}
	})
}
