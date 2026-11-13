package remux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// byteSource is random access to the remote file.
type byteSource interface {
	// size is the file length.
	size() int64
	// readAt reads len(p) bytes at off, fewer only at the end of the file.
	readAt(ctx context.Context, p []byte, off int64) (int, error)
	// stream reads sequentially from off. hint is how many bytes the
	// caller expects to read, to size the first range request; reading
	// on past it is fine.
	stream(ctx context.Context, off, hint int64) io.ReadCloser
}

// ErrUpstream wraps failures to read the remote file.
var ErrUpstream = errors.New("remux: upstream read failed")

const (
	idleTimeout   = 20 * time.Second
	maxRetries    = 4
	minChunk      = 256 << 10
	maxChunk      = 16 << 20
	drainLimit    = 128 << 10
	expiryHeadway = 45 * time.Second
)

// upstream reads a remote file with HTTP range requests. It follows the
// first redirect once and keeps the final address (an object storage
// URL), going back to the original URL when that address expires or is
// refused.
type upstream struct {
	client   *http.Client
	noFollow *http.Client
	orig     string
	header   http.Header
	logf     func(string, ...any)

	mu       sync.Mutex
	final    string
	expires  time.Time // zero: unknown
	resolved bool
	length   atomic.Int64

	bytesRead atomic.Int64
	requests  atomic.Int64
}

func newUpstream(c *http.Client, rawURL string, header http.Header, logf func(string, ...any)) *upstream {
	if c == nil {
		c = defaultClient
	}
	// Segment reads can take longer than an API client's timeout; the
	// idle watchdog bounds them instead.
	cc := *c
	cc.Timeout = 0
	nf := cc
	nf.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	u := &upstream{client: &cc, noFollow: &nf, orig: rawURL, header: header, logf: logf}
	u.length.Store(-1)
	return u
}

var defaultClient = &http.Client{Transport: func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 8
	t.ReadBufferSize = 64 << 10
	return t
}()}

// resolve finds the final address of the file and its size with a one-byte
// request that does not follow redirects into the file.
func (u *upstream) resolve(ctx context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.resolved && (u.expires.IsZero() || time.Until(u.expires) > expiryHeadway) {
		return nil
	}
	target := u.orig
	for hop := 0; hop < 8; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		for k, v := range u.header {
			req.Header[k] = v
		}
		req.Header.Set("Range", "bytes=0-0")
		u.requests.Add(1)
		t0 := time.Now()
		resp, err := u.noFollow.Do(req)
		u.logf("remux: resolve %s → %s in %v", redact(target), status(resp, err), time.Since(t0).Round(time.Millisecond))
		if err != nil {
			return fmt.Errorf("%w: %s", ErrUpstream, status(nil, err))
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc, err := resp.Location()
			if err != nil {
				return fmt.Errorf("%w: redirect without location", ErrUpstream)
			}
			target = loc.String()
			continue
		case resp.StatusCode == http.StatusPartialContent || resp.StatusCode == http.StatusOK:
			if n := contentLength(resp); n > 0 {
				u.length.Store(n)
			}
			u.final = target
			u.expires = signatureExpiry(target, time.Now())
			u.resolved = true
			return nil
		default:
			return fmt.Errorf("%w: %s from %s", ErrUpstream, resp.Status, redact(target))
		}
	}
	return fmt.Errorf("%w: too many redirects", ErrUpstream)
}

// invalidate forgets the final address after it was refused.
func (u *upstream) invalidate(addr string) {
	u.mu.Lock()
	if u.final == addr {
		u.resolved = false
	}
	u.mu.Unlock()
}

func (u *upstream) addr(ctx context.Context) (string, error) {
	if err := u.resolve(ctx); err != nil {
		return "", err
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.final, nil
}

func (u *upstream) size() int64 { return u.length.Load() }

// get requests bytes [off, end) (end -1: to the end of the file).
func (u *upstream) get(ctx context.Context, off, end int64) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, time.Duration(attempt*attempt)*250*time.Millisecond); err != nil {
				return nil, err
			}
		}
		addr, err := u.addr(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
		if err != nil {
			return nil, err
		}
		if addr == u.orig {
			for k, v := range u.header {
				req.Header[k] = v
			}
		}
		if end > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(end-1, 10))
		} else {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-")
		}
		u.requests.Add(1)
		t0 := time.Now()
		resp, err := u.client.Do(req)
		u.logf("remux: GET %d-%d → %s in %v", off, end, status(resp, err), time.Since(t0).Round(time.Millisecond))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("%w: %s", ErrUpstream, status(nil, err))
			continue
		}
		switch {
		case resp.StatusCode == http.StatusPartialContent:
			if start := contentRangeStart(resp); start != off {
				resp.Body.Close()
				return nil, fmt.Errorf("%w: server answered range at %d for %d", ErrUpstream, start, off)
			}
			return resp, nil
		case resp.StatusCode == http.StatusOK && off == 0:
			return resp, nil
		case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
			resp.Body.Close()
			return nil, io.EOF
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusGone ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound:
			resp.Body.Close()
			lastErr = fmt.Errorf("%w: %s", ErrUpstream, resp.Status)
			if addr != u.orig {
				u.logf("remux: upstream refused (%s), resolving again", resp.Status)
				u.invalidate(addr)
				continue
			}
			return nil, lastErr
		default:
			resp.Body.Close()
			lastErr = fmt.Errorf("%w: %s", ErrUpstream, resp.Status)
			if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
				continue
			}
			return nil, lastErr
		}
	}
	return nil, lastErr
}

func (u *upstream) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if n := u.size(); n >= 0 && off >= n {
		return 0, io.EOF
	}
	r := u.stream(ctx, off, int64(len(p)))
	defer r.Close()
	n, err := io.ReadFull(r, p)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return n, err
}

