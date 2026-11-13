package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Signing in and out, and the servers kept.
//
// The app talks to one server at a time, through a.client, nil while no
// one is signed in. Every account signed in to is kept in a.servers, with
// its token until the user signs out of it, so that the user can switch
// between them; the one in use is kept in Settings.Session too, and comes
// back as the app starts, checked against the server in the background.
//
// What is loaded for an account belongs to it: changing a.client drops it
// all, and a load's answer only lands if a.client is still the client it
// was asked of (see load).

const appVersion = "0.1.0"

// screen is what the window shows: the sign-in screen, or the app.
type screen int

const (
	screenLogin screen = iota
	screenMain
)

// connState is how the app last found the server of the account in use.
type connState int

const (
	connOK       connState = iota
	connChecking           // a restored session is being checked
	connOffline            // the server did not answer: the content says so
)

// Timeouts of the requests the user waits on.
const (
	probeTimeout = 15 * time.Second
	loginTimeout = 30 * time.Second
	checkTimeout = 20 * time.Second
	loadTimeout  = 45 * time.Second
)

// savedServer is an account of a server the app keeps, to sign in again or
// switch to: the session of its last sign-in. Its token is kept in the
// system's credential store (the Keychain), under TokenRef, and in Token
// once read; both are empty once the user signed out of it.
type savedServer struct {
	emby.Session
	TokenRef string
	LastUsed time.Time
}

// signedIn reports whether the app holds a token for the account.
func (s savedServer) signedIn() bool { return s.Token != "" || s.TokenRef != "" }

// storedServer is a savedServer as the settings file keeps it: no token,
// only where the credential store keeps it. Token is the old format's,
// read once and moved to the store; it is written only where the system
// has no store.
type storedServer struct {
	ServerURL     string    `json:"serverUrl"`
	ServerID      string    `json:"serverId,omitempty"`
	ServerName    string    `json:"serverName,omitempty"`
	ServerVersion string    `json:"serverVersion,omitempty"`
	ServerKind    string    `json:"serverKind,omitempty"`
	UserID        string    `json:"userId"`
	UserName      string    `json:"userName"`
	UserImageTag  string    `json:"userImageTag,omitempty"`
	TokenRef      string    `json:"tokenRef,omitempty"`
	Token         string    `json:"token,omitempty"`
	LastUsed      time.Time `json:"lastUsed,omitzero"`
}

func (st storedServer) saved() savedServer {
	return savedServer{
		Session: emby.Session{ServerURL: st.ServerURL, ServerID: st.ServerID, ServerName: st.ServerName,
			ServerVersion: st.ServerVersion, ServerKind: st.ServerKind, UserID: st.UserID, UserName: st.UserName,
			UserImageTag: st.UserImageTag, Token: st.Token},
		TokenRef: st.TokenRef,
		LastUsed: st.LastUsed,
	}
}

func (s savedServer) stored(withToken bool) storedServer {
	st := storedServer{ServerURL: s.ServerURL, ServerID: s.ServerID, ServerName: s.ServerName,
		ServerVersion: s.ServerVersion, ServerKind: s.ServerKind, UserID: s.UserID, UserName: s.UserName,
		UserImageTag: s.UserImageTag, TokenRef: s.TokenRef, LastUsed: s.LastUsed}
	if withToken {
		st.Token = s.Token
	}
	return st
}

// tokenRef is the key the credential store keeps an account's token under.
func tokenRef(s emby.Session) string {
	return "token " + s.ServerURL + " " + s.UserID
}

// secretStore keeps tokens: the system's credential store (mygo.Secrets),
// or, in tests and the screenshot run, one in memory.
type secretStore interface {
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
	Delete(key string) error
}

// memSecrets is a secretStore in memory.
type memSecrets struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemSecrets() *memSecrets { return &memSecrets{m: map[string][]byte{}} }

func (s *memSecrets) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return nil, mygo.ErrSecretNotFound
	}
	return v, nil
}

func (s *memSecrets) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}

func (s *memSecrets) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}

