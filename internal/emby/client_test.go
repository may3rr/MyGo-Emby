package emby

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

var testDevice = DeviceInfo{Client: "MyGo Emby", Device: "Test Mac", DeviceID: "dev-123", Version: "0.1.0"}

func TestNormalizeServerURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"demo.jellyfin.org/stable", "http://demo.jellyfin.org/stable"},
		{"https://demo.jellyfin.org/stable/", "https://demo.jellyfin.org/stable"},
		{"  HTTPS://Emby.Example.COM:8920  ", "https://emby.example.com:8920"},
		{"192.168.1.5:8096", "http://192.168.1.5:8096"},
		{"http://host:8096/web/index.html", "http://host:8096"},
		{"http://host:8096/web/index.html#!/home.html", "http://host:8096"},
		{"https://host/emby/web/index.html#!/home", "https://host/emby"},
		{"https://host/jellyfin/web/#/home.html", "https://host/jellyfin"},
		{"https://host/jellyfin/web", "https://host/jellyfin"},
		{"https://host/emby", "https://host/emby"},
		{"https://host///", "https://host"},
		{"http://user:pw@host:8096/?x=1", "http://host:8096"},
		{"[::1]:8096", "http://[::1]:8096"},
	}
	for _, c := range cases {
		got, err := NormalizeServerURL(c.in)
		if err != nil {
			t.Errorf("NormalizeServerURL(%q) error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizeServerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"", "   ", "ftp://host", "http://", "http://:8096"} {
		if _, err := NormalizeServerURL(bad); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("NormalizeServerURL(%q) err = %v, want ErrInvalidURL", bad, err)
		}
	}
}

func TestAuthHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"Items":[],"TotalRecordCount":0}`)
	}))
	defer srv.Close()

	c, err := RestoreClient(Session{ServerURL: srv.URL, UserID: "u1", Token: "tok"}, DeviceInfo{
		Client: "MyGo Emby", Device: `Jackie的"Mac", 1`, DeviceID: "dev-123", Version: "0.1.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Views(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := `MediaBrowser Client="MyGo Emby", Device="Jackie%E7%9A%84%22Mac%22%2C 1", DeviceId="dev-123", Version="0.1.0", Token="tok"`
	if a := got.Get("Authorization"); a != want {
		t.Errorf("Authorization = %q\nwant %q", a, want)
	}
	if a := got.Get("X-Emby-Authorization"); a != want {
		t.Errorf("X-Emby-Authorization = %q", a)
	}
	if a := got.Get("X-Emby-Token"); a != "tok" {
		t.Errorf("X-Emby-Token = %q", a)
	}
	// Without a token there must be no Token part and no X-Emby-Token.
	anon, _ := NewClient(srv.URL, testDevice)
	if _, err := anon.PublicInfo(context.Background()); err == nil || !errors.Is(err, ErrBadResponse) {
		// The handler answers a QueryResult, which has no server id.
		t.Errorf("PublicInfo on non-server JSON: err = %v, want ErrBadResponse", err)
	}
	if strings.Contains(got.Get("Authorization"), "Token=") || got.Get("X-Emby-Token") != "" {
		t.Errorf("anonymous request carried a token: %v", got)
	}
}

func TestErrorMapping(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/s401", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		io.WriteString(w, `{"ErrorCode":"AUTH_FAILED","Message":"username or password is invalid"}`)
	})
	mux.HandleFunc("/s403", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "User account has been disabled.", 403)
	})
	mux.HandleFunc("/s404", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/s500", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(500)
		io.WriteString(w, "<html><body>boom</body></html>")
	})
	mux.HandleFunc("/html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<!doctype html><title>router</title>")
	})
	mux.HandleFunc("/problem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(400)
		io.WriteString(w, `{"title":"One or more validation errors occurred."}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c, _ := NewClient(srv.URL, testDevice)
	ctx := context.Background()

	cases := []struct {
		path   string
		kind   ErrorKind
		target error
		status int
		msg    string
	}{
		{"/s401", KindUnauthorized, ErrUnauthorized, 401, "username or password is invalid"},
		{"/s403", KindForbidden, ErrForbidden, 403, "User account has been disabled."},
		{"/s404", KindNotFound, ErrNotFound, 404, ""},
		{"/s500", KindHTTP, ErrHTTP, 500, ""},
		{"/html", KindBadResponse, ErrBadResponse, 200, "server answered with an HTML page, not the media server API"},
		{"/problem", KindHTTP, ErrHTTP, 400, "One or more validation errors occurred."},
	}
	for _, tc := range cases {
		var out map[string]any
		err := c.do(ctx, http.MethodGet, tc.path, nil, nil, &out)
		if !errors.Is(err, tc.target) || KindOf(err) != tc.kind || StatusCode(err) != tc.status {
			t.Errorf("%s: err = %v (kind %v, status %d), want kind %v status %d", tc.path, err, KindOf(err), StatusCode(err), tc.kind, tc.status)
			continue
		}
		var e *Error
		errors.As(err, &e)
		if e.Message != tc.msg {
			t.Errorf("%s: message %q, want %q", tc.path, e.Message, tc.msg)
		}
	}

	// Unreachable: a closed server.
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	dc, _ := NewClient(deadURL, testDevice)
	if _, err := dc.PublicInfo(ctx); !errors.Is(err, ErrUnreachable) {
		t.Errorf("closed server: err = %v, want ErrUnreachable", err)
	}

	// Cancelled context is not reported as unreachable.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	sc, _ := NewClient(slow.URL, testDevice)
	cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := sc.PublicInfo(cctx); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrUnreachable) {
		t.Errorf("timeout: err = %v, want context.DeadlineExceeded", err)
	}

	// Methods needing a user fail fast without one.
	if _, err := c.Views(ctx); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Views without session: %v", err)
	}
}

