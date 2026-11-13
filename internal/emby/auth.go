package emby

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Server kinds recorded in Session.ServerKind.
const (
	ServerKindEmby     = "emby"
	ServerKindJellyfin = "jellyfin"
)

// PublicInfo is the anonymous answer of GET /System/Info/Public.
type PublicInfo struct {
	ID                     string `json:"Id"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"` // "Jellyfin Server"; often absent on Emby
	OperatingSystem        string `json:"OperatingSystem"`
	LocalAddress           string `json:"LocalAddress"`
	StartupWizardCompleted *bool  `json:"StartupWizardCompleted"`
}

// Kind returns ServerKindJellyfin or ServerKindEmby (the default when the
// server does not say).
func (p *PublicInfo) Kind() string {
	if strings.Contains(strings.ToLower(p.ProductName), "jellyfin") {
		return ServerKindJellyfin
	}
	return ServerKindEmby
}

// Product returns a display name such as "Jellyfin Server" or "Emby Server".
func (p *PublicInfo) Product() string {
	if p.ProductName != "" {
		return p.ProductName
	}
	return "Emby Server"
}

// PublicInfo probes the server without authentication. Use it to validate a
// typed address before asking for credentials: ErrUnreachable means nothing
// answered, ErrBadResponse/ErrNotFound mean something answered that is not
// Emby or Jellyfin.
func (c *Client) PublicInfo(ctx context.Context) (*PublicInfo, error) {
	var info PublicInfo
	if err := c.do(ctx, http.MethodGet, "/System/Info/Public", nil, nil, &info); err != nil {
		return nil, err
	}
	if info.ID == "" && info.ServerName == "" && info.Version == "" {
		return nil, &Error{Kind: KindBadResponse, Op: "GET /System/Info/Public",
			Message: "server did not identify itself as Emby or Jellyfin"}
	}
	return &info, nil
}

// User is the subset of UserDto the client uses.
type User struct {
	ID                    string            `json:"Id"`
	Name                  string            `json:"Name"`
	ServerID              string            `json:"ServerId"`
	PrimaryImageTag       string            `json:"PrimaryImageTag"`
	HasPassword           bool              `json:"HasPassword"`
	HasConfiguredPassword bool              `json:"HasConfiguredPassword"`
	LastLoginDate         Date              `json:"LastLoginDate"`
	LastActivityDate      Date              `json:"LastActivityDate"`
	Configuration         UserConfiguration `json:"Configuration"`
	Policy                UserPolicy        `json:"Policy"`
}

// UserConfiguration holds the user's playback preferences on the server.
type UserConfiguration struct {
	AudioLanguagePreference    string `json:"AudioLanguagePreference"`
	SubtitleLanguagePreference string `json:"SubtitleLanguagePreference"`
	SubtitleMode               string `json:"SubtitleMode"` // Default, Always, OnlyForced, None, Smart
	PlayDefaultAudioTrack      bool   `json:"PlayDefaultAudioTrack"`
	EnableNextEpisodeAutoPlay  bool   `json:"EnableNextEpisodeAutoPlay"`
	HidePlayedInLatest         bool   `json:"HidePlayedInLatest"`
}

// UserPolicy holds the permissions relevant to a client.
type UserPolicy struct {
	IsAdministrator                bool `json:"IsAdministrator"`
	IsDisabled                     bool `json:"IsDisabled"`
	EnableMediaPlayback            bool `json:"EnableMediaPlayback"`
	EnableVideoPlaybackTranscoding bool `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing         bool `json:"EnablePlaybackRemuxing"`
	EnableContentDownloading       bool `json:"EnableContentDownloading"`
	RemoteClientBitrateLimit       int  `json:"RemoteClientBitrateLimit"`
}

type authResult struct {
	User        User   `json:"User"`
	AccessToken string `json:"AccessToken"`
	ServerID    string `json:"ServerId"`
}

// Login authenticates with user name and password (POST
// /Users/AuthenticateByName), stores the resulting session in the client
// and returns it. Wrong credentials yield ErrUnauthorized. On failure the
// previous session, if any, is left untouched.
//
// Login first probes PublicInfo to record server name, version and kind;
// if the probe fails with ErrUnreachable that error is returned directly.
func (c *Client) Login(ctx context.Context, username, password string) (Session, error) {
	info, perr := c.PublicInfo(ctx)
	if perr != nil {
		if isContextErr(perr) || errors.Is(perr, ErrUnreachable) {
			return Session{}, perr
		}
		info = nil // some reverse proxies hide it; login may still work
	}

	// Authenticate without any existing token (some servers would attach
	// the request to the old session). The current session is kept until
	// the login succeeds.
	body := map[string]string{"Username": username, "Pw": password}
	var res authResult
	if err := c.doToken(ctx, "", http.MethodPost, "/Users/AuthenticateByName", nil, body, &res); err != nil {
		return Session{}, err
	}
	if res.AccessToken == "" || res.User.ID == "" {
		return Session{}, &Error{Kind: KindBadResponse, Op: "POST /Users/AuthenticateByName",
			Message: "login response has no access token"}
	}
	s := Session{
		ServerURL:    c.base,
		ServerID:     res.ServerID,
		UserID:       res.User.ID,
		UserName:     res.User.Name,
		UserImageTag: res.User.PrimaryImageTag,
		Token:        res.AccessToken,
		ServerKind:   ServerKindEmby,
	}
	if s.ServerID == "" {
		s.ServerID = res.User.ServerID
	}
	if info != nil {
		s.ServerName = info.ServerName
		s.ServerVersion = info.Version
		s.ServerKind = info.Kind()
		if s.ServerID == "" {
			s.ServerID = info.ID
		}
	}
	c.SetSession(s)
	return s, nil
}

// Logout revokes the token on the server (POST /Sessions/Logout) and clears
// the local session. The local session is cleared even if the request fails.
func (c *Client) Logout(ctx context.Context) error {
	if !c.LoggedIn() {
		return nil
	}
	err := c.do(ctx, http.MethodPost, "/Sessions/Logout", nil, nil, nil)
	c.mu.Lock()
	c.session = Session{ServerURL: c.base}
	c.mu.Unlock()
	return err
}

// CurrentUser fetches the logged-in user (GET /Users/{id}). Use it after
// RestoreClient to check the token: ErrUnauthorized means log in again.
// It also refreshes UserName and UserImageTag in the session.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	uid, err := c.requireUser("GET /Users/{id}")
	if err != nil {
		return nil, err
	}
	var u User
	if err := c.do(ctx, http.MethodGet, "/Users/"+url.PathEscape(uid), nil, nil, &u); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.session.UserID == u.ID {
		c.session.UserName = u.Name
		c.session.UserImageTag = u.PrimaryImageTag
	}
	c.mu.Unlock()
	return &u, nil
}