// storeToken keeps the token of the account of s in the credential store,
// and returns its reference, or "" where the store failed (the token is
// then kept in the settings file, as on a system without a store).
func (a *app) storeToken(s emby.Session) string {
	if s.Token == "" {
		return ""
	}
	ref := tokenRef(s)
	if err := a.secrets.Set(ref, []byte(s.Token)); err != nil {
		log.Printf("keeping a token: %v", err)
		a.plainTokens = true
		return ""
	}
	return ref
}

// tokenOf returns the token of a saved account, reading it from the
// credential store unless it was read already.
func (a *app) tokenOf(s *savedServer) string {
	if s.Token == "" && s.TokenRef != "" {
		v, err := a.secrets.Get(s.TokenRef)
		switch {
		case err == nil:
			s.Token = string(v)
		case errors.Is(err, mygo.ErrSecretNotFound):
			s.TokenRef = "" // gone from the store: signed out
		default:
			log.Printf("reading a token: %v", err)
		}
	}
	return s.Token
}

// sameAccount reports whether two sessions are one account on one server:
// the same server, by its address or its ID, and the same user, by ID or,
// where one has none, by name.
func sameAccount(x, y emby.Session) bool {
	sameServer := x.ServerURL == y.ServerURL || (x.ServerID != "" && x.ServerID == y.ServerID)
	if !sameServer {
		return false
	}
	if x.UserID != "" && y.UserID != "" {
		return x.UserID == y.UserID
	}
	return strings.EqualFold(x.UserName, y.UserName)
}

// serverTitle is how the app names a server: its own name, else its host.
func serverTitle(s emby.Session) string {
	if s.ServerName != "" {
		return s.ServerName
	}
	if u, err := url.Parse(s.ServerURL); err == nil && u.Host != "" {
		return u.Host
	}
	return s.ServerURL
}

// serverHost is the address of a server as the user reads it: its host and
// path, without the scheme.
func serverHost(s emby.Session) string {
	h := s.ServerURL
	for _, p := range []string{"https://", "http://"} {
		h = strings.TrimPrefix(h, p)
	}
	return h
}

// kindLabel names the software of a server: Emby or Jellyfin.
func kindLabel(kind string) string {
	if kind == emby.ServerKindJellyfin {
		return str.KindJellyfin
	}
	return str.KindEmby
}

// deviceInfo is how the app introduces itself to servers.
func deviceInfo(id string) emby.DeviceInfo {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "Mac"
	}
	name = strings.TrimSuffix(name, ".local")
	return emby.DeviceInfo{Client: "MyGo Emby", Device: name, DeviceID: id, Version: appVersion}
}

// loadAccounts reads the servers kept in the settings, making the device's
// ID on the first run. Tokens the settings hold, as an older version kept
// them, move to the credential store.
func (a *app) loadAccounts() {
	if a.settings.DeviceID == "" {
		a.settings.DeviceID = emby.NewDeviceID()
		a.saveSettings()
	}
	a.device = deviceInfo(a.settings.DeviceID)
	var stored []storedServer
	if len(a.settings.Servers) > 0 {
		if err := json.Unmarshal(a.settings.Servers, &stored); err != nil {
			log.Printf("reading the servers kept: %v", err)
		}
	}
	a.servers = nil
	migrate := false
	for _, st := range stored {
		if st.ServerURL == "" {
			continue
		}
		s := st.saved()
		if s.Token != "" {
			migrate = true
			s.TokenRef = a.storeToken(s.Session)
		}
		a.servers = append(a.servers, s)
	}
	// The old format kept the session in use, token and all, apart.
	if cur, ok := a.storedSession(); ok && cur.Token != "" {
		migrate = true
		s := cur.saved()
		if i := a.indexOf(s.Session); i >= 0 {
			if a.servers[i].Token == "" {
				a.servers[i].Token = s.Token
				a.servers[i].TokenRef = a.storeToken(s.Session)
			}
		} else {
			s.TokenRef = a.storeToken(s.Session)
			a.servers = append(a.servers, s)
		}
	}
	if migrate {
		cur, _ := a.storedSession()
		a.persistServers(cur.saved().Session)
	}
}

