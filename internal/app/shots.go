package app

import (
	"cmp"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"mygo-emby/internal/emby"
)

// The screenshot self-check (DESIGN.md §6). With EMBY_SHOTS set to a
// directory, the app shows its window, walks through the steps below, saves
// what the window shows after each named one as <name>.png there, and quits.
// EMBY_SHOTS_DARK=1 runs it dark and EMBY_SHOTS_DARK=0 light; the names
// then end in -dark or -light. The run leaves the user's settings and window
// size alone, and signs out as it ends.
//
// It signs in to EMBY_SHOTS_SERVER as EMBY_SHOTS_USER with
// EMBY_SHOTS_PASS, or to Jellyfin's demo server without them:
//
//	EMBY_SHOTS=build/shots/integration/emby-light EMBY_SHOTS_DARK=0 \
//	EMBY_SHOTS_SERVER=… EMBY_SHOTS_USER=… EMBY_SHOTS_PASS=… go run .
//
// Pages add their own steps from their files, in an init function, at an
// order that places them among the others:
//
//	func init() {
//		addShots(30,
//			shotStep{Do: func(a *app) { a.navigate(pathItem(id)) }, Until: loaded},
//			shotStep{Name: "item"},
//		)
//	}

// shotStep is one step of the walk: if If holds (always without it), Do
// runs on the main thread, then the walk waits Wait (a moment when zero),
// or, with Until, until it holds, checked on the main thread, up to Wait
// (30 seconds when zero) and Settle more for the pictures to come. Then it
// captures the window as Name, if it has one.
type shotStep struct {
	Name   string
	If     func(a *app) bool
	Do     func(a *app)
	Wait   time.Duration
	Until  func(a *app) bool
	Settle time.Duration
}

type orderedShots struct {
	order int
	steps []shotStep
}

var shotPlan []orderedShots

// addShots adds steps to the walk, at order among the others.
func addShots(order int, steps ...shotStep) {
	shotPlan = append(shotPlan, orderedShots{order, steps})
}

// shotSuffix ends the names of a run that forces an appearance.
var shotSuffix string

// applyShotsAppearance makes the run light or dark, as EMBY_SHOTS_DARK says.
func applyShotsAppearance() {
	switch os.Getenv("EMBY_SHOTS_DARK") {
	case "1", "true", "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
		shotSuffix = "-dark"
	case "0", "false", "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
		shotSuffix = "-light"
	}
}

// onMain runs fn on the main thread and returns what it says.
func onMain(fn func() bool) bool {
	ch := make(chan bool, 1)
	mygo.RunOnMain(func() { ch <- fn() })
	return <-ch
}

// runShots walks the steps on a goroutine, then quits the app.
func (a *app) runShots(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("shots: %v", err)
	}
	plan := slices.Clone(shotPlan)
	slices.SortStableFunc(plan, func(x, y orderedShots) int { return cmp.Compare(x.order, y.order) })
	// EMBY_SHOTS_FROM=<order> walks only the sign-in (order 0) and the
	// groups from that order on, to check one page quickly.
	if from, err := strconv.Atoi(os.Getenv("EMBY_SHOTS_FROM")); err == nil && from > 0 {
		plan = slices.DeleteFunc(plan, func(g orderedShots) bool { return g.order != 0 && g.order < from })
	}
	go func() {
		mygo.RunOnMain(func() { a.win.Show(); a.win.Focus() })
		time.Sleep(1500 * time.Millisecond) // the window shows
		for _, group := range plan {
			for _, s := range group.steps {
				if s.If != nil && !onMain(func() bool { return s.If(a) }) {
					continue
				}
				if s.Do != nil {
					mygo.RunOnMain(func() {
						s.Do(a)
						a.redraw()
					})
				}
				if s.Until != nil {
					// Out of sight, the window builds no frames: bring it
					// back in case something covered it meanwhile.
					mygo.RunOnMain(func() { a.win.Show() })
					wait := cmp.Or(s.Wait, 30*time.Second)
					deadline := time.Now().Add(wait)
					for !onMain(func() bool { return s.Until(a) }) {
						if time.Now().After(deadline) {
							log.Printf("shots: gave up waiting before %q", s.Name)
							break
						}
						time.Sleep(150 * time.Millisecond)
					}
					time.Sleep(cmp.Or(s.Settle, 1500*time.Millisecond))
				} else {
					time.Sleep(cmp.Or(s.Wait, 700*time.Millisecond))
				}
				if s.Name == "" {
					continue
				}
				png, err := a.win.CapturePage()
				if err != nil {
					log.Printf("shots: capturing %s: %v", s.Name, err)
					continue
				}
				path := filepath.Join(dir, s.Name+shotSuffix+".png")
				if err := os.WriteFile(path, png, 0o644); err != nil {
					log.Printf("shots: %v", err)
				}
				log.Printf("shots: %s", path)
			}
		}
		mygo.RunOnMain(mygo.App.Quit)
	}()
}

