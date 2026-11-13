// Package app is the desktop client's state and its native interface.
package app

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/player"
	"mygo-emby/internal/remux"
)

// app is the state the window shows. Everything in it is read and changed
// on the main thread: by the view, by menu items, and by goroutines through
// a.update.
type app struct {
	win    *mygo.Window
	router *ui.Router
	img    *imageCache
	http   *http.Client

	store    *settingsStore
	settings Settings

	pal   palette // the colors of this frame, set by applyTheme
	scale float32 // the screen's backing scale, to size pictures (imgPx)

	// The account in use (session.go): its client, nil while no one is
	// signed in, how the server last answered, and the accounts kept.
	screen     screen
	client     *emby.Client
	conn       connState
	connErr    error
	connGen    gen
	device     emby.DeviceInfo
	clientOpts []emby.Option // for tests: how clients reach their servers
	servers    []savedServer
	// secrets keeps the tokens: the Keychain, or memory in tests and the
	// screenshot run; plainTokens says it failed, and the settings keep
	// them instead.
	secrets     secretStore
	plainTokens bool

	// The sign-in panel (page_login.go), alone in the window or as a sheet
	// over the app (sheetOpen), and its fields' handles.
	login                       loginState
	loginGen, probeGen          gen
	sheetOpen                   bool
	serverBox, userBox, passBox ui.Handle
	accountOpen                 bool // the account's menu shows

	// The account's libraries, for the sidebar and their pages.
	libraries   []library
	viewsLoaded bool
	viewsErr    error
	viewsGen    gen

	// What the pages loaded for the account, dropped as it changes:
	// Home's, the lists of the libraries and their folders by listKey, and
	// the items by ID.
	epoch   uint64 // counts the resets: an answer of an older one is dropped
	home    *homeState
	lists   map[string]*listState
	details map[string]*detailState
	people  map[string]*personState
	favs    *favState
	found   *searchState
	// staleEpisodes says a series or a season was marked watched: the
	// episodes loaded go once the server confirms it.
	staleEpisodes bool

	lastPlay *playRequest // what play was last asked

	// form is the settings page's controls.
	form settingsForm

	// dev shows the developer's pages: EMBY_DEV=1.
	dev bool

	bar       barState       // what the page shown asks of the floating bar, each frame
	scrollReq *scrollRequest // a scroll the page shown takes as it next builds
	search    string         // the sidebar's search field
	searchBox ui.Handle
	toast     string // a message to show once, at the bottom of the window
	// reload counts refreshes (⌘R). A page that loads data remembers the
	// value it loaded at and loads again when it changes.
	reload int

	gallery galleryState

	// pageMems is what each page keeps of itself, by path (pagemem.go);
	// building is the memory of the page that builds now.
	pageMems     map[string]*pageMem
	pageMemOrder []string
	building     *pageMem

	// player plays over the whole window while it is Active (play.go);
	// remuxer serves what AVPlayer cannot read as it is. Both are made as
	// the first item plays.
	player  *player.Player
	remuxer *remux.Server

	// opaqueSidebar paints the sidebar where the window's material would
	// show, for the screenshots, which capture no material.
	opaqueSidebar bool

	// forceHover, when set, is the Key of a card drawn as if the pointer
	// were over it, for the screenshots.
	forceHover any

	// postFn, when set, takes what update would post to the main thread,
	// as in tests, which have no window.
	postFn func(func())
}

// library is a library of the server, as the sidebar lists it. Kind is the
// server's collection type: "movies", "tvshows", "music", "boxsets", …
type library struct {
	ID, Name, Kind string
}

// newApp makes the app's state, with its settings loaded from settingsPath
// ("" keeps none) and pictures cached in cacheDir ("" in memory only).
func newApp(settingsPath, cacheDir string) *app {
	var secrets secretStore = newMemSecrets()
	if settingsPath != "" {
		secrets = mygo.Secrets
	}
	return newAppWith(settingsPath, cacheDir, secrets)
}

// newAppWith is newApp keeping tokens in secrets.
func newAppWith(settingsPath, cacheDir string, secrets secretStore) *app {
	a := &app{scale: 2, dev: os.Getenv("EMBY_DEV") == "1", secrets: secrets}
	a.store, a.settings = openSettings(settingsPath)
	a.http = &http.Client{Timeout: 60 * time.Second}
	a.img = newImageCache(a.http, cacheDir, a.update)
	a.resetData()
	a.loadAccounts()
	return a
}