// storedSession returns the account in use as the app last ran, as the
// settings keep it.
func (a *app) storedSession() (storedServer, bool) {
	var st storedServer
	if len(a.settings.Session) == 0 || json.Unmarshal(a.settings.Session, &st) != nil || st.ServerURL == "" {
		return storedServer{}, false
	}
	return st, true
}

// indexOf returns where the account of s is among the servers kept, or -1.
func (a *app) indexOf(s emby.Session) int {
	for i := range a.servers {
		if sameAccount(a.servers[i].Session, s) {
			return i
		}
	}
	return -1
}

// savedSession returns the session of the account in use as the app last
// ran, its token read from the credential store, if it has one.
func (a *app) savedSession() (emby.Session, bool) {
	st, ok := a.storedSession()
	if !ok {
		return emby.Session{}, false
	}
	s := st.saved()
	if i := a.indexOf(s.Session); i >= 0 {
		s = a.servers[i]
	}
	s.Token = a.tokenOf(&s)
	if i := a.indexOf(s.Session); i >= 0 {
		a.servers[i].Token, a.servers[i].TokenRef = s.Token, s.TokenRef
	}
	return s.Session, s.Session.Valid()
}

// persistAccounts writes the servers kept and the account in use to the
// settings, most recently used first: no tokens, only their references.
func (a *app) persistAccounts() {
	var cur emby.Session
	if a.client != nil {
		cur = a.client.Session()
	}
	a.persistServers(cur)
}

func (a *app) persistServers(cur emby.Session) {
	slices.SortStableFunc(a.servers, func(x, y savedServer) int { return y.LastUsed.Compare(x.LastUsed) })
	stored := make([]storedServer, 0, len(a.servers))
	for _, s := range a.servers {
		stored = append(stored, s.stored(a.plainTokens && s.TokenRef == ""))
	}
	if data, err := json.Marshal(stored); err == nil {
		a.settings.Servers = data
	}
	a.settings.Session = nil
	if cur.ServerURL != "" {
		if i := a.indexOf(cur); i >= 0 {
			if data, err := json.Marshal(a.servers[i].stored(false)); err == nil {
				a.settings.Session = data
			}
		}
	}
	a.saveSettings()
}

// remember keeps s among the servers, in place of the account's older
// session, as the one used last, its token in the credential store.
func (a *app) remember(s emby.Session) {
	entry := savedServer{Session: s, LastUsed: time.Now()}
	i := a.indexOf(s)
	if i >= 0 && a.servers[i].Token == s.Token && a.servers[i].TokenRef != "" {
		entry.TokenRef = a.servers[i].TokenRef // kept already
	} else {
		entry.TokenRef = a.storeToken(s)
	}
	if i >= 0 {
		a.servers[i] = entry
		return
	}
	a.servers = append(a.servers, entry)
}

// forgetToken keeps the account of s, signed out: its token leaves the
// credential store.
func (a *app) forgetToken(s emby.Session) {
	for i := range a.servers {
		if sameAccount(a.servers[i].Session, s) {
			a.dropToken(&a.servers[i])
		}
	}
}

func (a *app) dropToken(s *savedServer) {
	if s.TokenRef != "" {
		if err := a.secrets.Delete(s.TokenRef); err != nil && !errors.Is(err, mygo.ErrSecretNotFound) {
			log.Printf("forgetting a token: %v", err)
		}
	}
	s.Token, s.TokenRef = "", ""
}

// isCurrent reports whether s is the account in use.
func (a *app) isCurrent(s emby.Session) bool {
	return a.client != nil && sameAccount(a.client.Session(), s)
}

// newClient makes a client of the server at url, signed out.
func (a *app) newClient(serverURL string) (*emby.Client, error) {
	return emby.NewClient(serverURL, a.device, a.clientOpts...)
}

// restoreClient makes a client signed in with s.
func (a *app) restoreClient(s emby.Session) (*emby.Client, error) {
	return emby.RestoreClient(s, a.device, a.clientOpts...)
}

