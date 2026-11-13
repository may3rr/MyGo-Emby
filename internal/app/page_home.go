package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Home: a banner of what was added lately, turning by itself, then what
// the user is watching, what comes next, and each library's latest, as
// shelves. A shelf with nothing to show is left out.

const (
	shelfLimit   = 16              // items asked for each shelf
	heroCount    = 6               // items the banner turns through
	heroInterval = 8 * time.Second // each shows this long
	homeWorkers  = 6               // libraries asked at once
)

// homeState is what Home loaded, for the account in use.
type homeState struct {
	started bool // a load was asked for
	loading bool
	loaded  bool // data holds an answer
	reload  int  // a.reload as it was asked for
	err     error
	gen     gen
	data    homeData

	heroIndex int       // the banner's item shown
	heroSince time.Time // since when, zero until the banner shows
}

// homeData is what Home shows.
type homeData struct {
	resume []emby.Item
	nextUp []emby.Item
	latest []latestShelf
	hero   []heroItem
}

// latestShelf is a library's latest items.
type latestShelf struct {
	lib   library
	items []emby.Item
}

// heroItem is an item the banner shows, with the library it was added to.
type heroItem struct {
	item emby.Item
	lib  library
}

// latestKinds says which libraries Home shows the latest of: those of
// films and shows, and folders that may hold them; not music, books,
// photos, playlists or live TV, which this app does not play.
func latestKinds(kind string) bool {
	switch kind {
	case emby.CollectionMusic, emby.CollectionBooks, emby.CollectionPhotos, emby.CollectionPlaylists,
		emby.CollectionLiveTV, "audiobooks", "podcasts":
		return false
	}
	return true
}

// landscapeKind reports whether a library's items show as 16:9 cards.
func landscapeKind(kind string) bool {
	return kind == emby.CollectionMusicVideos || kind == emby.CollectionHomeVideos
}

// fetchHome asks the server for what Home shows. Only the libraries are
// needed: a shelf that fails is left out, unless all of them fail.
func fetchHome(ctx context.Context, cl *emby.Client) (homeData, []emby.Item, error) {
	views, err := cl.Views(ctx)
	if err != nil {
		return homeData{}, nil, err
	}
	var (
		d        homeData
		mu       sync.Mutex
		firstErr error
		failed   int
		asked    int
		wg       sync.WaitGroup
	)
	note := func(what string, err error) {
		mu.Lock()
		defer mu.Unlock()
		asked++
		if err == nil {
			return
		}
		failed++
		if firstErr == nil || errors.Is(err, emby.ErrUnauthorized) {
			firstErr = err
		}
		if ctx.Err() == nil {
			log.Printf("home: %s: %v", what, err)
		}
	}
	wg.Go(func() {
		items, err := cl.ResumeItems(ctx, shelfLimit)
		note("continue watching", err)
		mu.Lock()
		d.resume = items
		mu.Unlock()
	})
	wg.Go(func() {
		items, err := cl.NextUp(ctx, "", shelfLimit)
		note("next up", err)
		mu.Lock()
		d.nextUp = items
		mu.Unlock()
	})
	var libs []library
	for _, v := range views {
		if latestKinds(v.CollectionType) {
			libs = append(libs, library{ID: v.ID, Name: v.Name, Kind: v.CollectionType})
		}
	}
	latest := make([]latestShelf, len(libs))
	sem := make(chan struct{}, homeWorkers)
	for i, lib := range libs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			items, err := cl.Latest(ctx, lib.ID, shelfLimit)
			note("latest of "+lib.Name, err)
			latest[i] = latestShelf{lib: lib, items: items}
		})
	}
	wg.Wait()
	if errors.Is(firstErr, emby.ErrUnauthorized) || (asked > 0 && failed == asked) {
		return homeData{}, nil, firstErr
	}
	// Emby lists an episode under way as next too: it shows once, under
	// Continue Watching.
	resuming := map[string]bool{}
	for _, it := range d.resume {
		resuming[it.ID] = true
	}
	d.nextUp = slices.DeleteFunc(d.nextUp, func(it emby.Item) bool { return resuming[it.ID] })
	d.latest = slices.DeleteFunc(latest, func(s latestShelf) bool { return len(s.items) == 0 })
	d.hero = pickHero(d.latest, heroCount)
	return d, views, nil
}

