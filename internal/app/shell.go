package app

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// The window's chrome: the sidebar over the window's material, and the
// content area, where the page shown scrolls under a floating bar of glass
// holding back and forward, the page's title once its large title has
// scrolled away, and the page's own actions.

// view builds the window, every frame.
func (a *app) view(c *ui.Context) {
	a.applyTheme(c)
	p := a.pal
	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	if a.playerShown(c) {
		return
	}
	if a.screen == screenLogin {
		a.loginScreen(c)
		a.showToast(c)
		return
	}
	c.OnWindowShortcut(sidebarShortcutMods(), ui.KeyS, a.toggleSidebar)
	c.OnWindowShortcut(ui.Cmd, ui.KeyF, a.focusSearch)
	collapsed := a.settings.SidebarCollapsed
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if !collapsed {
			side := ui.Column(c.Key("sidebar")).Width(sidebarWidth).Shrink(0)
			if !c.Vibrancy() || a.opaqueSidebar {
				side.Background(p.sidebar)
			}
			side.Children(func() { a.sidebar(c) })
		}
		main := ui.Box(c.Key("main")).Grow(1).MinWidth(0).Background(p.bg)
		if !collapsed {
			main.BorderWidth(0, 0, 0, 1).BorderColor(p.hairline)
		}
		main.Children(func() {
			// Each page tells the bar what it needs as it builds.
			a.bar = barState{}
			if a.conn == connOffline {
				a.offlineState(c)
			} else {
				a.router.View(c, func(r *ui.Route) { a.pages(c, r) }).Fill()
			}
			a.topBar(c)
		})
	})
	a.serverSheet(c)
	a.showToast(c)
}

// sidebarShortcutMods is the sidebar toggle's shortcut: Control-Command-S
// on macOS, and Control-Option-S elsewhere.
func sidebarShortcutMods() ui.Modifiers {
	if runtime.GOOS == "darwin" {
		return ui.Super | ui.Ctrl
	}
	return ui.Ctrl | ui.Alt
}

// sidebarAccelerator writes sidebarShortcutMods for the menu bar.
func sidebarAccelerator() string {
	if runtime.GOOS == "darwin" {
		return "Super+Ctrl+S"
	}
	return "Ctrl+Alt+S"
}

// setSidebarCollapsed records and applies the sidebar's preference.
func (a *app) setSidebarCollapsed(collapsed bool) {
	if a.settings.SidebarCollapsed == collapsed {
		return
	}
	if collapsed {
		a.rememberPageScroll()
	} else {
		a.restorePageScroll()
	}
	a.settings.SidebarCollapsed = collapsed
	a.saveSettings()
	a.redraw()
}

func (a *app) toggleSidebar() {
	a.setSidebarCollapsed(!a.settings.SidebarCollapsed)
}

func (a *app) rememberPageScroll() {
	if a.router == nil {
		return
	}
	mem := a.pageMems[a.router.Path()]
	if mem == nil {
		return
	}
	mem.beforeCollapse = true
	mem.collapsedFromScroll = mem.scroll.Y
	mem.collapsedFromGrid = mem.grid.scroll.Y
}

func (a *app) restorePageScroll() {
	for _, mem := range a.pageMems {
		if !mem.beforeCollapse {
			continue
		}
		mem.scroll.Y = mem.collapsedFromScroll
		mem.grid.scroll.Y = mem.collapsedFromGrid
		mem.beforeCollapse = false
	}
}

// contentWidth is the width the pages have, without the sidebar when it
// shows.
func (a *app) contentWidth(w float32) float32 {
	if a.settings.SidebarCollapsed {
		return w
	}
	return w - sidebarWidth
}

// showToast shows the message a.toast holds, once.
func (a *app) showToast(c *ui.Context) {
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
}