// update runs fn on the main thread and draws a frame after it: how
// goroutines hand results to the view. It returns at once. Without a
// window, as in tests, postFn takes fn, or it is dropped.
//
// A result that arrives after the user moved on must not overwrite newer
// state: compare a generation (see gen) or the route before applying it.
func (a *app) update(fn func()) {
	if a.postFn != nil {
		a.postFn(fn)
		return
	}
	if a.win == nil {
		return
	}
	a.win.Update(fn)
}

// redraw asks for a frame, for state changed outside the view, as by a menu
// item. Safe from any goroutine.
func (a *app) redraw() {
	if a.win != nil {
		a.win.Invalidate()
	}
}

// gen hands out generations of a load, so that the answer to a request
// superseded by a newer one is dropped:
//
//	n := a.homeGen.next()
//	go func() {
//		items, err := fetch()
//		a.update(func() {
//			if !a.homeGen.is(n) {
//				return // the user refreshed or left meanwhile
//			}
//			a.home, a.homeErr = items, err
//		})
//	}()
type gen struct{ n uint64 }

func (g *gen) next() uint64     { g.n++; return g.n }
func (g *gen) is(n uint64) bool { return g.n == n }

// imgPx is how many pixels wide to ask the server for a picture shown dips
// wide: its display size times the screen's scale, in steps.
func (a *app) imgPx(dips float32) int { return imagePx(dips, a.scale) }

// Run runs the app until it quits.
func Run() error {
	shots := os.Getenv("EMBY_SHOTS")
	if shots == "" && !mygo.App.RequestSingleInstanceLock() {
		return nil // the running app shows its window (OnSecondInstance)
	}
	var a *app
	mygo.App.WhenReady(func() {
		settingsPath, cacheDir := dataPaths()
		if shots != "" {
			// A run checking the design leaves the user's settings alone.
			settingsPath = ""
		}
		a = newApp(settingsPath, cacheDir)
		a.restore()
		a.start(shots)
	})
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) {
		if a == nil {
			return
		}
		a.closePlayer(3 * time.Second)
		if a.remuxer != nil {
			_ = a.remuxer.Close()
		}
		if err := a.store.Flush(); err != nil {
			log.Printf("saving settings: %v", err)
		}
		a.img.Close()
	})
	mygo.App.OnActivate(func(bool) { a.showWindow() })
	mygo.App.OnSecondInstance(func([]string, string) { a.showWindow() })
	return mygo.App.Run()
}

// dataPaths returns where the settings and the pictures are kept.
func dataPaths() (settingsPath, cacheDir string) {
	if dir, err := mygo.App.Path(mygo.PathUserData); err == nil {
		settingsPath = filepath.Join(dir, "settings.json")
	} else {
		log.Printf("no data directory: %v", err)
	}
	if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
		cacheDir = filepath.Join(dir, "images")
	}
	return settingsPath, cacheDir
}

// start opens the window, on the main thread once the app is ready. With
// shots set, it then walks through the screenshot steps (shots.go).
func (a *app) start(shots string) {
	a.img.onMain = mygo.RunOnMain
	if shots != "" {
		a.opaqueSidebar = true
		applyShotsAppearance()
	} else {
		applyThemeMode(a.settings.ThemeMode)
	}
	stateKey := "main"
	if shots != "" {
		stateKey = "" // the same size every run, and the user's left alone
	}
	a.win = mygo.NewWindow(mygo.WindowOptions{
		Title:                str.AppName,
		Width:                1280,
		Height:               820,
		MinWidth:             960,
		MinHeight:            620,
		StateKey:             stateKey,
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 20, Y: 20},
		Vibrancy:             mygo.VibrancySidebar,
		BackgroundColor:      "light-dark(#ffffff, #141416)",
		Content:              ui.View(a.view),
	})
	a.updateScale()
	mygo.Screen.OnDisplaysChanged(func() { mygo.RunOnMain(a.updateScale) })
	a.win.OnMove(a.updateScale)
	a.setMenu()
	go a.img.TrimDisk(imageDiskLimit)
	if shots != "" {
		// Keep normal window stacking in verification runs as well: taking
		// screenshots must not pin the app over the user's other windows.
		a.runShots(shots)
	}
}

// updateScale reads the scale of the display the window is on.
func (a *app) updateScale() {
	if a.win == nil {
		return
	}
	if d := mygo.Screen.DisplayMatching(a.win.Bounds()); d.ScaleFactor > 0 {
		a.scale = float32(d.ScaleFactor)
	}
}