func TestLoginAndLogout(t *testing.T) {
	var loginBody map[string]string
	var loginAuth string
	loggedOut := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /jf/System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ServerName":"Stable Demo","Version":"12.4.0","ProductName":"Jellyfin Server","Id":"srv1"}`)
	})
	mux.HandleFunc("POST /jf/Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		loginAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&loginBody)
		if loginBody["Pw"] != "secret" {
			w.WriteHeader(401)
			io.WriteString(w, "Error processing request.")
			return
		}
		io.WriteString(w, `{"User":{"Name":"demo","Id":"u1","ServerId":"srv1","PrimaryImageTag":"img1"},"AccessToken":"tok1","ServerId":"srv1"}`)
	})
	mux.HandleFunc("POST /jf/Sessions/Logout", func(w http.ResponseWriter, r *http.Request) {
		loggedOut = r.Header.Get("X-Emby-Token") == "tok1"
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := NewClient(srv.URL+"/jf/web/index.html", testDevice)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.Login(ctx, "demo", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong password: err = %v, want ErrUnauthorized", err)
	}
	s, err := c.Login(ctx, "demo", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(loginAuth, "Token=") {
		t.Errorf("login request carried a token: %s", loginAuth)
	}
	want := Session{ServerURL: srv.URL + "/jf", ServerID: "srv1", ServerName: "Stable Demo", ServerVersion: "12.4.0",
		ServerKind: ServerKindJellyfin, UserID: "u1", UserName: "demo", UserImageTag: "img1", Token: "tok1"}
	if s != want {
		t.Errorf("session = %+v\nwant %+v", s, want)
	}
	if !c.LoggedIn() || !c.IsJellyfin() {
		t.Error("client should be logged in to Jellyfin")
	}
	// Session round-trips through JSON and restores.
	b, _ := json.Marshal(s)
	var s2 Session
	json.Unmarshal(b, &s2)
	r, err := RestoreClient(s2, testDevice)
	if err != nil || r.Session() != want {
		t.Errorf("restored session = %+v, %v", r.Session(), err)
	}
	// A failed re-login keeps the current session.
	if _, err := c.Login(ctx, "demo", "wrong"); !errors.Is(err, ErrUnauthorized) || c.Token() != "tok1" {
		t.Errorf("failed re-login: err %v, token %q", err, c.Token())
	}
	if strings.Contains(loginAuth, "Token=") {
		t.Errorf("re-login request carried the old token: %s", loginAuth)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if !loggedOut || c.LoggedIn() {
		t.Errorf("logout: server saw token %v, client still logged in %v", loggedOut, c.LoggedIn())
	}
}

func TestItemsQueryParameters(t *testing.T) {
	var gotPath string
	var gotQuery map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		io.WriteString(w, `{"Items":[{"Id":"a","Name":"A","Type":"Movie","UserData":{"Played":true}}],"TotalRecordCount":41}`)
	}))
	defer srv.Close()
	c, _ := RestoreClient(Session{ServerURL: srv.URL, UserID: "u 1", Token: "t"}, testDevice)
	res, err := c.Items(context.Background(), ItemQuery{
		ParentID: "p1", IncludeItemTypes: []string{TypeMovie, TypeSeries}, Recursive: true,
		SortBy: []string{SortDateCreated, SortName}, SortOrder: Descending,
		Filters: []string{FilterIsUnplayed}, StartIndex: 40, Limit: 20, SearchTerm: "lla ma",
		PersonIDs: []string{"x"}, Fields: []string{FieldPeople},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/Users/u 1/Items" {
		t.Errorf("path %q", gotPath)
	}
	check := map[string]string{
		"ParentId": "p1", "IncludeItemTypes": "Movie,Series", "Recursive": "true",
		"SortBy": "DateCreated,SortName", "SortOrder": "Descending", "Filters": "IsUnplayed",
		"StartIndex": "40", "Limit": "20", "SearchTerm": "lla ma", "PersonIds": "x",
		"EnableTotalRecordCount": "true",
	}
	for k, v := range check {
		if got := strings.Join(gotQuery[k], ","); got != v {
			t.Errorf("query %s = %q, want %q", k, got, v)
		}
	}
	if f := gotQuery["Fields"][0]; !strings.Contains(f, "People") || !strings.Contains(f, "Overview") {
		t.Errorf("Fields = %q", f)
	}
	if res.TotalRecordCount != 41 || res.StartIndex != 40 || len(res.Items) != 1 || !res.Items[0].IsPlayed() {
		t.Errorf("result = %+v", res)
	}
}

func TestCredentialOrigin(t *testing.T) {
	const token = "private-session-token"
	authHeaders := []string{"Authorization", "X-Emby-Authorization", "X-Emby-Token"}
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range authHeaders {
			if r.Header.Get(name) != "" {
				t.Errorf("external request leaked %s", name)
			}
		}
		io.WriteString(w, "WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n")
	}))
	defer external.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != token {
			t.Error("same-origin request lost authentication")
		}
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/subtitle", http.StatusFound)
		case "/external":
			http.Redirect(w, r, external.URL+"/subtitle", http.StatusFound)
		default:
			io.WriteString(w, "WEBVTT\n\n00:01.000 --> 00:02.000\nhi\n")
		}
	}))
	defer origin.Close()
	c, err := RestoreClient(Session{ServerURL: origin.URL, UserID: "u", Token: token}, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	for _, rawURL := range []string{external.URL, origin.URL + "/same", origin.URL + "/external"} {
		if _, err := c.FetchSubtitles(context.Background(), rawURL); err != nil {
			t.Fatal(err)
		}
	}
	// API requests use the same redirect policy as subtitle downloads.
	if err := c.do(context.Background(), http.MethodGet, "/external", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTransportErrorRedactsURL(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	c, err := NewClient(dead.URL, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.FetchSubtitles(context.Background(), dead.URL+"/subtitle?api_key=private-token&ApiKey=private-token")
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("expected transport error, got %v", err)
	}
	if strings.Contains(err.Error(), "private-token") {
		t.Error("transport error text exposes the token")
	}
	var transportErr *url.Error
	if !errors.As(err, &transportErr) {
		t.Error("underlying transport error must remain available")
	}
}

func TestDateParsing(t *testing.T) {
	var v struct {
		A, B, C, D, E, F Date
	}
	err := json.Unmarshal([]byte(`{"A":"2018-09-01T00:00:00.0000000Z","B":"2026-11-11T06:04:27.6974153Z",
		"C":"2020-05-06T07:08:09","D":null,"E":"","F":"0001-01-01T00:00:00.0000000Z"}`), &v)
	if err != nil {
		t.Fatal(err)
	}
	if v.A.Year() != 2018 || v.A.Month() != 9 || v.B.Nanosecond() != 697415300 || v.C.Hour() != 7 {
		t.Errorf("parsed %v %v %v", v.A, v.B, v.C)
	}
	if !v.D.IsZero() || !v.E.IsZero() || !v.F.IsZero() {
		t.Errorf("zero dates: %v %v %v", v.D, v.E, v.F)
	}
	b, _ := json.Marshal(v.A)
	if string(b) != `"2018-09-01T00:00:00Z"` {
		t.Errorf("marshal = %s", b)
	}
}

func TestRedactURLAndDirectStreamURL(t *testing.T) {
	raw := "https://user:secret@emby.example.com:8096/Videos/v1/stream.mkv?Static=true&MediaSourceId=ms1&api_key=tok123&Token=tok456"
	got := RedactURL(raw)
	for _, secret := range []string{"secret", "tok123", "tok456"} {
		if strings.Contains(got, secret) {
			t.Errorf("RedactURL leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "Static=true") || !strings.Contains(got, "MediaSourceId=ms1") || !strings.Contains(got, "REDACTED") {
		t.Errorf("RedactURL = %q", got)
	}
	if bad := RedactURL("://bad/%zz?api_key=leak"); strings.Contains(bad, "leak") {
		t.Errorf("malformed RedactURL leaked token: %q", bad)
	}

	c, err := RestoreClient(Session{ServerURL: "https://emby.example.com", UserID: "u1", Token: "secret-tk"}, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	if u := c.DirectStreamURL("item1", nil); !strings.Contains(u, "/Videos/item1/stream") || !strings.Contains(u, "MediaSourceId=item1") || !strings.Contains(u, "Static=true") {
		t.Errorf("DirectStreamURL(nil) = %q", u)
	}
	src := &MediaSource{ID: "ms9", Container: "mkv,webm"}
	if u := c.DirectStreamURL("item1", src); !strings.Contains(u, "/Videos/item1/stream.mkv") || !strings.Contains(u, "MediaSourceId=ms9") || !strings.Contains(u, "api_key=secret-tk") {
		t.Errorf("DirectStreamURL(src) = %q", u)
	}
}