// shotTarget is the account the walk signs in to.
func shotTarget() (server, user, pass string) {
	if s := os.Getenv("EMBY_SHOTS_SERVER"); s != "" {
		return s, os.Getenv("EMBY_SHOTS_USER"), os.Getenv("EMBY_SHOTS_PASS")
	}
	return demoServer, "demo", ""
}

// shotsState is what the walk found on its way, for its later steps.
var shotsState struct {
	lib    library // a library of films or shows, whose grid it shows
	folder library // a library it opens a folder of
	shows  library // a library of shows, whose first series it opens
	series string  // that series
	person string  // a person of an item's cast
}

// homeReady reports whether Home shows what it loaded.
func homeReady(a *app) bool {
	return a.screen == screenMain && a.home.loaded && !a.home.loading
}

// listReady reports whether the page shown has its list.
func listReady(a *app, lib library, folderID string) bool {
	ls := a.lists[listKey(lib, folderID, a.prefsFor(lib.ID))]
	return ls != nil && ls.loaded && !ls.loading
}

// shotLibrary picks the library whose grid the walk shows: of films if
// there is one, else of shows, else the first.
func shotLibrary(a *app) library {
	for _, kind := range []string{"movies", "tvshows"} {
		for _, l := range a.libraries {
			if l.Kind == kind {
				return l
			}
		}
	}
	if len(a.libraries) > 0 {
		return a.libraries[0]
	}
	return library{}
}

// shotFolderLibrary picks a library browsed by folder, else one of shows,
// whose series the walk opens as a folder of seasons.
func shotFolderLibrary(a *app) library {
	for _, l := range a.libraries {
		if browsesFolders(l.Kind) && l.Kind != "playlists" {
			return l
		}
	}
	for _, l := range a.libraries {
		if l.Kind == "tvshows" {
			return l
		}
	}
	return shotLibrary(a)
}

