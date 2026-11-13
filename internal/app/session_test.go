package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

func TestLoginThroughTheForm(t *testing.T) {
	f := newFakeServer(t, "客厅")
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	if a.screen != screenLogin || !tt.HasText(str.LoginTitle) {
		t.Fatalf("a first run shows %q", tt.Texts())
	}
	// The address field has the focus; the labels focus the others.
	tt.Type(f.URL())
	if err := tt.Click(str.UserName); err != nil {
		t.Fatal(err)
	}
	tt.Type("jackie")
	if err := tt.Click(str.Password); err != nil {
		t.Fatal(err)
	}
	tt.Type("secret")
	// Enter signs in, once: a second Enter while it waits does nothing.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !a.login.busy || !tt.HasText(str.SigningIn) {
		t.Fatalf("Enter did not sign in: busy %v, %q", a.login.busy, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	a.submitLogin()
	a.waitFor(t, tt, "the app", func() bool { return a.screen == screenMain && a.viewsLoaded })
	if n := len(f.requests("/Users/AuthenticateByName")); n != 1 {
		t.Errorf("signed in %d times", n)
	}
	// The sidebar shows the account and its libraries.
	settle(tt)
	for _, text := range []string{"jackie", "客厅", "电影", "剧集"} {
		if !tt.HasText(text) {
			t.Errorf("the app shows no %q: %q", text, tt.Texts())
		}
	}
	// The session and the server are kept.
	s, ok := a.savedSession()
	if !ok || s.Token != f.token || s.ServerURL != f.URL() || s.ServerName != "客厅" {
		t.Errorf("kept session %+v", s)
	}
	if len(a.servers) != 1 || a.servers[0].Token != f.token {
		t.Errorf("kept servers %+v", a.servers)
	}
	// The token is in the credential store, not in the settings.
	if strings.Contains(string(a.settings.Servers)+string(a.settings.Session), f.token) {
		t.Errorf("the settings hold the token: %s %s", a.settings.Servers, a.settings.Session)
	}
	if v, err := a.secrets.Get(tokenRef(f.session())); err != nil || string(v) != f.token {
		t.Errorf("the store holds %q, %v", v, err)
	}
	if a.settings.DeviceID == "" || a.device.DeviceID != a.settings.DeviceID || a.device.Client != "MyGo Emby" {
		t.Errorf("device %+v", a.device)
	}
}

func TestLoginEmptyPassword(t *testing.T) {
	f := newFakeServer(t, "Demo")
	f.set(func(f *fakeServer) { f.pass = "" })
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	a.signIn(t, tt, f)
}

// The address the server answered at shows its name and kind, before the
// user signs in.
func TestLoginProbesTheServer(t *testing.T) {
	f := newFakeServer(t, "Stable Demo")
	f.set(func(f *fakeServer) { f.jellyfin = true })
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	tt.Type(f.URL())
	tt.Advance(probeDelay + 100*1e6)
	a.waitFor(t, tt, "the probe", func() bool { return a.login.probe != nil })
	if !tt.HasText("Stable Demo") || !tt.HasText("· Jellyfin 4.9.3.0") {
		t.Errorf("the probe shows %q", tt.Texts())
	}
	// Typing on forgets it.
	tt.Type("/x")
	tt.Frame()
	if a.login.probe != nil || tt.HasText("Stable Demo") {
		t.Error("a changed address keeps the old server's name")
	}
}

func TestLoginErrors(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	unreachable := closed.URL
	closed.Close()
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>Welcome to nginx</body></html>")
	}))
	t.Cleanup(html.Close)
	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	fake := newFakeServer(t, "Fake")

	cases := []struct {
		name         string
		server, user string
		pass         string
		status       int // the server's answer to signing in
		want         string
		on           errField
	}{
		{"invalid address", "http://", "jackie", "", 0, str.ErrInvalidURL, errServer},
		{"no user", fake.URL(), "  ", "", 0, str.ErrEnterUser, errUser},
		{"unreachable", unreachable, "jackie", "", 0, str.ErrUnreachable, errServer},
		{"not a server", html.URL, "jackie", "", 0, str.ErrNotServer, errServer},
		{"nothing there", missing.URL, "jackie", "", 0, str.ErrNotServer, errServer},
		{"wrong password", fake.URL(), "jackie", "nope", 0, str.ErrBadCredentials, errPassword},
		{"disabled", fake.URL(), "jackie", "secret", http.StatusForbidden, str.ErrForbidden, errGeneral},
		{"server error", fake.URL(), "jackie", "secret", http.StatusInternalServerError, fmt.Sprintf(str.ErrServerFmt, 500), errGeneral},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake.set(func(f *fakeServer) { f.authStatus = tc.status })
			a := newTestApp(t)
			tt := ui.NewTester(a.view, 1280, 820)
			a.login.server, a.login.user, a.login.pass = tc.server, tc.user, tc.pass
			if err := tt.Click(str.SignIn); err != nil {
				t.Fatal(err)
			}
			a.waitFor(t, tt, "the answer", func() bool { return !a.login.busy })
			if a.login.err != tc.want || a.login.errOn != tc.on {
				t.Errorf("error %q on %d, want %q on %d", a.login.err, a.login.errOn, tc.want, tc.on)
			}
			if !tt.HasText(tc.want) {
				t.Errorf("the form does not say %q: %q", tc.want, tt.Texts())
			}
			if a.screen != screenLogin || a.client != nil {
				t.Error("a failed sign-in let the user in")
			}
		})
	}
}