// pickHero chooses the banner's items: the latest of each library in turn,
// those with a backdrop to show.
func pickHero(shelves []latestShelf, n int) []heroItem {
	var out []heroItem
	seen := map[string]bool{}
	for depth := 0; len(out) < n; depth++ {
		more := false
		for _, s := range shelves {
			if depth >= len(s.items) {
				continue
			}
			more = true
			it := s.items[depth]
			if _, ok := it.Image(emby.RoleBackdrop); !ok || seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			out = append(out, heroItem{item: it, lib: s.lib})
			if len(out) == n {
				break
			}
		}
		if !more {
			break
		}
	}
	return out
}

// loadHome asks for what Home shows, again with ⌘R.
func (a *app) loadHome() {
	h := a.home
	n := h.gen.next()
	h.started, h.loading, h.reload = true, true, a.reload
	type result struct {
		data  homeData
		views []emby.Item
	}
	load(a, func(ctx context.Context, cl *emby.Client) (result, error) {
		d, views, err := fetchHome(ctx, cl)
		return result{d, views}, err
	}, func(r result, err error) {
		if a.home != h || !h.gen.is(n) {
			return // superseded
		}
		h.loading = false
		if err != nil {
			if h.loaded {
				a.toast = a.errText(err) // what shows stays
			}
			h.err = err
			return
		}
		h.err, h.loaded, h.data = nil, true, r.data
		if h.heroIndex >= len(r.data.hero) {
			h.heroIndex = 0
		}
		a.setLibraries(r.views)
	})
}

// heroHeight is the banner's height in a content area w wide.
func heroHeight(w float32) float32 {
	return min(max(w*0.46, 340), 560)
}

// ensureHome loads Home unless it is loaded, or loading, since the last ⌘R.
func (a *app) ensureHome() {
	h := a.home
	if a.client != nil && !h.loading && (!h.started || h.reload != a.reload) {
		a.loadHome()
	}
}

func (a *app) homePage(c *ui.Context, r *ui.Route) {
	a.ensureHome()
	h := a.home
	w, _ := c.Size()
	heroH := heroHeight(a.contentWidth(w))
	waiting := !h.loaded && h.err == nil
	spec := pageSpec{
		Title:      str.Home,
		Hero:       waiting || (h.loaded && len(h.data.hero) > 0),
		HeroHeight: heroH,
		Actions:    []barAction{{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh}},
	}
	a.scrollPage(c, r, spec, func() {
		switch {
		case waiting:
			a.homeSkeleton(c, heroH)
		case !h.loaded:
			a.errorState(c, a.errText(h.err), a.loadHome)
		default:
			a.homeContent(c, heroH)
		}
	})
}

// homeSkeleton stands for Home while it loads.
func (a *app) homeSkeleton(c *ui.Context, heroH float32) {
	s := a.shimmer(c)
	ui.Column(c.Key("home-skeleton")).Gap(shelfGap).Label(str.Loading).Busy(true).Children(func() {
		s.bone(c, 0).Height(heroH).Margin(0, -gutter, 0, -gutter)
		a.skeletonShelf(c, "skeleton-landscape", true)
		a.skeletonShelf(c, "skeleton-poster", false)
	})
}

// homeContent is Home once loaded.
func (a *app) homeContent(c *ui.Context, heroH float32) {
	d := &a.home.data
	empty := len(d.hero) == 0 && len(d.resume) == 0 && len(d.nextUp) == 0 && len(d.latest) == 0
	ui.Column(c.Key("home")).Gap(shelfGap).Children(func() {
		if len(d.hero) > 0 {
			a.homeHero(c, d.hero, heroH)
		}
		if empty {
			msg := str.HomeEmptyBody
			if len(a.libraries) == 0 {
				msg = str.NoLibraries
			}
			a.emptyState(c, emptySpec{Icon: iconInbox, Title: str.HomeEmpty, Message: msg})
			return
		}
		a.itemShelf(c, "resume", str.ContinueWatching, d.resume, "", true, true, nil)
		a.itemShelf(c, "next-up", str.NextUp, d.nextUp, "", true, false, nil)
		for _, s := range d.latest {
			lib := s.lib
			a.itemShelf(c, "latest-"+lib.ID, fmt.Sprintf(str.LatestFmt, lib.Name), s.items, lib.ID,
				landscapeKind(lib.Kind), false, func() { a.navigate(pathLibrary(lib.ID)) })
		}
	})
}

