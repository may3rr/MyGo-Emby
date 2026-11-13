package emby

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DeviceInfo identifies this application to the server. The server lists
// sessions by these values; DeviceID must be stable across launches (persist
// it, see NewDeviceID), because Jellyfin keys access tokens by device.
type DeviceInfo struct {
	Client   string // application name, e.g. "MyGo Emby"
	Device   string // human-readable machine name, e.g. "Jackie's MacBook"
	DeviceID string // stable unique id
	Version  string // application version, e.g. "0.1.0"
}

// NewDeviceID returns a random 32-character hex id suitable for
// DeviceInfo.DeviceID. Generate it once and persist it.
func NewDeviceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Session is everything needed to resume a login without the password.
// It is plain data: marshal it to JSON to persist it.
type Session struct {
	ServerURL     string `json:"serverUrl"`
	ServerID      string `json:"serverId,omitempty"`
	ServerName    string `json:"serverName,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	ServerKind    string `json:"serverKind,omitempty"` // ServerKindEmby or ServerKindJellyfin
	UserID        string `json:"userId"`
	UserName      string `json:"userName"`
	UserImageTag  string `json:"userImageTag,omitempty"`
	Token         string `json:"token"`
}

// Valid reports whether the session has the fields needed for API calls.
func (s Session) Valid() bool {
	return s.ServerURL != "" && s.UserID != "" && s.Token != ""
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the default *http.Client (45 s timeout, proxy from
// environment). The client must not strip the Authorization header on
// same-host redirects.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// Client talks to one Emby or Jellyfin server. It is safe for concurrent use.
type Client struct {
	base   string // normalized, no trailing slash, e.g. "https://host/emby"
	device DeviceInfo
	http   *http.Client

	mu      sync.RWMutex
	session Session
}

// NewClient returns an unauthenticated client for serverURL, which is
// normalized with NormalizeServerURL.
func NewClient(serverURL string, device DeviceInfo, opts ...Option) (*Client, error) {
	base, err := NormalizeServerURL(serverURL)
	if err != nil {
		return nil, err
	}
	c := &Client{
		base:   base,
		device: device,
		http:   &http.Client{Timeout: 45 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	c.session.ServerURL = base
	return c, nil
}

// RestoreClient returns a client that is already logged in with s.
// It performs no network request; call CurrentUser to validate the token.
func RestoreClient(s Session, device DeviceInfo, opts ...Option) (*Client, error) {
	c, err := NewClient(s.ServerURL, device, opts...)
	if err != nil {
		return nil, err
	}
	c.SetSession(s)
	return c, nil
}

// BaseURL returns the normalized server URL, without trailing slash.
func (c *Client) BaseURL() string { return c.base }

// Device returns the device info this client identifies itself with.
func (c *Client) Device() DeviceInfo { return c.device }

// Session returns a copy of the current session (zero Token when logged out).
func (c *Client) Session() Session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.session
}

// SetSession installs s as the current session. ServerURL is forced to the
// client's base URL.
func (c *Client) SetSession(s Session) {
	s.ServerURL = c.base
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()
}

// LoggedIn reports whether the client has a user id and token.
func (c *Client) LoggedIn() bool {
	s := c.Session()
	return s.UserID != "" && s.Token != ""
}

// UserID returns the logged-in user's id, or "".
func (c *Client) UserID() string { return c.Session().UserID }

// Token returns the access token, or "".
func (c *Client) Token() string { return c.Session().Token }

// IsJellyfin reports whether the session's server identified itself as
// Jellyfin. Unknown servers are treated as Emby.
func (c *Client) IsJellyfin() bool { return c.Session().ServerKind == ServerKindJellyfin }

// NormalizeServerURL turns what a user typed into a canonical base URL:
// it adds "http://" when no scheme is given, lowercases scheme and host,
// strips a trailing slash, query and fragment, and strips a pasted web-client
// suffix such as "/web/index.html", "/web/#/home" or "/web".
// A "/emby" or "/jellyfin" base path is kept.
func NormalizeServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", &Error{Kind: KindInvalidURL, Message: "empty server address"}
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", &Error{Kind: KindInvalidURL, Message: raw, Err: err}
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", &Error{Kind: KindInvalidURL, Message: "unsupported scheme " + u.Scheme}
	}
	if u.Host == "" || strings.HasPrefix(u.Host, ":") {
		return "", &Error{Kind: KindInvalidURL, Message: "missing host in " + raw}
	}
	u.Host = strings.ToLower(u.Host)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	u.RawFragment = ""
	p := strings.TrimRight(u.Path, "/")
	lp := strings.ToLower(p)
	if i := strings.Index(lp, "/web/"); i >= 0 {
		p = p[:i]
	} else if strings.HasSuffix(lp, "/web") {
		p = p[:len(p)-len("/web")]
	}
	p = strings.TrimRight(p, "/")
	u.Path = p
	u.RawPath = ""
	return u.String(), nil
}

// authValue builds the MediaBrowser authorization header value.
func (c *Client) authValue(token string) string {
	var b strings.Builder
	b.WriteString("MediaBrowser ")
	part := func(k, v string, last bool) {
		b.WriteString(k)
		b.WriteString(`="`)
		b.WriteString(headerEscape(v))
		b.WriteString(`"`)
		if !last {
			b.WriteString(", ")
		}
	}
	d := c.device
	part("Client", orDefault(d.Client, "MyGo Emby"), false)
	part("Device", orDefault(d.Device, "Mac"), false)
	part("DeviceId", orDefault(d.DeviceID, "mygo-emby"), false)
	part("Version", orDefault(d.Version, "0.0.0"), token == "")
	if token != "" {
		part("Token", token, true)
	}
	return b.String()
}