// restore signs in again as the app starts, with the session it last used:
// the app shows at once, from what the session says, while the server is
// asked whether the session still holds. Without one, it asks the user to
// sign in.
func (a *app) restore() {
	s, ok := a.savedSession()
	if !ok {
		a.showLogin(emby.Session{}, "")
		return
	}
	cl, err := a.restoreClient(s)
	if err != nil {
		a.showLogin(s, "")
		return
	}
	a.useClient(cl, connChecking)
	a.loadViews()
	a.checkSession()
}

// useClient makes cl the client of the app and shows the app: what was
// loaded for another account goes, and so does the history of pages.
func (a *app) useClient(cl *emby.Client, conn connState) {
	a.client = cl
	a.conn = conn
	a.connErr = nil
	a.resetData()
	a.screen = screenMain
	a.sheetOpen = false
	a.accountOpen = false
	a.prefetch(pathHome)
}

// clearClient forgets the client, as the user signs out.
func (a *app) clearClient() {
	a.client = nil
	a.conn = connOK
	a.connErr = nil
	a.resetData()
	a.sheetOpen = false
	a.accountOpen = false
}

// resetData drops what was loaded for an account, and the pages' history.
func (a *app) resetData() {
	a.epoch++
	// The player plays for the account it was made with, and the pages
	// of the history go with it.
	a.closePlayer(0)
	a.pageMems, a.pageMemOrder = nil, nil
	a.libraries = nil
	a.viewsLoaded = false
	a.viewsErr = nil
	a.viewsGen.next()
	a.home = &homeState{}
	a.lists = map[string]*listState{}
	a.details = map[string]*detailState{}
	a.people = map[string]*personState{}
	a.favs = &favState{}
	a.found = &searchState{sel: -1}
	a.form = settingsForm{}
	a.router = ui.NewRouter(pathHome)
	a.scrollReq = nil
	a.search = ""
}

// checkSession asks the server who the session's user is: a token the
// server no longer takes sends the user to sign in again; a server that
// does not answer leaves the app as it is, saying so.
func (a *app) checkSession() {
	cl := a.client
	if cl == nil {
		return
	}
	a.conn = connChecking
	n := a.connGen.next()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		_, err := cl.CurrentUser(ctx)
		a.update(func() {
			if a.client != cl || !a.connGen.is(n) {
				return // signed out or switched meanwhile
			}
			switch {
			case err == nil:
				a.conn, a.connErr = connOK, nil
				a.remember(cl.Session()) // the user's name or picture may have changed
				a.persistAccounts()
				if !a.viewsLoaded {
					a.loadViews()
				}
			case errors.Is(err, emby.ErrUnauthorized):
				a.expire()
			default:
				a.conn, a.connErr = connOffline, err
			}
		})
	}()
}

// reconnect tries the server again, from the offline state.
func (a *app) reconnect() {
	a.checkSession()
	a.refresh()
}

// expire sends the user to sign in again, the server having refused the
// session's token.
func (a *app) expire() {
	if a.client == nil {
		return
	}
	s := a.client.Session()
	a.forgetToken(s)
	a.clearClient()
	a.persistAccounts()
	a.showLogin(s, str.SessionExpired)
}

// signOut signs out of the account in use: the server forgets the token,
// in the background, and the app forgets it at once.
func (a *app) signOut() {
	cl := a.client
	if cl == nil {
		return
	}
	s := cl.Session()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cl.Logout(ctx); err != nil {
			log.Printf("signing out of %s: %v", serverHost(s), err)
		}
	}()
	a.forgetToken(s)
	a.clearClient()
	a.persistAccounts()
	a.showLogin(s, "")
	if len(a.servers) > 1 {
		a.login.mode = loginServers
	}
}

// showLogin shows the sign-in screen, filled in with the server and the
// user of s, if any, saying notice above the fields.
func (a *app) showLogin(s emby.Session, notice string) {
	a.screen = screenLogin
	a.login = newLoginState()
	a.login.server = s.ServerURL
	a.login.user = s.UserName
	a.login.notice = notice
	if s.ServerURL == "" && len(a.servers) > 0 {
		a.login.mode = loginServers
	}
}

// openServers shows the servers kept, over the app, to switch to another.
func (a *app) openServers() {
	a.login = newLoginState()
	a.login.mode = loginServers
	a.sheetOpen = true
	a.accountOpen = false
}