// offlineState takes the place of the pages while the server does not
// answer: the app stays, the user's way back being to try again or to
// choose another server.
func (a *app) offlineState(c *ui.Context) {
	p := a.pal
	name := ""
	if a.client != nil {
		name = serverTitle(a.client.Session())
	}
	ui.Column(c.Key("offline")).Fill().Center().Padding(barH(c), gutter, 0, gutter).Children(func() {
		ui.Column(c).AlignItems(ui.Center).MaxWidth(400).Gap(8).Children(func() {
			ui.Box(c).Size(64, 64).Radius(32).Center().Background(p.fill).Margin(0, 0, 8, 0).Children(func() {
				ui.Icon(c, iconWifiOff).Size(28, 28).TextColor(p.text2)
			})
			ui.Text(c, str.OfflineTitle).FontSize(17).FontWeight(wSemibold).TextColor(p.text).TextAlign(ui.Center)
			ui.Text(c, fmt.Sprintf(str.OfflineFmt, name)).FontSize(fsBody).TextColor(p.text2).
				TextAlign(ui.Center).LineHeight(1.45).Balance()
			ui.Row(c).Gap(10).Margin(12, 0, 0, 0).Children(func() {
				a.pillButton(c, iconRefresh, str.Retry, pillPrimary).OnClick(a.reconnect)
				a.pillButton(c, iconServer, str.SwitchServerMenu, pillPlain).OnClick(a.openServers)
			})
		})
	})
	a.setBar(pageSpec{}, 0)
}

// The sidebar.

func (a *app) sidebar(c *ui.Context) {
	p := a.pal
	top := float32(18)
	if tb := c.TitleBar(); tb.Left > 0 {
		top = tb.Bottom + 16 // under the traffic lights
	}
	// Where nothing in it takes the press, the sidebar moves the window, as
	// the title bar it reaches up into would.
	ui.Column(c).Fill().Padding(top, 12, 10, 12).DragWindow().Children(func() {
		a.searchField(c)
		nav := ui.Scroll(c.Key("nav")).Grow(1).Margin(10, -12, 0, -12).Padding(0, 12, 8, 12)
		nav.ScrollbarAxes(false, false)
		nav.Children(func() {
			ui.Column(c).Gap(2).FocusGroup(ui.Vertical).Children(func() {
				a.navRow(c, sectionHome, iconHome, str.Home, pathHome)
				a.navRow(c, sectionFavorites, iconHeart, str.Favorites, pathFavorites)
				ui.Text(c, str.Libraries).FontSize(fsSmall).FontWeight(wSemibold).TextColor(p.text3).
					Padding(16, 8, 5, 8).SingleLine()
				switch {
				case len(a.libraries) > 0:
					for _, lib := range a.libraries {
						a.navRow(c, sectionLibPrefix+lib.ID, libraryIcon(lib.Kind), lib.Name, pathLibrary(lib.ID))
					}
				case a.viewsErr != nil && a.conn != connOffline:
					a.librariesFailed(c) // offline, the content says so
				case !a.viewsLoaded:
					a.navSkeleton(c)
				}
				if a.dev {
					ui.Text(c, str.Developer).FontSize(fsSmall).FontWeight(wSemibold).TextColor(p.text3).
						Padding(16, 8, 5, 8).SingleLine()
					a.navRow(c, sectionGallery, iconGrid, str.Gallery, pathGallery)
				}
			})
		})
		a.accountRow(c)
	})
}

// navSkeleton stands for the libraries while they load.
func (a *app) navSkeleton(c *ui.Context) {
	s := a.shimmer(c)
	ui.Column(c.Key("nav-skeleton")).Gap(2).Label(str.Loading).Busy(true).Children(func() {
		for i, w := range []float32{58, 72, 46} {
			ui.Row(c.Key(i)).Height(30).PaddingX(8).Gap(9).AlignItems(ui.Center).Children(func() {
				s.bone(c, 5).Size(17, 17)
				s.bone(c, 4).Height(10).WidthPercent(w)
			})
		}
	})
}