// headerEscape percent-encodes characters that would break the quoted
// MediaBrowser header (quotes, commas, percent, control and non-ASCII
// bytes). Plain ASCII such as "MyGo Emby" is kept readable. Jellyfin decodes
// the values; Emby shows them as sent.
func headerEscape(s string) string {
	const hexd = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < 0x20 || ch >= 0x7f || ch == '"' || ch == ',' || ch == '%' || ch == '=' {
			b.WriteByte('%')
			b.WriteByte(hexd[ch>>4])
			b.WriteByte(hexd[ch&15])
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// setAuthHeaders adds the three authorization headers understood by Emby
// and Jellyfin. Newer Jellyfin only honours Authorization; Emby honours all.
func (c *Client) setAuthHeaders(h http.Header, token string) {
	v := c.authValue(token)
	h.Set("Authorization", v)
	h.Set("X-Emby-Authorization", v)
	if token != "" {
		h.Set("X-Emby-Token", token)
	}
}

// AuthorizationHeader returns the value sent in the Authorization and
// X-Emby-Authorization headers, for callers making their own requests.
func (c *Client) AuthorizationHeader() string { return c.authValue(c.Token()) }

// apiURL joins the base URL, an API path and a query.
func (c *Client) apiURL(path string, q url.Values) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// requireUser returns the user id and token, or ErrNotLoggedIn.
func (c *Client) requireUser(op string) (string, error) {
	s := c.Session()
	if s.UserID == "" || s.Token == "" {
		return "", &Error{Kind: KindNotLoggedIn, Op: op}
	}
	return s.UserID, nil
}

const maxErrorBody = 4 << 10

// do performs one API request with the session token. body, when non-nil,
// is sent as JSON. out, when non-nil, receives the decoded JSON response.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	return c.doToken(ctx, c.Token(), method, path, q, body, out)
}

// doToken is do with an explicit token ("" for anonymous requests).
func (c *Client) doToken(ctx context.Context, token, method, path string, q url.Values, body, out any) error {
	op := method + " " + path
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return &Error{Kind: KindBadResponse, Op: op, Message: "encode request", Err: err}
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.apiURL(path, q), rd)
	if err != nil {
		return &Error{Kind: KindInvalidURL, Op: op, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setAuthHeaders(req.Header, token)

	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &Error{Kind: KindUnreachable, Op: op, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return &Error{
			Kind:       kindForStatus(resp.StatusCode),
			Op:         op,
			StatusCode: resp.StatusCode,
			Message:    serverMessage(resp.Header.Get("Content-Type"), b),
		}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &Error{Kind: KindUnreachable, Op: op, StatusCode: resp.StatusCode, Err: err}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		msg := "response is not valid JSON"
		if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "html") {
			msg = "server answered with an HTML page, not the media server API"
		}
		return &Error{Kind: KindBadResponse, Op: op, StatusCode: resp.StatusCode, Message: msg, Err: err}
	}
	return nil
}

// fetchRaw GETs an absolute URL with auth headers and returns the body
// (at most limit bytes). Used for subtitles.
func (c *Client) fetchRaw(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	op := "GET " + redactURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &Error{Kind: KindInvalidURL, Op: op, Err: err}
	}
	c.setAuthHeaders(req.Header, c.Token())
	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &Error{Kind: KindUnreachable, Op: op, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &Error{Kind: kindForStatus(resp.StatusCode), Op: op, StatusCode: resp.StatusCode,
			Message: serverMessage(resp.Header.Get("Content-Type"), b)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, &Error{Kind: KindUnreachable, Op: op, StatusCode: resp.StatusCode, Err: err}
	}
	return data, nil
}

// redactURL drops the query string (which may hold a token) for error text.
func redactURL(s string) string {
	if i := strings.IndexByte(s, '?'); i >= 0 {
		return s[:i]
	}
	return s
}

// isContextErr reports whether err is a context cancellation or deadline.
func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