// switchTo uses the account kept as s: at once with its token, checked in
// the background; without one, the user signs in to it again.
func (a *app) switchTo(s savedServer) {
	if a.isCurrent(s.Session) && s.signedIn() {
		a.sheetOpen = false
		return
	}
	if i := a.indexOf(s.Session); i >= 0 {
		s.Token = a.tokenOf(&a.servers[i])
		s.TokenRef = a.servers[i].TokenRef
	}
	if s.Token == "" {
		a.login = newLoginState()
		a.login.server, a.login.user = s.ServerURL, s.UserName
		return
	}
	cl, err := a.restoreClient(s.Session)
	if err != nil {
		a.login.err = a.errText(err)
		return
	}
	a.remember(s.Session)
	a.useClient(cl, connChecking)
	a.persistAccounts()
	a.loadViews()
	a.checkSession()
}

// removeServer forgets an account kept, signing out of it on its server.
func (a *app) removeServer(s savedServer) {
	if a.isCurrent(s.Session) {
		return
	}
	i := a.indexOf(s.Session)
	if i < 0 {
		return
	}
	s = a.servers[i]
	token := a.tokenOf(&s)
	a.dropToken(&s)
	a.servers = slices.Delete(a.servers, i, i+1)
	a.persistAccounts()
	if token == "" {
		return
	}
	s.Token = token
	if cl, err := a.restoreClient(s.Session); err == nil {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = cl.Logout(ctx)
		}()
	}
}

// load runs fetch off the main thread with the client in use, and hands
// its result to done on the main thread, unless the user signed out or
// switched accounts meanwhile. A token the server refuses sends the user to
// sign in again rather than to done.
func load[T any](a *app, fetch func(ctx context.Context, cl *emby.Client) (T, error), done func(T, error)) {
	cl := a.client
	if cl == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), loadTimeout)
		defer cancel()
		v, err := fetch(ctx, cl)
		a.update(func() {
			if a.client != cl {
				return
			}
			if errors.Is(err, emby.ErrUnauthorized) {
				a.expire()
				return
			}
			done(v, err)
		})
	}()
}

// loadViews loads the account's libraries, for the sidebar.
func (a *app) loadViews() {
	n := a.viewsGen.next()
	load(a, func(ctx context.Context, cl *emby.Client) ([]emby.Item, error) {
		return cl.Views(ctx)
	}, func(views []emby.Item, err error) {
		if !a.viewsGen.is(n) {
			return
		}
		if err != nil {
			a.viewsErr = err
			return
		}
		a.setLibraries(views)
		if a.conn == connOffline {
			a.conn, a.connErr = connOK, nil // the server answers again
		}
	})
}

// setLibraries takes the account's libraries from its views.
func (a *app) setLibraries(views []emby.Item) {
	libs := make([]library, 0, len(views))
	for _, v := range views {
		libs = append(libs, library{ID: v.ID, Name: v.Name, Kind: v.CollectionType})
	}
	a.libraries = libs
	a.viewsLoaded = true
	a.viewsErr = nil
	a.prefetch(a.router.Location()) // a library's page waited for them
}

// libraryByID returns the library with the ID id, if the account has it.
func (a *app) libraryByID(id string) (library, bool) {
	for _, l := range a.libraries {
		if l.ID == id {
			return l, true
		}
	}
	return library{}, false
}

// errText says what went wrong with a request, for the user.
func (a *app) errText(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return str.ErrTimeout
	case errors.Is(err, emby.ErrInvalidURL):
		return str.ErrInvalidURL
	case errors.Is(err, emby.ErrUnreachable):
		return str.ErrUnreachable
	case errors.Is(err, emby.ErrUnauthorized):
		return str.ErrBadCredentials
	case errors.Is(err, emby.ErrForbidden):
		return str.ErrForbidden
	case errors.Is(err, emby.ErrNotFound):
		return str.ErrNotFound
	case errors.Is(err, emby.ErrBadResponse):
		return str.ErrNotServer
	case errors.Is(err, emby.ErrHTTP):
		return fmt.Sprintf(str.ErrServerFmt, emby.StatusCode(err))
	}
	return str.ErrUnknown
}