// librariesFailed says the libraries did not load, with a way to try again.
func (a *app) librariesFailed(c *ui.Context) {
	p := a.pal
	ui.Row(c.Key("libraries-failed")).Height(30).PaddingX(8).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconAlert).Size(14, 14).TextColor(p.text3).Shrink(0)
		ui.Text(c, str.LibrariesFailed).FontSize(fsCaption).TextColor(p.text2).SingleLine().Grow(1).Shrink(1).MinWidth(0)
		a.iconButton(c, iconRefresh, str.Retry, 24).OnClick(a.loadViews)
	})
}

// navRow is an entry of the sidebar: a click opens its page, or scrolls it
// to its top when it shows already.
func (a *app) navRow(c *ui.Context, id string, icon *ui.SVG, label, path string) {
	p := a.pal
	selected := a.section() == id
	b := ui.ButtonBase(c.Key(id)).Height(30).PaddingX(8).Gap(9).Radius(radiusButton).
		AlignItems(ui.Center).Justify(ui.Start).Label(label).FocusRingOffset(-1)
	bg := ui.Transparent
	switch {
	case selected:
		bg = p.selected
	case b.Pressed():
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	iconColor := p.text2
	if selected {
		b.Current(ui.CurrentPage)
		iconColor = p.accent
		if !c.WindowActive() {
			iconColor = p.text2
		}
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(17, 17).TextColor(iconColor).Shrink(0)
		t := ui.Text(c, label).FontSize(fsBody).TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
		if selected {
			t.FontWeight(wMedium)
		}
	})
	b.OnClick(func() { a.navigate(path) })
}

// searchField is the sidebar's search: typing shows the search page, which
// follows the query as it changes; clearing it goes back.
func (a *app) searchField(c *ui.Context) {
	p := a.pal
	if a.section() == sectionSearch && !a.searchBox.FocusWithin(c) {
		a.search = a.router.Query("q")
	}
	field := ui.Row(c.Key("search")).Height(30).PaddingX(8).Gap(6).Radius(radiusButton).
		AlignItems(ui.Center).Background(p.fill).Cursor(ui.CursorText).Role(ui.RoleSearch)
	if a.searchBox.FocusWithin(c) {
		field.Shadow(0, 0, 0, 3, p.focusRing)
	}
	field.Transition(ui.ElementTransition{Shadows: true, Duration: quick.Duration})
	field.Children(func() {
		ui.Icon(c, iconSearch).Size(14, 14).TextColor(p.text2).Shrink(0)
		in := ui.TextInputBase(c.Key("query"), &a.search).Bind(&a.searchBox).Grow(1).MinWidth(0).
			Placeholder(str.Search).Label(str.Search).Search()
		in.OnTextKey(func(k ui.TextKey) bool { return k.Mods == 0 && a.searchKey(k.Key) })
		if in.Changed() {
			a.searchChanged()
		}
		if a.search != "" {
			clear := ui.ButtonBase(c.Key("clear")).Size(16, 16).Radius(8).Center().Background(p.text3).
				Label("清除").TabStop(false).Shrink(0)
			clear.Children(func() { ui.Icon(c, iconX).Size(10, 10).TextColor(p.bg) })
			if clear.Clicked() || in.Shortcut(0, ui.KeyEscape) {
				a.search = ""
				a.searchChanged()
			}
		}
	})
}

// searchChanged shows the search page for the field's query: pushed as the
// user starts typing, then replaced as they type on, so that Back leaves
// the search at once; emptied, it goes back to where the search began.
func (a *app) searchChanged() {
	q := strings.TrimSpace(a.search)
	onSearch := a.router.Path() == "/search"
	switch {
	case onSearch && q == "":
		if a.router.CanGoBack() {
			a.router.Back()
		} else {
			a.router.Replace(pathHome)
		}
	case onSearch:
		a.router.Replace(pathSearch(q))
	case q != "":
		a.router.Push(pathSearch(q))
	}
}