// showWindow brings the window to the front, as the Dock icon does.
func (a *app) showWindow() {
	if a == nil || a.win == nil {
		return
	}
	if a.win.IsMinimized() {
		a.win.Restore()
	}
	a.win.Show()
	a.win.Focus()
}

// setMenu gives the app its menu bar, with the shortcuts of DESIGN.md §4.11.
func (a *app) setMenu() {
	run := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			fn()
			a.redraw()
		}
	}
	appMenu := []*mygo.MenuItem{
		{Role: mygo.RoleAbout, Label: str.MenuAbout},
		mygo.Separator(),
		{Label: str.MenuSettings, Accelerator: "CmdOrCtrl+,", Click: run(a.openSettings)},
		mygo.Separator(),
	}
	if runtime.GOOS == "darwin" {
		appMenu = append(appMenu,
			&mygo.MenuItem{Role: mygo.RoleServices, Label: str.MenuServices},
			mygo.Separator(),
			&mygo.MenuItem{Role: mygo.RoleHide, Label: str.MenuHide},
			&mygo.MenuItem{Role: mygo.RoleHideOthers, Label: str.MenuHideOthers},
			&mygo.MenuItem{Role: mygo.RoleUnhide, Label: str.MenuShowAll},
			mygo.Separator(),
		)
	}
	appMenu = append(appMenu, &mygo.MenuItem{Role: mygo.RoleQuit, Label: str.MenuQuit})
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: str.AppName, Submenu: appMenu},
		{Label: str.MenuEdit, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleUndo, Label: str.MenuUndo},
			{Role: mygo.RoleRedo, Label: str.MenuRedo},
			mygo.Separator(),
			{Role: mygo.RoleCut, Label: str.MenuCut},
			{Role: mygo.RoleCopy, Label: str.MenuCopy},
			{Role: mygo.RolePaste, Label: str.MenuPaste},
			{Role: mygo.RoleSelectAll, Label: str.MenuSelectAll},
		}},
		{Label: str.MenuView, Submenu: []*mygo.MenuItem{
			{Label: str.MenuRefresh, Accelerator: "CmdOrCtrl+R", Click: run(a.refresh)},
			{Label: str.MenuFind, Accelerator: "CmdOrCtrl+F", Click: run(a.focusSearch)},
			{Label: str.MenuSidebar, Accelerator: sidebarAccelerator(), Click: run(a.toggleSidebar)},
			mygo.Separator(),
			{Role: mygo.RoleToggleFullScreen, Label: str.MenuFullScreen},
		}},
		{Label: str.MenuGo, Submenu: []*mygo.MenuItem{
			{Label: str.Back, Accelerator: "CmdOrCtrl+[", Click: run(a.back)},
			{Label: str.Forward, Accelerator: "CmdOrCtrl+]", Click: run(a.forward)},
			mygo.Separator(),
			{Label: str.Home, Accelerator: "CmdOrCtrl+1", Click: run(func() { a.navigate(pathHome) })},
			{Label: str.Favorites, Accelerator: "CmdOrCtrl+2", Click: run(func() { a.navigate(pathFavorites) })},
		}},
		{Label: str.MenuWindow, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleMinimize, Label: str.MenuMinimize},
			{Role: mygo.RoleZoom, Label: str.MenuZoom},
			{Role: mygo.RoleClose, Label: str.MenuClose},
			mygo.Separator(),
			{Role: mygo.RoleFront, Label: str.MenuFront},
		}},
	}))
}

// Actions of the menus and the sidebar.

// back and forward go through the pages' history, as ⌘[ and ⌘] do.
func (a *app) back() {
	a.router.Back()
	a.prefetch(a.router.Location())
}

func (a *app) forward() {
	a.router.Forward()
	a.prefetch(a.router.Location())
}

// openSettings shows the settings page (⌘,).
func (a *app) openSettings() { a.navigate(pathSettings) }

// focusSearch puts the keyboard in the sidebar's search field (⌘F).
func (a *app) focusSearch() {
	if a.settings.SidebarCollapsed {
		a.setSidebarCollapsed(false)
	}
	a.showWindow()
	a.searchBox.Focus()
}

// refresh asks the page shown to load what it shows again (⌘R), and lets
// pictures that failed be tried again; the libraries load again too.
func (a *app) refresh() {
	if a.client == nil {
		return
	}
	if a.conn == connOffline {
		a.checkSession()
	}
	a.reload++
	a.img.ForgetFailures()
	a.loadViews()
	a.prefetch(a.router.Location())
}

// switchServer shows the servers kept, to switch to another (the account
// menu's).
func (a *app) switchServer() { a.openServers() }