// keepSession stores s as the session in use, with its server kept, as a
// past run would have.
func keepSession(a *testApp, s emby.Session) {
	a.servers = nil
	a.remember(s)
	a.persistServers(s)
	a.servers[0].Token = "" // read again from the store, as at a start
}

func TestRestoreValidSession(t *testing.T) {
	f := newFakeServer(t, "书房")
	a := newTestApp(t)
	keepSession(a, f.session())
	a.restore()
	if a.screen != screenMain || a.conn != connChecking {
		t.Fatalf("restoring shows screen %d, conn %d", a.screen, a.conn)
	}
	tt := ui.NewTester(a.view, 1280, 820)
	if !tt.HasText("jackie") || !tt.HasText("书房") {
		t.Errorf("the sidebar does not show the session at once: %q", tt.Texts())
	}
	a.waitFor(t, tt, "the check", func() bool { return a.conn == connOK && a.viewsLoaded })
	if len(f.requests("/Users/"+f.userID)) == 0 {
		t.Error("the session was not checked")
	}
}

func TestRestoreExpiredSession(t *testing.T) {
	f := newFakeServer(t, "书房")
	f.set(func(f *fakeServer) { f.revoked = true })
	a := newTestApp(t)
	keepSession(a, f.session())
	a.restore()
	tt := ui.NewTester(a.view, 1280, 820)
	a.waitFor(t, tt, "the sign-in screen", func() bool { return a.screen == screenLogin })
	if a.login.notice != str.SessionExpired || !tt.HasText(str.SessionExpired) {
		t.Errorf("the sign-in screen does not say the session expired: %q", tt.Texts())
	}
	if a.login.server != f.URL() || a.login.user != "jackie" {
		t.Errorf("the form is not filled in: %+v", a.login)
	}
	if a.servers[0].Token != "" {
		t.Error("the expired token is kept")
	}
	if _, ok := a.savedSession(); ok {
		t.Error("the expired session is kept")
	}
}

