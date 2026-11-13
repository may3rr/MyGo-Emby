package remux

import (
	"context"
	"io"
	"sync"
)

// headCacheMax bounds the start of a file a session keeps in memory.
const headCacheMax = 8 << 20

// headCache keeps the start of the file as the demuxer read it while the
// session opened (the header, and for MPEG-TS the first GOPs it scans for
// the codecs and the GOP length), so that the first segments, which start
// there, take those bytes from memory rather than fetch them again. It
// records only while the session opens, until freeze, and only bytes that
// continue what it holds from offset 0, up to headCacheMax.
type headCache struct {
	byteSource
	mu        sync.Mutex
	buf       []byte // the file's bytes [0, len(buf))
	recording bool
}

func newHeadCache(bs byteSource) *headCache {
	return &headCache{byteSource: bs, recording: true}
}

// freeze stops recording: what the cache holds stays for the session.
func (h *headCache) freeze() {
	h.mu.Lock()
	h.recording = false
	h.mu.Unlock()
}

// held returns the bytes the cache holds from off, nil past them. The
// slice is capped, so that recording more never writes into it.
func (h *headCache) held(off int64) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if off < 0 || off >= int64(len(h.buf)) {
		return nil
	}
	return h.buf[off:len(h.buf):len(h.buf)]
}

// record adds p, read at off, where it continues what the cache holds.
func (h *headCache) record(p []byte, off int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	end := int64(len(h.buf))
	if !h.recording || off > end || off+int64(len(p)) <= end {
		return
	}
	p = p[end-off:]
	if room := headCacheMax - len(h.buf); len(p) > room {
		p = p[:room]
	}
	h.buf = append(h.buf, p...)
}

func (h *headCache) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n := copy(p, h.held(off))
	if n == len(p) {
		return n, nil
	}
	m, err := h.byteSource.readAt(ctx, p[n:], off+int64(n))
	if m > 0 {
		h.record(p[n:n+m], off+int64(n))
	}
	return n + m, err
}

func (h *headCache) stream(ctx context.Context, off, hint int64) io.ReadCloser {
	return &headReader{h: h, ctx: ctx, off: off, hint: hint}
}

// headReader reads from the cache while it holds the bytes, then from the
// source, which it opens only then.
type headReader struct {
	h         *headCache
	ctx       context.Context
	off, hint int64
	up        io.ReadCloser
}

func (r *headReader) Read(p []byte) (int, error) {
	if r.up == nil {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if b := r.h.held(r.off); len(b) > 0 {
			n := copy(p, b)
			r.off += int64(n)
			r.hint -= int64(n)
			return n, nil
		}
		r.up = r.h.byteSource.stream(r.ctx, r.off, max(r.hint, 0))
	}
	n, err := r.up.Read(p)
	if n > 0 {
		r.h.record(p[:n], r.off)
		r.off += int64(n)
	}
	return n, err
}

func (r *headReader) Close() error {
	if r.up != nil {
		return r.up.Close()
	}
	return nil
}