func (u *upstream) stream(ctx context.Context, off, hint int64) io.ReadCloser {
	return &rangeReader{u: u, ctx: ctx, off: off, chunk: max(hint, minChunk)}
}

// rangeReader reads a file sequentially with range requests of growing
// size, resuming after broken connections.
type rangeReader struct {
	u     *upstream
	ctx   context.Context
	off   int64
	chunk int64

	body    io.ReadCloser
	bodyEnd int64
	cancel  context.CancelFunc
	timer   *time.Timer
	fails   int
	closed  bool
}

func (r *rangeReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.body == nil {
			if size := r.u.size(); size >= 0 && r.off >= size {
				return 0, io.EOF
			}
			if err := r.open(); err != nil {
				return 0, err
			}
		}
		n, err := r.body.Read(p)
		if n > 0 {
			r.off += int64(n)
			r.u.bytesRead.Add(int64(n))
			r.timer.Reset(idleTimeout)
			r.fails = 0
		}
		if err == nil {
			return n, nil
		}
		complete := err == io.EOF && r.off >= r.bodyEnd
		r.closeBody(false)
		if complete {
			if n > 0 {
				return n, nil
			}
			continue
		}
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
		r.fails++
		if r.fails > maxRetries {
			return n, fmt.Errorf("%w: %s", ErrUpstream, status(nil, err))
		}
		r.u.logf("remux: upstream read broke at %d (%s), resuming", r.off, status(nil, err))
		if n > 0 {
			return n, nil
		}
		if err := sleepCtx(r.ctx, time.Duration(r.fails)*200*time.Millisecond); err != nil {
			return 0, err
		}
	}
}

func (r *rangeReader) open() error {
	end := r.off + r.chunk
	if size := r.u.size(); size >= 0 && end > size {
		end = size
	}
	ctx, cancel := context.WithCancel(r.ctx)
	timer := time.AfterFunc(idleTimeout, cancel)
	resp, err := r.u.get(ctx, r.off, end)
	if err != nil {
		timer.Stop()
		cancel()
		return err
	}
	if resp.StatusCode == http.StatusOK {
		end = r.off + resp.ContentLength
		if resp.ContentLength < 0 {
			end = 1<<62 - 1
		}
	}
	r.body, r.bodyEnd, r.cancel, r.timer = resp.Body, end, cancel, timer
	if r.chunk < maxChunk {
		r.chunk = min(r.chunk*2, maxChunk)
	}
	return nil
}

func (r *rangeReader) closeBody(drain bool) {
	if r.body == nil {
		return
	}
	if drain && r.bodyEnd-r.off <= drainLimit {
		// Read the rest so the connection can serve the next request.
		n, _ := io.Copy(io.Discard, r.body)
		r.u.bytesRead.Add(n)
	}
	r.body.Close()
	r.timer.Stop()
	r.cancel()
	r.body = nil
}

func (r *rangeReader) Close() error {
	r.closeBody(true)
	r.closed = true
	return nil
}

func contentLength(resp *http.Response) int64 {
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndexByte(cr, '/'); i >= 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
				return n
			}
		}
	}
	if resp.StatusCode == http.StatusOK {
		return resp.ContentLength
	}
	return -1
}

func contentRangeStart(resp *http.Response) int64 {
	cr := resp.Header.Get("Content-Range")
	cr = strings.TrimPrefix(strings.TrimSpace(cr), "bytes ")
	i := strings.IndexByte(cr, '-')
	if i < 0 {
		return -1
	}
	n, err := strconv.ParseInt(cr[:i], 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// signatureExpiry reads the expiry of a pre-signed object storage URL
// (AWS SigV4 X-Amz-Date + X-Amz-Expires, or an Expires Unix time).
func signatureExpiry(raw string, now time.Time) time.Time {
	u, err := url.Parse(raw)
	if err != nil {
		return time.Time{}
	}
	q := u.Query()
	if exp := q.Get("X-Amz-Expires"); exp != "" {
		secs, err1 := strconv.Atoi(exp)
		date, err2 := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
		if err1 == nil && err2 == nil {
			return date.Add(time.Duration(secs) * time.Second)
		}
		if err1 == nil {
			return now.Add(time.Duration(secs) * time.Second)
		}
	}
	for _, k := range []string{"Expires", "expires"} {
		if v := q.Get(k); v != "" {
			if t, err := strconv.ParseInt(v, 10, 64); err == nil && t > 1e9 && t < 1e11 {
				return time.Unix(t, 0)
			}
		}
	}
	return time.Time{}
}

// redact drops the query of a URL for logs: it carries tokens.
func redact(raw string) string {
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		return raw[:i]
	}
	return raw
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// readerAtSource is a byteSource over local data, for tests and files.
type readerAtSource struct {
	r interface {
		ReadAt([]byte, int64) (int, error)
	}
	n int64
}

func (s *readerAtSource) size() int64 { return s.n }

func (s *readerAtSource) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	if off >= s.n {
		return 0, io.EOF
	}
	n, err := s.r.ReadAt(p, off)
	if err == io.EOF && n > 0 {
		err = nil
		if n < len(p) {
			err = io.EOF
		}
	}
	return n, err
}

func (s *readerAtSource) stream(ctx context.Context, off, hint int64) io.ReadCloser {
	return io.NopCloser(&ctxReader{ctx: ctx, r: io.NewSectionReader(s.r, off, max(s.n-off, 0))})
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// status describes a response for logs, without the URL (it carries a
// signature).
func status(resp *http.Response, err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	if err != nil {
		return err.Error()
	}
	return resp.Status
}
