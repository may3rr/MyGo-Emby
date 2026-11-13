package app

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// A library's page: its items in a grid that loads a page more as it nears
// its end, sorted and filtered as the user chooses for that library. A
// library of films, shows, box sets or music videos lists all of them, from
// every folder; any other is browsed folder by folder, a folder opening a
// level down on the same page (/library/{id}/{folder}), which Back leaves.

const libraryPageSize = 60

// Sort fields and filters of a library.
const (
	sortName = iota
	sortAdded
	sortYear
	sortRating
)

const (
	filterAll = iota
	filterUnplayed
	filterFavorites
)

// libPrefs is how the user sorts and filters a library, and its folders.
type libPrefs struct {
	sort   int
	desc   bool
	filter int
}

// sortLabels and filterLabels name the choices, in their order.
func sortLabels() []string {
	return []string{str.SortName, str.SortAdded, str.SortYear, str.SortRating}
}
func filterLabels() []string { return []string{str.FilterAll, str.FilterUnplayed, str.FilterFavorites} }

// sortKeys and filterKeys name the choices in the settings, in their order.
var (
	sortKeys   = []string{"name", "added", "year", "rating"}
	filterKeys = []string{"all", "unplayed", "favorites"}
)

// prefsFor returns how the library with the ID id is sorted and filtered:
// by name, A to Z, all of it, until the user chooses otherwise, which the
// settings keep.
func (a *app) prefsFor(id string) libPrefs {
	v, ok := a.settings.Libraries[id]
	if !ok {
		return libPrefs{}
	}
	return libPrefs{sort: max(slices.Index(sortKeys, v.Sort), 0), desc: v.Desc, filter: max(slices.Index(filterKeys, v.Filter), 0)}
}

// setPrefs sorts or filters a library anew, from its top.
func (a *app) setPrefs(id string, p libPrefs) {
	if a.prefsFor(id) == p {
		return
	}
	libs := maps.Clone(a.settings.Libraries)
	if libs == nil {
		libs = map[string]LibraryView{}
	}
	libs[id] = LibraryView{Sort: sortKeys[p.sort], Desc: p.desc, Filter: filterKeys[p.filter]}
	a.settings.Libraries = libs
	a.saveSettings()
	a.scrollPageTo(0)
	a.prefetch(a.router.Location())
}

// setSort sorts by field i, in the order that suits it: names A to Z, the
// rest newest or best first.
func (a *app) setSort(id string, i int) {
	p := a.prefsFor(id)
	p.sort, p.desc = i, i != sortName
	a.setPrefs(id, p)
}

// browsesFolders reports whether a library's page goes folder by folder,
// rather than listing all its items of a type.
func browsesFolders(kind string) bool { return libraryTypes(kind) == nil }

// libraryTypes are the item types a library lists, from all its folders,
// or nil for a library browsed folder by folder.
func libraryTypes(kind string) []string {
	switch kind {
	case emby.CollectionMovies:
		return []string{emby.TypeMovie}
	case emby.CollectionTVShows:
		return []string{emby.TypeSeries}
	case emby.CollectionBoxSets:
		return []string{emby.TypeBoxSet}
	case emby.CollectionMusicVideos:
		return []string{emby.TypeMusicVideo}
	}
	return nil
}

// libraryQuery is the query of a page of a library's items, or of one of
// its folders' when folderID is set.
func libraryQuery(lib library, folderID string, p libPrefs, start, limit int) emby.ItemQuery {
	q := emby.ItemQuery{ParentID: lib.ID, StartIndex: start, Limit: limit}
	if folderID != "" {
		q.ParentID = folderID
	} else if types := libraryTypes(lib.Kind); types != nil {
		q.IncludeItemTypes = types
		q.Recursive = true
	}
	switch p.sort {
	case sortAdded:
		q.SortBy = []string{emby.SortDateCreated, emby.SortName}
	case sortYear:
		q.SortBy = []string{emby.SortProductionYear, emby.SortPremiereDate, emby.SortName}
	case sortRating:
		q.SortBy = []string{emby.SortCommunityRating, emby.SortName}
	default:
		q.SortBy = []string{emby.SortName}
	}
	q.SortOrder = emby.Ascending
	if p.desc {
		q.SortOrder = emby.Descending
	}
	switch p.filter {
	case filterUnplayed:
		q.Filters = []string{emby.FilterIsUnplayed}
	case filterFavorites:
		q.Filters = []string{emby.FilterIsFavorite}
	}
	return q
}