// accountRow is who is signed in, at the foot of the sidebar: it opens the
// account's menu, which the window draws, above it.
func (a *app) accountRow(c *ui.Context) {
	p := a.pal
	var sess emby.Session
	if a.client != nil {
		sess = a.client.Session()
	}
	b := ui.ButtonBase(c.Key("account")).Height(48).PaddingX(8).Gap(10).Radius(10).
		AlignItems(ui.Center).Justify(ui.Start).Label(str.AccountMenu).Margin(6, 0, 0, 0)
	bg := ui.Transparent
	switch {
	case b.Pressed() || a.accountOpen:
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		ui.Avatar(c, sess.UserName, a.img.Get(a.avatarURL(sess, 32))).FontSize(16).Shrink(0)
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
			ui.Text(c, sess.UserName).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine()
			ui.Text(c, serverTitle(sess)).FontSize(fsSmall).TextColor(p.text2).SingleLine()
		})
		ui.Icon(c, iconChevronUpDown).Size(13, 13).TextColor(p.text3).Shrink(0)
	})
	b.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(str.Settings).Shortcut(ui.Cmd, ui.KeyComma).Chosen() {
			a.openSettings()
		}
		m.Separator()
		if m.Item(str.SwitchServerMenu).Chosen() {
			a.switchServer()
		}
		if m.Item(str.SignOut).Chosen() {
			a.signOut()
		}
	}, ui.PopoverMenuButtonOptions{At: ui.AnchorTopStart, Self: ui.AnchorBottomStart, Open: &a.accountOpen})
}

// The floating bar.

// barState is what the page shown asks of the bar, set as it builds.
type barState struct {
	title   string      // the page's title, shown once titleIn
	titleIn bool        // the large title has scrolled under the bar
	under   bool        // content has scrolled under the bar: its edge shows
	actions []barAction // the page's buttons, at the bar's end
}

// barAction is a button a page puts at the end of the bar: an icon, named
// by Label for its tooltip and screen readers, that runs Do or opens Menu.
type barAction struct {
	Icon     *ui.SVG
	Label    string
	Do       func()
	Menu     func(m *ui.Menu)
	On       bool // drawn as on, as a toggle that is
	Disabled bool
}

// barH is the height of the bar along the top of the content.
func barH(c *ui.Context) float32 {
	return max(barHeight, c.TitleBar().Height)
}

func (a *app) topBar(c *ui.Context) {
	p := a.pal
	b := a.bar
	h := barH(c)
	left := float32(16)
	if a.settings.SidebarCollapsed {
		left = max(left, c.TitleBar().Left+8)
	}
	// The edge fades in under the bar once content scrolls under it,
	// frosting it as macOS 26's hard scroll edge does, so that the bar's
	// title and buttons read over anything; it drags the window, as a
	// title bar would.
	edge := ui.Box(c.Key("bar-edge")).Absolute().Top(0).Left(0).Right(0).Height(h).
		DragWindow().Material(glass.ScrollEdge{Hard: true, Background: p.bg})
	edge.Opacity(edge.AnimateTo("shown", b2f(b.under), medium))
	ui.Row(c.Key("bar")).Absolute().Top(0).Left(0).Right(0).Height(h).Padding(0, 16, 0, left).Gap(12).
		AlignItems(ui.Center).DragWindow().Children(func() {
		a.sidebarToggleButton(c)
		ui.Row(c.Key("history")).Height(32).PaddingX(2).Radius(16).AlignItems(ui.Center).
			Material(glass.Glass{}).Shrink(0).Children(func() {
			a.barButton(c, barAction{Icon: iconBack, Label: str.Back, Do: a.back, Disabled: !a.router.CanGoBack()})
			a.barButton(c, barAction{Icon: iconForward, Label: str.Forward, Do: a.forward, Disabled: !a.router.CanGoForward()})
		})
		title := ui.Text(c.Key("bar-title"), b.title).FontSize(fsHeadline).FontWeight(wSemibold).
			TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
		in := title.AnimateTo("in", b2f(b.titleIn && b.title != ""), medium)
		title.Opacity(in).Translate(0, (1-in)*6)
		ui.Spacer(c)
		if len(b.actions) > 0 {
			ui.Row(c.Key("actions")).Height(32).PaddingX(2).Radius(16).AlignItems(ui.Center).
				Material(glass.Glass{}).Shrink(0).Children(func() {
				for _, act := range b.actions {
					a.barButton(c, act)
				}
			})
		}
	})
}

