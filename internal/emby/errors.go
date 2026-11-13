package emby

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorKind classifies a failed request so the UI can show a specific message.
type ErrorKind int

const (
	// KindUnknown is never produced by this package; it is the zero value.
	KindUnknown ErrorKind = iota
	// KindUnreachable: DNS failure, connection refused, TLS error, timeout.
	KindUnreachable
	// KindUnauthorized: HTTP 401. Wrong user name or password on login,
	// expired or revoked token otherwise.
	KindUnauthorized
	// KindForbidden: HTTP 403. User disabled, or not allowed to do this.
	KindForbidden
	// KindNotFound: HTTP 404. Unknown item, or a route the server lacks.
	KindNotFound
	// KindHTTP: any other non-2xx HTTP status.
	KindHTTP
	// KindBadResponse: 2xx but the body is not the expected JSON. Usually the
	// URL points at something that is not an Emby or Jellyfin server.
	KindBadResponse
	// KindNotLoggedIn: the method needs a session and the client has none.
	KindNotLoggedIn
	// KindInvalidURL: the server address cannot be parsed.
	KindInvalidURL
	// KindNotPlayable: PlaybackInfo returned no usable stream.
	KindNotPlayable
)

func (k ErrorKind) String() string {
	switch k {
	case KindUnreachable:
		return "unreachable"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not found"
	case KindHTTP:
		return "http error"
	case KindBadResponse:
		return "bad response"
	case KindNotLoggedIn:
		return "not logged in"
	case KindInvalidURL:
		return "invalid url"
	case KindNotPlayable:
		return "not playable"
	}
	return "unknown"
}

// Sentinel errors for errors.Is. An *Error matches the sentinel of its Kind.
var (
	ErrUnreachable  = errors.New("emby: server unreachable")
	ErrUnauthorized = errors.New("emby: unauthorized")
	ErrForbidden    = errors.New("emby: forbidden")
	ErrNotFound     = errors.New("emby: not found")
	ErrHTTP         = errors.New("emby: http error")
	ErrBadResponse  = errors.New("emby: unexpected response")
	ErrNotLoggedIn  = errors.New("emby: not logged in")
	ErrInvalidURL   = errors.New("emby: invalid server url")
	ErrNotPlayable  = errors.New("emby: no playable stream")
)

func (k ErrorKind) sentinel() error {
	switch k {
	case KindUnreachable:
		return ErrUnreachable
	case KindUnauthorized:
		return ErrUnauthorized
	case KindForbidden:
		return ErrForbidden
	case KindNotFound:
		return ErrNotFound
	case KindHTTP:
		return ErrHTTP
	case KindBadResponse:
		return ErrBadResponse
	case KindNotLoggedIn:
		return ErrNotLoggedIn
	case KindInvalidURL:
		return ErrInvalidURL
	case KindNotPlayable:
		return ErrNotPlayable
	}
	return nil
}

// Error is the concrete error type returned by this package.
type Error struct {
	Kind       ErrorKind
	Op         string // e.g. "GET /Users/{id}/Views"
	StatusCode int    // HTTP status, 0 when no response was received
	Message    string // server-provided message, if any
	Err        error  // underlying transport or decode error, if any
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("emby: ")
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(e.Kind.String())
	if e.StatusCode != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.StatusCode)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap exposes the underlying error (for example a *net.OpError).
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is the sentinel for e.Kind.
func (e *Error) Is(target error) bool {
	s := e.Kind.sentinel()
	return s != nil && target == s
}

// KindOf returns the ErrorKind of err, or KindUnknown if err is not (or does
// not wrap) an *Error. Context cancellation is reported as KindUnknown.
func KindOf(err error) ErrorKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return KindUnknown
}

// StatusCode returns the HTTP status carried by err, or 0.
func StatusCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.StatusCode
	}
	return 0
}

func kindForStatus(code int) ErrorKind {
	switch code {
	case http.StatusUnauthorized:
		return KindUnauthorized
	case http.StatusForbidden:
		return KindForbidden
	case http.StatusNotFound:
		return KindNotFound
	}
	return KindHTTP
}

// serverMessage extracts a short human-readable message from an error body.
// Emby and Jellyfin return either plain text, a JSON object with a Message
// or title field, or (behind reverse proxies) an HTML page, which is ignored.
func serverMessage(contentType string, body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if strings.Contains(contentType, "html") || strings.HasPrefix(s, "<") {
		return ""
	}
	if strings.HasPrefix(s, "{") {
		var m struct {
			Message string `json:"Message"`
			Title   string `json:"title"`
			Detail  string `json:"detail"`
		}
		if json.Unmarshal(body, &m) == nil {
			switch {
			case m.Message != "":
				return m.Message
			case m.Detail != "":
				return m.Detail
			case m.Title != "":
				return m.Title
			}
		}
		return ""
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