// listState is a list of items loaded page by page: a library's or a
// folder's, sorted and filtered one way.
type listState struct {
	items   []emby.Item
	total   int
	started bool
	loading bool
	loaded  bool  // the first page arrived
	err     error // the first page failed
	moreErr error // a later page failed: no more is asked until ⌘R
	reload  int
	gen     gen
}

// listKey names the list of a folder of a library sorted and filtered so.
func listKey(lib library, folderID string, p libPrefs) string {
	return strings.Join([]string{lib.ID, lib.Kind, folderID, itoa(p.sort), itoa(int(b2f(p.desc))), itoa(p.filter)}, "|")
}

// list returns the list of a folder of a library, loading its first page
// when it has none, or again after ⌘R.
func (a *app) list(lib library, folderID string, p libPrefs) *listState {
	key := listKey(lib, folderID, p)
	ls := a.lists[key]
	if ls == nil {
		ls = &listState{}
		a.lists[key] = ls
	}
	if a.client != nil && !ls.loading && (!ls.started || ls.reload != a.reload) {
		ls.moreErr = nil
		a.loadList(ls, lib, folderID, p, 0)
	}
	return ls
}

// loadMore loads the next page of a list, if it has more.
func (a *app) loadMore(ls *listState, lib library, folderID string, p libPrefs) {
	if ls.loading || !ls.loaded || ls.moreErr != nil || len(ls.items) >= ls.total {
		return
	}
	a.loadList(ls, lib, folderID, p, len(ls.items))
}

// loadList loads the page of a list from start. A first page replaces what
// the list holds, once it comes; a later one adds to it. An answer that
// comes after another load of the list was asked for is dropped.
func (a *app) loadList(ls *listState, lib library, folderID string, p libPrefs, start int) {
	ls.started, ls.loading = true, true
	if start == 0 {
		ls.reload = a.reload
	}
	n := ls.gen.next()
	epoch := a.epoch
	q := libraryQuery(lib, folderID, p, start, libraryPageSize)
	load(a, func(ctx context.Context, cl *emby.Client) (*emby.ItemsResult, error) {
		return cl.Items(ctx, q)
	}, func(res *emby.ItemsResult, err error) {
		if a.epoch != epoch || !ls.gen.is(n) {
			return
		}
		ls.loading = false
		if err != nil {
			if start == 0 && !ls.loaded {
				ls.err = err
			} else {
				ls.moreErr = err
				a.toast = str.LoadMoreFailed
			}
			return
		}
		switch {
		case start == 0:
			ls.items = res.Items
		case start == len(ls.items):
			ls.items = append(ls.items, res.Items...)
		}
		ls.total = max(res.TotalRecordCount, len(ls.items))
		if len(res.Items) == 0 {
			ls.total = len(ls.items) // the server has no more, whatever it counted
		}
		ls.loaded, ls.err = true, nil
	})
}

// libraryPage is a library's page, or one of its folders' with folderID.
func (a *app) libraryPage(c *ui.Context, r *ui.Route, libID, folderID string) {
	lib, ok := a.libraryByID(libID)
	title := r.Query("name")
	if title == "" {
		title = lib.Name
	}
	switch {
	case !ok && !a.viewsLoaded && a.viewsErr == nil:
		a.scrollPage(c, r, pageSpec{Title: title}, func() { a.skeletonGrid(c, 150) })
		return
	case !ok && a.viewsErr != nil:
		a.scrollPage(c, r, pageSpec{Title: title}, func() { a.errorState(c, a.errText(a.viewsErr), a.loadViews) })
		return
	case !ok:
		a.scrollPage(c, r, pageSpec{Title: str.NotFound}, func() {
			a.emptyState(c, emptySpec{Icon: iconFolder, Title: str.NotFound, Message: str.NotFoundBody,
				Action: str.Home, OnAction: func() { a.navigate(pathHome) }})
		})
		return
	}
	p := a.prefsFor(lib.ID)
	ls := a.list(lib, folderID, p)
	eyebrow := str.Libraries
	if folderID != "" {
		eyebrow = lib.Name
	}
	spec := pageSpec{
		Title:   title,
		Eyebrow: eyebrow,
		Actions: []barAction{
			{Icon: iconSort, Label: str.SortBy, Menu: a.sortMenu(lib.ID)},
			{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh},
		},
	}
	landscape := folderID == "" && landscapeKind(lib.Kind)
	items := ls.items
	a.gridPage(c, r, spec, gridSpec{
		Count:     len(items),
		Landscape: landscape,
		MinWidth:  150,
		Key:       func(i int) any { return items[i].ID },
		Label:     func(i int) string { return items[i].Name },
		Item: func(i int, width float32) {
			a.itemCard(c, &items[i], cardOpts{libID: lib.ID, shown: width, landscape: landscape})
		},
		OnNearEnd:    func() { a.loadMore(ls, lib, folderID, p) },
		HeaderHeight: 44,
		Header:       func() { a.libraryHeader(c, lib.ID, ls) },
		Empty: func() {
			switch {
			case !ls.loaded && ls.err != nil:
				a.errorState(c, a.errText(ls.err), func() { a.loadList(ls, lib, folderID, p, 0) })
			case !ls.loaded:
				ui.Column(c).AlignSelf(ui.Stretch).Grow(1).Children(func() { a.skeletonGrid(c, 150) })
			default:
				a.emptyState(c, a.emptyLibrary(p, folderID != ""))
			}
		},
	})
}