// sidebarToggleButton is available with the sidebar both shown and
// hidden. The hidden sidebar lets the title bar move clear of the window
// controls in topBar.
func (a *app) sidebarToggleButton(c *ui.Context) ui.Element {
	p := a.pal
	label := str.CollapseSidebar
	if a.settings.SidebarCollapsed {
		label = str.ExpandSidebar
	}
	b := ui.ButtonBase(c.Key("sidebar-toggle")).Size(36, 28).Radius(14).Center().
		Label(label).Tooltip(label).KeyShortcuts(sidebarShortcutMods(), ui.KeyS).
		FocusRingOffset(-1).Shrink(0)
	bg := ui.Transparent
	switch {
	case b.Pressed():
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() { ui.Icon(c, iconSidebar).Size(17, 17).TextColor(p.text) })
	b.OnClick(a.toggleSidebar)
	return b
}

// barButton is a button on the bar's glass.
func (a *app) barButton(c *ui.Context, act barAction) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(act.Label)).Size(36, 28).Radius(14).Center().Label(act.Label).
		Tooltip(act.Label).Disabled(act.Disabled).DimDisabled(false).FocusRingOffset(-1)
	color := p.text
	bg := ui.Transparent
	switch {
	case act.Disabled:
		color = p.text3
	case act.On:
		color = p.accent
	}
	if !act.Disabled {
		switch {
		case b.Pressed():
			bg = p.press
		case b.Hovered():
			bg = p.hover
		}
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() { ui.Icon(c, act.Icon).Size(17, 17).TextColor(color) })
	if act.Menu != nil {
		b.Menu(act.Menu)
	} else if act.Do != nil {
		b.OnClick(act.Do)
	}
	return b
}

// Pages.

// pageSpec is what a page tells its scaffold.
type pageSpec struct {
	// Title is the page's large title, and the bar's once it has scrolled
	// away; screen readers hear it as the page shows.
	Title string
	// Eyebrow is a small line above the large title, as a library's kind.
	Eyebrow string
	// Actions are the page's buttons, at the end of the bar.
	Actions []barAction
	// Hero starts the content at the top of the window, under the bar,
	// for a page that opens with a picture, as an item's; it shows no
	// large title, and the bar shows the title once the content scrolls.
	Hero bool
	// HeroHeight is how far down the bar's title waits on a Hero page.
	HeroHeight float32
}

const (
	titleTop    = 6  // from the bar to the large title
	titleHeight = 36 // the large title's line
	eyebrowH    = 18 // the eyebrow's line
	titleBelow  = 18 // from the large title to the content
	pageBottom  = 56 // room after the content
)

// titleBlock is the height the large title takes, with its eyebrow.
func titleBlock(p pageSpec) float32 {
	if p.Hero || p.Title == "" {
		return 0
	}
	h := float32(titleHeight + titleBelow)
	if p.Eyebrow != "" {
		h += eyebrowH
	}
	return h
}

// largeTitle is a page's title at the top of its content.
func (a *app) largeTitle(c *ui.Context, p pageSpec) {
	pal := a.pal
	ui.Column(c.Key("large-title")).Margin(0, 0, titleBelow, 0).Children(func() {
		if p.Eyebrow != "" {
			ui.Text(c, p.Eyebrow).FontSize(fsCaption).FontWeight(wSemibold).TextColor(pal.text2).
				Height(eyebrowH).SingleLine()
		}
		pageTitle(c, p.Title).TextColor(pal.text)
	})
}