// The walk: signing in, Home, a library, a folder, an item, the account's
// menu and the servers, the offline state, then the window's sizes, and
// signing out.
func init() {
	addShots(0,
		shotStep{Wait: 1200 * time.Millisecond},
		shotStep{Name: "login"},
		shotStep{Do: func(a *app) {
			a.login = newLoginState()
			a.login.server, a.login.user = "127.0.0.1:9", "demo"
			a.submitLogin()
		}, Until: func(a *app) bool { return !a.login.busy }, Wait: 15 * time.Second, Settle: 500 * time.Millisecond},
		shotStep{Name: "login-error"},
		shotStep{Do: func(a *app) {
			server, user, pass := shotTarget()
			a.login = newLoginState()
			a.login.server, a.login.user, a.login.pass = server, user, pass
			a.submitLogin()
		}, Until: homeReady, Wait: 60 * time.Second, Settle: 4 * time.Second},
		shotStep{Name: "home-top"},
	)
	addShots(20,
		shotStep{Do: func(a *app) {
			w, _ := a.win.Size()
			a.scrollPageTo(heroHeight(a.contentWidth(float32(w))) - 60)
			if d := a.home.data; len(d.resume) > 0 {
				a.forceHover = d.resume[0].ID
			} else if len(d.latest) > 0 {
				a.forceHover = d.latest[0].items[0].ID
			}
		}, Wait: 2500 * time.Millisecond},
		shotStep{Name: "home-scrolled"},
		shotStep{Do: func(a *app) {
			a.forceHover = nil
			a.scrollPageTo(1e6)
		}, Wait: 2500 * time.Millisecond},
		shotStep{Name: "home-bottom"},
	)
	addShots(30,
		shotStep{Do: func(a *app) {
			shotsState.lib = shotLibrary(a)
			a.navigate(pathLibrary(shotsState.lib.ID))
		}, Until: func(a *app) bool { return listReady(a, shotsState.lib, "") }, Settle: 3 * time.Second},
		shotStep{Name: "library-grid"},
		shotStep{Do: func(a *app) { a.setSort(shotsState.lib.ID, sortYear) },
			Until: func(a *app) bool { return listReady(a, shotsState.lib, "") }, Settle: 3 * time.Second},
		shotStep{Name: "library-sorted"},
		shotStep{Do: func(a *app) { a.scrollPageTo(2400) }, Wait: 3 * time.Second},
		shotStep{Name: "library-scrolled"},
	)
	addShots(40,
		shotStep{Do: func(a *app) {
			shotsState.folder = shotFolderLibrary(a)
			a.navigate(pathLibrary(shotsState.folder.ID))
		}, Until: func(a *app) bool { return listReady(a, shotsState.folder, "") }},
		shotStep{Do: func(a *app) {
			lib := shotsState.folder
			for _, it := range a.list(lib, "", a.prefsFor(lib.ID)).items {
				if it.IsFolderLike() {
					a.navigate(pathFolder(lib.ID, it.ID, it.Name))
					return
				}
			}
		}, Until: func(a *app) bool {
			_, folder, _ := strings.Cut(strings.TrimPrefix(a.router.Path(), pathLibrary(shotsState.folder.ID)), "/")
			return folder != "" && listReady(a, shotsState.folder, folder)
		}, Settle: 3 * time.Second},
		shotStep{Name: "folder-drill"},
	)
	addShots(50,
		shotStep{Do: func(a *app) {
			lib := shotsState.lib
			items := a.list(lib, "", a.prefsFor(lib.ID)).items
			for _, it := range items {
				if _, ok := it.Image(emby.RoleBackdrop); ok {
					a.navigate(pathItem(it.ID))
					return
				}
			}
			if len(items) > 0 {
				a.navigate(pathItem(items[0].ID))
			}
		}, Until: func(a *app) bool {
			d := a.details[strings.TrimPrefix(a.router.Path(), "/item/")]
			return d != nil && d.item != nil
		}, Settle: 3 * time.Second},
		shotStep{Name: "item"},
	)
	addShots(55,
		shotStep{Do: func(a *app) { a.scrollPageTo(560) }, Wait: 1500 * time.Millisecond},
		shotStep{Name: "item-scrolled"},
		shotStep{Do: func(a *app) {
			// A person of the item's cast.
			d := a.details[strings.TrimPrefix(a.router.Path(), "/item/")]
			if d == nil || d.item == nil {
				return
			}
			for _, p := range d.item.Cast() {
				if p.ID != "" {
					shotsState.person = p.ID
					a.navigate(pathPerson(p.ID))
					return
				}
			}
		}, Until: func(a *app) bool {
			ps := a.people[shotsState.person]
			return ps != nil && ps.person != nil && !ps.loading
		}, Settle: 3 * time.Second},
		shotStep{Name: "person", If: func(a *app) bool { return shotsState.person != "" }},
		shotStep{Do: func(a *app) {
			for _, l := range a.libraries {
				if l.Kind == "tvshows" {
					shotsState.shows = l
					a.navigate(pathLibrary(l.ID))
					return
				}
			}
		}, Until: func(a *app) bool { return shotsState.shows.ID != "" && listReady(a, shotsState.shows, "") }},
		shotStep{Do: func(a *app) {
			if items := a.list(shotsState.shows, "", a.prefsFor(shotsState.shows.ID)).items; len(items) > 0 {
				shotsState.series = items[0].ID
				a.navigate(pathItem(items[0].ID))
			}
		}, Until: func(a *app) bool {
			d := a.details[shotsState.series]
			if d == nil || d.item == nil {
				return false
			}
			eps := d.episodes[d.season]
			return len(d.seasons) == 0 || (eps != nil && eps.loaded)
		}, Settle: 3 * time.Second},
		shotStep{Name: "series", If: func(a *app) bool { return shotsState.series != "" }},
		shotStep{Do: func(a *app) { a.scrollPageTo(620) }, Wait: 2 * time.Second},
		shotStep{Name: "series-episodes", If: func(a *app) bool { return shotsState.series != "" }},
		shotStep{Do: func(a *app) {
			q := "a"
			if items := a.list(shotsState.lib, "", a.prefsFor(shotsState.lib.ID)).items; len(items) > 0 {
				q = string([]rune(items[0].Name)[:min(2, len([]rune(items[0].Name)))])
			}
			a.search = q
			a.searchChanged()
		}, Until: func(a *app) bool { return a.found.res != nil && !a.found.loading }, Settle: 3 * time.Second},
		shotStep{Name: "search"},
		shotStep{Do: func(a *app) {
			a.search = ""
			a.searchChanged()
			a.navigate(pathFavorites)
		}, Until: func(a *app) bool { return a.favs.loaded }, Settle: 3 * time.Second},
		shotStep{Name: "favorites"},
		shotStep{Do: func(a *app) { a.openSettings() }, Wait: 2 * time.Second},
		shotStep{Name: "settings"},
	)
	addShots(60,
		shotStep{Do: func(a *app) {
			a.navigate(pathHome)
			a.accountOpen = true
		}, Wait: 2 * time.Second},
		shotStep{Name: "account-menu"},
		shotStep{Do: func(a *app) {
			a.accountOpen = false
			// A second account, signed out, for the list to show both
			// kinds of rows; the run keeps no settings.
			other := emby.Session{ServerURL: demoServer, ServerName: "Stable Demo", ServerKind: emby.ServerKindJellyfin, UserName: "demo"}
			if server, _, _ := shotTarget(); server == demoServer {
				other = emby.Session{ServerURL: "https://media.example.com", ServerName: "家庭影院", UserName: "jackie"}
			}
			a.servers = append(a.servers, savedServer{Session: other})
			a.openServers()
		}, Wait: 1200 * time.Millisecond},
		shotStep{Name: "servers"},
		shotStep{Do: func(a *app) { a.login = newLoginState() }, Wait: 2 * time.Second},
		shotStep{Name: "servers-add"},
		shotStep{Do: func(a *app) {
			a.sheetOpen = false
			a.conn, a.connErr = connOffline, emby.ErrUnreachable
		}, Wait: time.Second},
		shotStep{Name: "offline"},
		shotStep{Do: func(a *app) { a.conn, a.connErr = connOK, nil }, Wait: 1500 * time.Millisecond},
	)
	addShots(99,
		shotStep{Do: func(a *app) {
			a.win.SetSize(1280, 820)
			a.signOut()
		}, Wait: 3 * time.Second},
		shotStep{Name: "signed-out"},
	)
}

// The window's own steps: its size.
func init() {
	addShots(90,
		shotStep{Do: func(a *app) {
			a.win.SetSize(960, 620)
			a.scrollPageTo(0)
			a.navigate(pathHome)
		}, Wait: 2500 * time.Millisecond},
		shotStep{Name: "min-size"},
		shotStep{Do: func(a *app) { a.scrollPageTo(700) }, Wait: time.Second},
		shotStep{Name: "min-size-scrolled"},
		shotStep{Do: func(a *app) {
			a.win.SetSize(1720, 980)
			a.scrollPageTo(0)
		}, Wait: 3 * time.Second},
		shotStep{Name: "wide"},
	)
}