// itemShelf is a shelf of items, left out when there are none.
func (a *app) itemShelf(c *ui.Context, key, title string, items []emby.Item, libID string, landscape, resume bool, seeAll func()) {
	if len(items) == 0 {
		return
	}
	width, artH := float32(posterWidth), float32(posterWidth*3/2)
	if landscape {
		width, artH = landscapeWidth, landscapeWidth*9/16
	}
	a.shelf(c, shelfSpec{
		Key: key, Title: title, OnSeeAll: seeAll, Count: len(items), ItemWidth: width, ArtHeight: artH,
		Item: func(i int) {
			a.itemCard(c, &items[i], cardOpts{libID: libID, width: width, landscape: landscape, resume: resume})
		},
	})
}

// homeHero is the banner: an item's backdrop across the content, its name
// (or its logo), its facts, the start of its overview, and buttons to play
// it or open it. It turns to the next item by itself, unless the pointer is
// on it or the desktop asks for less motion; its buttons at the bottom
// right turn it by hand.
func (a *app) homeHero(c *ui.Context, items []heroItem, height float32) {
	p := a.pal
	h := a.home
	n := len(items)
	idx := h.heroIndex % n
	box := ui.Box(c.Key("hero")).Height(height).Margin(0, -gutter, 0, -gutter).Background(p.skeleton).
		Role(ui.RoleGroup).Label(str.RecentlyAdded)
	hovered := box.Hovered() || box.FocusWithin()
	still := c.Preferences().ReduceMotion
	switch {
	case h.heroSince.IsZero() || hovered:
		h.heroSince = c.Now() // the time starts again once the pointer leaves
	case n > 1 && !still:
		if left := heroInterval - c.Now().Sub(h.heroSince); left > 0 {
			c.After(left)
		} else {
			idx = (idx + 1) % n
			h.heroIndex, h.heroSince = idx, c.Now()
			c.After(heroInterval)
		}
	}
	w, _ := c.Size()
	contentW := a.contentWidth(w)
	box.Children(func() {
		hi := items[idx]
		it := &hi.item
		slide := ui.Box(c.Key(it.ID)).Absolute().Top(0).Left(0).Right(0).Bottom(0)
		slide.Transition(ui.ElementTransition{Duration: fadeIn.Duration, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
		slide.Children(func() {
			a.artwork(c, a.itemImage(it, emby.RoleBackdrop, contentW), iconFilm, 0)
			// Scrims: along the foot, and from the start, where the text is.
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
					{Color: ui.Transparent, At: 0.35}, {Color: p.scrim.Alpha(0.75), At: 1},
				}})
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
					{Color: p.scrim.Alpha(0.7), At: 0}, {Color: p.scrim.Alpha(0.25), At: 0.5}, {Color: ui.Transparent, At: 0.8},
				}})
			a.heroText(c, hi, contentW)
		})
		if n > 1 {
			a.heroPager(c, n, idx)
		}
	})
}

