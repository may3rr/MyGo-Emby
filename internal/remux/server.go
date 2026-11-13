package remux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrClosed is returned by Open after Close.
var ErrClosed = errors.New("remux: server closed")

// Server serves the playlists and segments of sessions on 127.0.0.1. One
// server serves any number of sessions; it is safe for concurrent use.
type Server struct {
	ln   net.Listener
	hs   *http.Server
	base string
	logf func(string, ...any)
	// ffmpeg is resolved at startup. GUI launches often have a minimal
	// PATH, so the standard Homebrew locations are checked explicitly.
	ffmpeg string

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool
}

// Option configures a Server.
type Option func(*Server)

// WithLogf sets a logger for diagnostics (segment timings, retries). URLs
// in messages have their queries, which carry tokens, removed.
func WithLogf(f func(format string, args ...any)) Option {
	return func(s *Server) {
		if f != nil {
			s.logf = f
		}
	}
}

// WithFFmpeg sets the executable used only for audio that cannot be copied
// into fMP4. An empty path disables the optional fallback. Tests and
// embedders can use it to choose a bundled executable.
func WithFFmpeg(path string) Option {
	return func(s *Server) {
		s.ffmpeg = path
	}
}

// NewServer listens on a random port of 127.0.0.1.
func NewServer(opts ...Option) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{ln: ln, base: "http://" + ln.Addr().String(), logf: func(string, ...any) {},
		ffmpeg: findFFmpeg(), sessions: map[string]*Session{}}
	for _, o := range opts {
		o(s)
	}
	s.hs = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	go s.hs.Serve(ln)
	return s, nil
}

// Close ends every session and stops listening.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	sessions := s.sessions
	s.sessions = map[string]*Session{}
	s.mu.Unlock()
	for _, x := range sessions {
		x.Close()
	}
	return s.hs.Close()
}

func findFFmpeg() string {
	candidates := []string{os.Getenv("MYGO_FFMPEG"), "/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		candidates = append(candidates, path)
	}
	seen := map[string]bool{}
	for _, path := range candidates {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		if !filepath.IsAbs(path) {
			if p, err := exec.LookPath(path); err == nil {
				path = p
			}
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

func newToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) add(x *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.sessions[x.token] = x
	return nil
}

func (s *Server) remove(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// ServeHTTP routes /{token}/{file} to a session.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token, name, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	x := s.sessions[token]
	s.mu.Unlock()
	if x == nil {
		http.NotFound(w, r)
		return
	}
	if name == "source" {
		x.serveSource(w, r)
		return
	}
	x.serve(w, r, name)
}

// Open reads the source's header and index and returns a session whose
// PlaylistURL AVPlayer can open. ctx bounds the opening only; the session
// lives until Close.
func (s *Server) Open(ctx context.Context, src Source) (*Session, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	x, err := openSession(ctx, s, src)
	if err != nil {
		return nil, err
	}
	if err := s.add(x); err != nil {
		x.Close()
		return nil, err
	}
	x.startReadAhead()
	return x, nil
}