// pageTitle is the 28-point title of a page, for pages that lay out their
// own header.
func pageTitle(c *ui.Context, title string) ui.Element {
	return ui.Text(c, title).FontSize(fsLargeTitle).FontWeight(wBold).LetterSpacing(-0.4).
		Height(titleHeight).SingleLine()
}

// setBar tells the bar what the page shows, from how far it is scrolled.
func (a *app) setBar(p pageSpec, scrolled float32) {
	a.bar.title = p.Title
	a.bar.actions = p.Actions
	a.bar.under = scrolled > 1
	if p.Hero {
		a.bar.titleIn = scrolled > max(p.HeroHeight-barHeight, 0)
		a.bar.under = scrolled > max(p.HeroHeight-barHeight-40, 1)
		return
	}
	hidden := float32(titleTop) + titleHeight*0.7
	if p.Eyebrow != "" {
		hidden += eyebrowH
	}
	a.bar.titleIn = scrolled > hidden
}

// scrollPage builds a page that scrolls: its large title, then body, which
// lays out in a column inside the content's gutters. A row that should
// reach the window's edges, as a shelf, takes Margin(0, -gutter). Where the
// page is scrolled is the page's own state, which the router keeps while
// the page is in its history.
func (a *app) scrollPage(c *ui.Context, r *ui.Route, p pageSpec, body func()) {
	r.Title(p.Title)
	h := barH(c)
	page := ui.Box(c.Key("page")).Fill()
	mem := a.pageMemFor(r)
	st := &mem.scroll
	a.takeScroll(st)
	outer := a.building
	a.building = mem
	defer func() { a.building = outer }()
	page.Children(func() {
		top := h + titleTop
		if p.Hero {
			top = 0
		}
		ui.Scroll(c.Key("scroll")).Fill().TrackScroll(st).ScrollbarInsets(h, 0, 4, 0).Children(func() {
			ui.Column(c.Key("content")).Padding(top, gutter, pageBottom, gutter).Children(func() {
				if !p.Hero && p.Title != "" {
					a.largeTitle(c, p)
				}
				body()
			})
		})
	})
	a.setBar(p, st.Y)
}

// gridPage builds a page of a grid of cards that loads more as it nears its
// end, as a library: its large title and Header scroll away with the
// cards. Empty, it shows Empty in place of the grid.
func (a *app) gridPage(c *ui.Context, r *ui.Route, p pageSpec, g gridSpec) {
	r.Title(p.Title)
	h := barH(c)
	page := ui.Box(c.Key("page")).Fill()
	mem := a.pageMemFor(r)
	v := &mem.grid
	a.takeScroll(&v.scroll)
	head := titleBlock(p) + g.HeaderHeight
	page.Children(func() {
		if g.Count == 0 && g.Empty != nil {
			ui.Column(c.Key("empty")).Fill().Padding(h+titleTop, gutter, pageBottom, gutter).Children(func() {
				a.largeTitle(c, p)
				if g.Header != nil {
					ui.Box(c).Height(g.HeaderHeight).Children(g.Header)
				}
				ui.Column(c).Grow(1).Center().Children(g.Empty)
			})
			v.scroll.Y = 0
			return
		}
		g.PadTop = h + titleTop + head
		a.posterGrid(c.Key("grid"), v, g).Fill().ScrollbarInsets(h, 0, 4, 0)
		// The header scrolls with the cards: it sits over the grid's top
		// padding and moves up as the grid scrolls.
		ui.Column(c.Key("head")).Absolute().Top(h+titleTop).Left(gutter).Right(gutter).
			Translate(0, -v.scroll.Y).PassThrough().Children(func() {
			if titleBlock(p) > 0 {
				a.largeTitle(c, p)
			}
			if g.Header != nil {
				ui.Box(c).Height(g.HeaderHeight).Children(g.Header)
			}
		})
	})
	a.setBar(p, v.scroll.Y)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