// heroText is what the banner says of its item, at its foot.
func (a *app) heroText(c *ui.Context, hi heroItem, contentW float32) {
	p := a.pal
	it := &hi.item
	ui.Column(c.Key("text")).Absolute().Left(gutter + 8).Bottom(36).MaxWidth(min(560, contentW*0.55)).Gap(8).
		Children(func() {
			ui.Text(c, fmt.Sprintf(str.LatestFmt, hi.lib.Name)).FontSize(fsSmall).FontWeight(wBold).
				TextColor(p.onImage2).LetterSpacing(0.4).SingleLine()
			a.heroTitle(c, it)
			parts := []string{it.YearText()}
			if it.Type != emby.TypeSeries && it.RunTimeTicks > 0 {
				parts = append(parts, runtimeText(it.Runtime()))
			}
			if len(it.Genres) > 0 {
				parts = append(parts, joinNonEmpty(" / ", it.Genres[:min(len(it.Genres), 2)]...))
			}
			a.metaRow(c, metaSpec{Parts: parts, Rating: it.CommunityRating, Badge: it.OfficialRating, OnImage: true})
			if it.Overview != "" {
				ui.Text(c, it.Overview).FontSize(fsBody).TextColor(p.onImage2).LineHeight(1.45).MaxLines(2).
					TextShadow(0, 1, 4, ui.RGBA(0, 0, 0, 0.25))
			}
			item := *it
			ui.Row(c).Gap(10).Margin(8, 0, 0, 0).Children(func() {
				a.pillButton(c, iconPlay, str.Play, pillOnImage).OnClick(func() { a.play(&item, playOptions{}) })
				a.pillButton(c, iconInfo, str.MoreInfo, pillGlass).OnClick(func() { a.openItem(&item, hi.lib.ID) })
			})
		})
}

// heroTitle is the banner item's logo, where it has one that loaded, or its
// name. While the logo loads, its room stays empty, so the name does not
// show for a moment before it.
func (a *app) heroTitle(c *ui.Context, it *emby.Item) {
	p := a.pal
	const logoW, logoH = 380, 110
	if logo := a.itemImage(it, emby.RoleLogo, logoW); logo != "" {
		bmp, _, failed := a.img.Lookup(logo)
		if !failed {
			box := ui.Box(c.Key("logo")).Width(logoW).Height(logoH).Margin(0, 0, 4, 0).Label(it.Name)
			in := fadeInOn(box, bmp != nil)
			box.Children(func() {
				if bmp != nil {
					ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain).
						ObjectPosition(0, 1).Opacity(in)
				}
			})
			return
		}
	}
	ui.Text(c.Key("title"), it.Name).FontSize(34).FontWeight(wBold).TextColor(p.onImage).LetterSpacing(-0.6).
		MaxLines(2).Balance().TextShadow(0, 1, 8, ui.RGBA(0, 0, 0, 0.3))
}

// heroPager turns the banner by hand: back, a dot for each item, forward.
func (a *app) heroPager(c *ui.Context, n, idx int) {
	h := a.home
	set := func(i int) {
		h.heroIndex = (i + n) % n
		h.heroSince = time.Time{}
	}
	ui.Row(c.Key("pager")).Absolute().Right(gutter).Bottom(36).Height(30).PaddingX(3).Gap(2).Radius(15).
		AlignItems(ui.Center).Material(glass.Glass{Style: glass.Clear, Tint: ui.RGBA(0, 0, 0, 0.25)}).
		Children(func() {
			a.heroArrow(c, "prev", iconBack, str.Previous, func() { set(idx - 1) })
			for i := range n {
				dot := ui.ButtonBase(c.Key(i)).Height(24).PaddingX(3).Center().TabStop(false).
					Label(fmt.Sprintf(str.SlideFmt, i+1, n)).AccessSelected(i == idx)
				dw := float32(6)
				alpha := float32(0.45)
				if i == idx {
					dw, alpha = 18, 0.95
				} else if dot.Hovered() {
					alpha = 0.7
				}
				dot.Children(func() {
					ui.Box(c).Width(dot.AnimateTo("w", dw, medium)).Height(6).Radius(3).
						Background(ui.RGBA(255, 255, 255, alpha))
				})
				dot.OnClick(func() { set(i) })
			}
			a.heroArrow(c, "next", iconForward, str.Next, func() { set(idx + 1) })
		})
}

// heroArrow is a small round button of the banner's pager.
func (a *app) heroArrow(c *ui.Context, key string, icon *ui.SVG, label string, do func()) {
	p := a.pal
	b := ui.ButtonBase(c.Key(key)).Size(24, 24).Radius(12).Center().Label(label).Tooltip(label)
	bg := ui.Transparent
	switch {
	case b.Pressed():
		bg = ui.RGBA(255, 255, 255, 0.22)
	case b.Hovered():
		bg = ui.RGBA(255, 255, 255, 0.14)
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() { ui.Icon(c, icon).Size(14, 14).TextColor(p.onImage) })
	b.OnClick(do)
}
