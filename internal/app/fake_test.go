package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// fakeServer is an Emby server in the test process: an account, its
// libraries and their items, as the app asks for them. It records what it
// was asked, and can refuse the token, drop connections, or hold requests
// for items until the test lets them go.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	name, id   string
	jellyfin   bool
	user, pass string
	userID     string
	token      string
	revoked    bool // the token no longer holds: 401
	down       bool // connections are dropped, as an unreachable server's
	authStatus int  // AuthenticateByName answers this status, when set
	views      []emby.Item
	children   map[string][]emby.Item // the items under each parent
	resume     []emby.Item
	nextUp     []emby.Item
	latest     map[string][]emby.Item
	seasons    map[string][]emby.Item // the seasons of each series
	episodes   map[string][]emby.Item // the episodes of each season
	persons    []emby.Item            // people, for the search and their pages
	similar    map[string][]emby.Item
	personFail bool            // listing a person's works fails, as on the Emby test server
	dataFail   bool            // changing what the user watched or keeps fails
	hold       bool            // requests for items wait in held
	held       []chan struct{} // until the test closes them
	reqs       []fakeReq
	dataCalls  []string // changes of what the user watched or keeps
	auths      int
	logouts    int
}

// fakeReq is a request the server was asked.
type fakeReq struct {
	method, path string
	query        url.Values
}

// newFakeServer starts a server named name, whose account jackie has the
// password secret and two libraries: 电影 (films) and 剧集 (shows).
func newFakeServer(t *testing.T, name string) *fakeServer {
	t.Helper()
	f := &fakeServer{
		t:        t,
		name:     name,
		id:       "srv-" + name,
		user:     "jackie",
		pass:     "secret",
		userID:   "u-" + name,
		token:    "tok-" + name,
		children: map[string][]emby.Item{},
		latest:   map[string][]emby.Item{},
		seasons:  map[string][]emby.Item{},
		episodes: map[string][]emby.Item{},
		similar:  map[string][]emby.Item{},
	}
	f.views = []emby.Item{
		{ID: name + "-movies", Name: "电影", Type: emby.TypeCollectionFolder, CollectionType: emby.CollectionMovies, IsFolder: true},
		{ID: name + "-shows", Name: "剧集", Type: emby.TypeCollectionFolder, CollectionType: emby.CollectionTVShows, IsFolder: true},
	}
	f.children[name+"-movies"] = fakeItems(name+"-m", 8, emby.TypeMovie)
	f.children[name+"-shows"] = fakeItems(name+"-s", 4, emby.TypeSeries)
	f.srv = httptest.NewServer(f)
	t.Cleanup(func() {
		f.release()
		f.srv.Close()
	})
	return f
}

// fakeItems makes n items of a type, named prefix-0, prefix-1, …
func fakeItems(prefix string, n int, typ string) []emby.Item {
	items := make([]emby.Item, n)
	for i := range items {
		items[i] = emby.Item{
			ID: fmt.Sprintf("%s-%d", prefix, i), Name: fmt.Sprintf("%s-%d", prefix, i), Type: typ,
			ProductionYear: 2000 + i, IsFolder: typ == emby.TypeSeries || typ == emby.TypeFolder,
		}
	}
	return items
}

func (f *fakeServer) URL() string { return f.srv.URL }

// session is the account's session, as a sign-in would give.
func (f *fakeServer) session() emby.Session {
	kind := emby.ServerKindEmby
	if f.jellyfin {
		kind = emby.ServerKindJellyfin
	}
	return emby.Session{ServerURL: f.URL(), ServerID: f.id, ServerName: f.name, ServerKind: kind,
		UserID: f.userID, UserName: f.user, Token: f.token}
}