// emptyLibrary says why a library shows nothing.
func (a *app) emptyLibrary(p libPrefs, folder bool) emptySpec {
	switch p.filter {
	case filterUnplayed:
		return emptySpec{Icon: iconCheckCircle, Title: str.EmptyUnplayed, Message: str.EmptyFilterBody}
	case filterFavorites:
		return emptySpec{Icon: iconHeart, Title: str.EmptyFavorites, Message: str.EmptyFilterBody}
	}
	if folder {
		return emptySpec{Icon: iconFolder, Title: str.EmptyFolder}
	}
	return emptySpec{Icon: iconInbox, Title: str.EmptyTitle}
}

// libraryHeader is the row under a library's title: its filters, how many
// items it has, and how it is sorted.
func (a *app) libraryHeader(c *ui.Context, libID string, ls *listState) {
	p := a.pal
	prefs := a.prefsFor(libID)
	ui.Row(c.Key("library-header")).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
		for i, label := range filterLabels() {
			a.filterChip(c, label, i == prefs.filter).OnClick(func() {
				np := a.prefsFor(libID)
				np.filter = i
				a.setPrefs(libID, np)
			})
		}
		ui.Spacer(c)
		if ls.loaded {
			ui.Text(c.Key("count"), itemCountText(ls.total)).FontSize(fsCaption).TextColor(p.text2).
				FontFeatures("tnum").SingleLine().Margin(0, 4, 0, 0)
		}
		a.sortButton(c, libID, prefs)
	})
}

// sortButton shows how a library is sorted, and opens the choices.
func (a *app) sortButton(c *ui.Context, libID string, prefs libPrefs) {
	p := a.pal
	label := sortLabels()[prefs.sort]
	b := ui.ButtonBase(c.Key("sort")).Height(28).Padding(0, 10, 0, 12).Gap(5).Radius(14).AlignItems(ui.Center).
		Label(str.SortBy + "：" + label).Shrink(0)
	bg := p.fill
	switch {
	case b.Pressed():
		bg = p.fillHover.MixOklab(black, 0.06)
	case b.Hovered():
		bg = p.fillHover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine()
		icon := iconArrowUp
		if prefs.desc {
			icon = iconArrowDown
		}
		ui.Icon(c, icon).Size(13, 13).TextColor(p.text2)
		ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.text3)
	})
	b.PopoverMenuButton(a.sortMenu(libID), ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
}

// sortMenu is the menu of a library's sort fields and orders.
func (a *app) sortMenu(libID string) func(m *ui.Menu) {
	return func(m *ui.Menu) {
		prefs := a.prefsFor(libID)
		for i, label := range sortLabels() {
			if m.Item(label).Radio(prefs.sort == i).Chosen() {
				a.setSort(libID, i)
			}
		}
		m.Separator()
		if m.Item(str.Ascending).Radio(!prefs.desc).Chosen() {
			prefs.desc = false
			a.setPrefs(libID, prefs)
		}
		if m.Item(str.Descending).Radio(prefs.desc).Chosen() {
			prefs.desc = true
			a.setPrefs(libID, prefs)
		}
	}
}