func TestRestoreOfflineThenRetry(t *testing.T) {
	f := newFakeServer(t, "书房")
	f.set(func(f *fakeServer) { f.down = true })
	a := newTestApp(t)
	keepSession(a, f.session())
	a.restore()
	tt := ui.NewTester(a.view, 1280, 820)
	a.waitFor(t, tt, "the offline state", func() bool { return a.conn == connOffline })
	if a.screen != screenMain || !tt.HasText(str.OfflineTitle) {
		t.Fatalf("offline, the app shows %q", tt.Texts())
	}
	if _, ok := a.savedSession(); !ok {
		t.Error("offline, the session was dropped")
	}
	f.set(func(f *fakeServer) { f.down = false })
	if err := tt.Click(str.Retry); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "the server back", func() bool { return a.conn == connOK && a.viewsLoaded })
	settle(tt)
	if tt.HasText(str.OfflineTitle) || !tt.HasText("电影") {
		t.Errorf("back online, the app shows %q", tt.Texts())
	}
}

func TestSwitchServers(t *testing.T) {
	fa := newFakeServer(t, "客厅")
	fb := newFakeServer(t, "书房")
	fb.set(func(f *fakeServer) {
		f.views = []emby.Item{{ID: "b-docs", Name: "纪录片", CollectionType: emby.CollectionMovies}}
	})
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	a.signIn(t, tt, fa)

	// The account menu's Switch Server shows the servers kept; adding one
	// signs in to it.
	a.switchServer()
	settle(tt)
	if !a.sheetOpen || !tt.HasText(str.ChooseServer) || !tt.HasText("客厅") {
		t.Fatalf("the servers do not show: %q", tt.Texts())
	}
	if err := tt.Click(str.AddServer); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if !tt.HasText(str.ServerAddress) {
		t.Fatalf("adding a server shows %q", tt.Texts())
	}
	a.signIn(t, tt, fb)
	if a.sheetOpen || !tt.HasText("纪录片") || tt.HasText("剧集") {
		t.Fatalf("signed in to the second server, the app shows %q", tt.Texts())
	}
	if len(a.servers) != 2 {
		t.Fatalf("kept %d servers", len(a.servers))
	}

	// Back to the first, with its token, without signing in again.
	a.switchServer()
	settle(tt)
	if err := tt.Click("客厅，jackie"); err != nil {
		t.Fatal(err)
	}
	a.waitFor(t, tt, "the first server", func() bool {
		return a.client != nil && a.client.BaseURL() == fa.URL() && a.conn == connOK && a.viewsLoaded
	})
	settle(tt)
	if !tt.HasText("剧集") || tt.HasText("纪录片") {
		t.Errorf("switched back, the app shows %q", tt.Texts())
	}
	if n := len(fa.requests("/Users/AuthenticateByName")); n != 1 {
		t.Errorf("switching back signed in again (%d)", n)
	}
	if fa.logouts+fb.logouts != 0 {
		t.Error("switching signed out of a server")
	}
	if s, _ := a.savedSession(); s.ServerURL != fa.URL() {
		t.Errorf("the session kept is %s's", s.ServerURL)
	}
}

func TestSignOut(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 820)
	if err := tt.Click(str.AccountMenu); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(str.SignOut); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.screen != screenLogin || a.client != nil {
		t.Fatal("signing out left the app")
	}
	a.waitFor(t, tt, "the server to forget the token", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.logouts == 1
	})
	if len(a.servers) != 1 || a.servers[0].Token != "" || a.servers[0].UserName != "jackie" {
		t.Errorf("after signing out, kept %+v", a.servers)
	}
	if _, ok := a.savedSession(); ok {
		t.Error("the session is kept")
	}
	// The form is filled in for the next time.
	if a.login.server != f.URL() || a.login.user != "jackie" || a.login.pass != "" {
		t.Errorf("the form holds %+v", a.login)
	}
}

// An answer that comes after the user moved on is dropped (DESIGN.md §6):
// here, a library's items asked of a server the user switched away from.
func TestAnswersOfAnotherAccountAreDropped(t *testing.T) {
	fa := newFakeServer(t, "客厅")
	fb := newFakeServer(t, "书房")
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 820)
	a.signIn(t, tt, fa)
	fa.set(func(f *fakeServer) { f.hold = true })
	a.navigate(pathLibrary(fa.views[0].ID))
	settle(tt)
	a.waitFor(t, tt, "the request", func() bool { return fa.heldCount() == 1 })

	a.openServers()
	a.login = newLoginState()
	a.signIn(t, tt, fb)
	fa.release()
	a.waitFor(t, tt, "the late answer", func() bool { return a.runPosted() == 0 })
	for key, ls := range a.lists {
		for _, it := range ls.items {
			if it.ID == fa.children[fa.views[0].ID][0].ID {
				t.Errorf("the first server's items landed in %q", key)
			}
		}
	}
	if a.client.BaseURL() != fb.URL() || a.screen != screenMain {
		t.Error("the late answer changed the account")
	}
}