func (f *fakeServer) set(fn func(f *fakeServer)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// requests returns the requests to paths that start with prefix.
func (f *fakeServer) requests(prefix string) []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeReq
	for _, r := range f.reqs {
		if strings.HasPrefix(r.path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// itemQueries returns the queries of the requests for items of a parent.
func (f *fakeServer) itemQueries(parent string) []url.Values {
	var out []url.Values
	for _, r := range f.requests("/Users/" + f.userID + "/Items") {
		if r.path == "/Users/"+f.userID+"/Items" && r.query.Get("ParentId") == parent {
			out = append(out, r.query)
		}
	}
	return out
}

// heldCount returns how many requests wait.
func (f *fakeServer) heldCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held)
}

// releaseAt lets the i-th request held go.
func (f *fakeServer) releaseAt(i int) {
	f.mu.Lock()
	ch := f.held[i]
	f.mu.Unlock()
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// release lets every request held go, and holds no more.
func (f *fakeServer) release() {
	f.mu.Lock()
	held := f.held
	f.hold = false
	f.mu.Unlock()
	for _, ch := range held {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, fakeReq{r.Method, r.URL.Path, r.URL.Query()})
	down := f.down
	f.mu.Unlock()
	if down {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	path := r.URL.Path
	q := r.URL.Query()
	switch {
	case path == "/System/Info/Public":
		info := map[string]any{"Id": f.id, "ServerName": f.name, "Version": "4.9.3.0"}
		if f.jellyfin {
			info["ProductName"] = "Jellyfin Server"
		}
		writeJSON(w, info)
		return
	case path == "/Users/AuthenticateByName" && r.Method == http.MethodPost:
		var body struct{ Username, Pw string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.auths++
		status := f.authStatus
		ok := body.Username == f.user && body.Pw == f.pass
		f.mu.Unlock()
		switch {
		case status != 0:
			http.Error(w, "nope", status)
		case !ok:
			http.Error(w, "Invalid username or password entered.", http.StatusUnauthorized)
		default:
			writeJSON(w, map[string]any{
				"User":        map[string]any{"Id": f.userID, "Name": f.user, "ServerId": f.id},
				"AccessToken": f.token, "ServerId": f.id,
			})
		}
		return
	}
	f.mu.Lock()
	authorized := r.Header.Get("X-Emby-Token") == f.token && !f.revoked
	f.mu.Unlock()
	if !authorized {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	user := "/Users/" + f.userID
	switch {
	case path == "/Sessions/Logout":
		f.set(func(f *fakeServer) { f.logouts++ })
		w.WriteHeader(http.StatusNoContent)
	case path == user:
		writeJSON(w, map[string]any{"Id": f.userID, "Name": f.user, "ServerId": f.id})
	case path == user+"/Views":
		f.mu.Lock()
		views := f.views
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: views, TotalRecordCount: len(views)})
	case path == user+"/Items/Resume":
		f.mu.Lock()
		items := f.resume
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: items, TotalRecordCount: len(items)})
	case path == "/Shows/NextUp":
		f.mu.Lock()
		items := f.nextUp
		if s := q.Get("SeriesId"); s != "" {
			items = slices.DeleteFunc(slices.Clone(items), func(it emby.Item) bool { return it.SeriesID != s })
		}
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: items})
	case path == user+"/Items/Latest":
		f.mu.Lock()
		items := f.latest[q.Get("ParentId")]
		f.mu.Unlock()
		if items == nil {
			items = []emby.Item{}
		}
		writeJSON(w, items)
	case path == user+"/Items" && q.Get("ParentId") == "":
		f.mu.Lock()
		defer f.mu.Unlock()
		if q.Get("PersonIds") != "" && f.personFail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		var out []emby.Item
		for _, it := range f.allLocked() {
			switch {
			case q.Get("IncludeItemTypes") != "" && !slices.Contains(strings.Split(q.Get("IncludeItemTypes"), ","), it.Type):
			case q.Get("SearchTerm") != "" && !strings.Contains(strings.ToLower(it.Name), strings.ToLower(q.Get("SearchTerm"))):
			case strings.Contains(q.Get("Filters"), "IsFavorite") && !it.IsFavorite():
			case q.Get("PersonIds") != "" && !slices.ContainsFunc(it.People, func(p emby.Person) bool { return p.ID == q.Get("PersonIds") }):
			default:
				out = append(out, it)
			}
		}
		writeJSON(w, emby.QueryResult{Items: out, TotalRecordCount: len(out)})
	case strings.HasPrefix(path, user+"/FavoriteItems/") || strings.HasPrefix(path, user+"/PlayedItems/"):
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.dataFail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		id := path[strings.LastIndex(path, "/")+1:]
		on := r.Method == http.MethodPost
		var ud emby.UserData
		f.eachLocked(func(it *emby.Item) {
			if it.ID != id {
				return
			}
			if it.UserData == nil {
				it.UserData = &emby.UserData{}
			}
			if strings.Contains(path, "/FavoriteItems/") {
				it.UserData.IsFavorite = on
			} else {
				it.UserData.Played = on
				it.UserData.PlaybackPositionTicks = 0
			}
			ud = *it.UserData
		})
		f.dataCalls = append(f.dataCalls, r.Method+" "+path)
		writeJSON(w, ud)
	case strings.HasPrefix(path, "/Shows/") && strings.HasSuffix(path, "/Seasons"):
		f.mu.Lock()
		items := f.seasons[strings.TrimSuffix(strings.TrimPrefix(path, "/Shows/"), "/Seasons")]
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: items, TotalRecordCount: len(items)})
	case strings.HasPrefix(path, "/Shows/") && strings.HasSuffix(path, "/Episodes"):
		f.mu.Lock()
		items := f.episodes[q.Get("SeasonId")]
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: items, TotalRecordCount: len(items)})
	case strings.HasPrefix(path, "/Items/") && strings.HasSuffix(path, "/Similar"):
		f.mu.Lock()
		items := f.similar[strings.TrimSuffix(strings.TrimPrefix(path, "/Items/"), "/Similar")]
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: items, TotalRecordCount: len(items)})
	case path == "/Persons":
		f.mu.Lock()
		var out []emby.Item
		for _, p := range f.persons {
			if strings.Contains(strings.ToLower(p.Name), strings.ToLower(q.Get("SearchTerm"))) {
				out = append(out, p)
			}
		}
		f.mu.Unlock()
		writeJSON(w, emby.QueryResult{Items: out, TotalRecordCount: len(out)})
	case path == user+"/Items":
		f.mu.Lock()
		var wait chan struct{}
		if f.hold {
			wait = make(chan struct{})
			f.held = append(f.held, wait)
		}
		f.mu.Unlock()
		if wait != nil {
			<-wait
		}
		f.mu.Lock()
		all := f.children[q.Get("ParentId")]
		f.mu.Unlock()
		start, _ := strconv.Atoi(q.Get("StartIndex"))
		limit, _ := strconv.Atoi(q.Get("Limit"))
		page := all[min(start, len(all)):]
		if limit > 0 && len(page) > limit {
			page = page[:limit]
		}
		writeJSON(w, emby.QueryResult{Items: page, TotalRecordCount: len(all), StartIndex: start})
	case strings.HasPrefix(path, user+"/Items/"):
		id := strings.TrimPrefix(path, user+"/Items/")
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, it := range f.allLocked() {
			if it.ID == id {
				writeJSON(w, it)
				return
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// eachLocked calls fn with every item the server has, by pointer, with
// f.mu held.
func (f *fakeServer) eachLocked(fn func(it *emby.Item)) {
	for _, m := range []map[string][]emby.Item{f.children, f.seasons, f.episodes} {
		for _, items := range m {
			for i := range items {
				fn(&items[i])
			}
		}
	}
	for i := range f.persons {
		fn(&f.persons[i])
	}
}

// allLocked returns every item the server has, with f.mu held.
func (f *fakeServer) allLocked() []emby.Item {
	var out []emby.Item
	f.eachLocked(func(it *emby.Item) { out = append(out, *it) })
	slices.SortFunc(out, func(x, y emby.Item) int { return strings.Compare(x.ID, y.ID) })
	return out
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// waitFor runs what the app's goroutines post, and frames of tt when set,
// until cond holds, failing the test after a while.
func (ta *testApp) waitFor(t *testing.T, tt *ui.Tester, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ta.runPosted()
		if tt != nil {
			tt.Frame()
		}
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// signIn signs the app in to f through the form, and waits for the app
// and its libraries to show.
func (ta *testApp) signIn(t *testing.T, tt *ui.Tester, f *fakeServer) {
	t.Helper()
	ta.login.mode = loginForm
	ta.login.server, ta.login.user, ta.login.pass = f.URL(), f.user, f.pass
	ta.submitLogin()
	ta.waitFor(t, tt, "signing in", func() bool {
		return ta.screen == screenMain && ta.client != nil && ta.client.BaseURL() == f.URL() && ta.viewsLoaded
	})
	if tt != nil {
		settle(tt)
	}
}