// A window out of sight builds no frames, so what it shows must load
// without them: Home as the user signs in, a page as the user goes there.
// (Loads asked for by the view alone waited, in the screenshot run whose
// window was covered, until it showed again: Home stayed a skeleton.)
func TestLoadsWithoutFrames(t *testing.T) {
	f := newFakeServer(t, "Hidden")
	f.set(func(f *fakeServer) {
		f.latest[f.views[0].ID] = fakeItems("new", 3, emby.TypeMovie)
	})
	a := newTestApp(t)
	a.signIn(t, nil, f) // no tester: no frame is ever built
	a.waitFor(t, nil, "Home", func() bool { return a.home.loaded })
	lib := a.libraries[0]
	a.navigate(pathLibrary(lib.ID))
	a.waitFor(t, nil, "the library", func() bool {
		ls := a.lists[listKey(lib, "", libPrefs{})]
		return ls != nil && ls.loaded
	})
	a.navigate(pathItem("Hidden-m-1"))
	a.waitFor(t, nil, "the item", func() bool {
		d := a.details["Hidden-m-1"]
		return d != nil && d.item != nil
	})
	reloads := len(f.requests("/Users/" + f.userID + "/Items/Hidden-m-1"))
	a.refresh()
	a.waitFor(t, nil, "the item again", func() bool {
		return len(f.requests("/Users/"+f.userID+"/Items/Hidden-m-1")) > reloads && !a.details["Hidden-m-1"].loading
	})
}

// Tokens an older version kept in the settings move to the credential
// store as the app starts, and leave the file.
func TestTokensMoveToTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	old := `{"themeMode":"system","deviceId":"dev1",
		"servers":[{"serverUrl":"https://a.example","serverName":"客厅","userId":"u1","userName":"jackie","token":"tok-a","lastUsed":"2026-11-11T10:00:00Z"},
		           {"serverUrl":"https://b.example","userId":"u2","userName":"guest","token":"","lastUsed":"2026-11-10T10:00:00Z"}],
		"session":{"serverUrl":"https://a.example","serverName":"客厅","userId":"u1","userName":"jackie","token":"tok-a"}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := newMemSecrets()
	a := newAppWith(path, "", secrets)
	defer a.img.Close()
	if err := a.store.Flush(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "tok-a") || !strings.Contains(string(data), "tokenRef") {
		t.Errorf("the settings after moving the tokens:\n%s", data)
	}
	if v, _ := secrets.Get(tokenRef(emby.Session{ServerURL: "https://a.example", UserID: "u1"})); string(v) != "tok-a" {
		t.Errorf("the store holds %q", v)
	}
	if len(a.servers) != 2 || !a.servers[0].signedIn() || a.servers[1].signedIn() {
		t.Errorf("servers %+v", a.servers)
	}
	// A later run reads it from the store.
	b := newAppWith(path, "", secrets)
	defer b.img.Close()
	s, ok := b.savedSession()
	if !ok || s.Token != "tok-a" || s.ServerName != "客厅" {
		t.Errorf("a later run restores %+v, %v", s, ok)
	}
	// Signing out takes it out of the store.
	cl, _ := b.restoreClient(s)
	b.useClient(cl, connOK)
	b.client.SetSession(emby.Session{ServerURL: cl.BaseURL()}) // no request to make
	b.forgetToken(s)
	if _, err := secrets.Get(tokenRef(s)); err == nil {
		t.Error("signed out, the store keeps the token")
	}
}
