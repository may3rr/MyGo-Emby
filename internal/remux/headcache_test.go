package remux

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// countingSource counts the bytes read from a source.
type countingSource struct {
	byteSource
	n atomic.Int64
}

func (s *countingSource) readAt(ctx context.Context, p []byte, off int64) (int, error) {
	n, err := s.byteSource.readAt(ctx, p, off)
	s.n.Add(int64(n))
	return n, err
}

func (s *countingSource) stream(ctx context.Context, off, hint int64) io.ReadCloser {
	return &countingReader{ReadCloser: s.byteSource.stream(ctx, off, hint), n: &s.n}
}

type countingReader struct {
	io.ReadCloser
	n *atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.n.Add(int64(n))
	return n, err
}

// The first segments start where the session read the file as it opened:
// with the head cache they take those bytes from memory, and come out the
// same.
func TestHeadCacheSavesTheFirstReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ts_h264_aac.ts"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(cached bool) (int64, []byte) {
		src := &countingSource{byteSource: &readerAtSource{r: bytes.NewReader(data), n: int64(len(data))}}
		var bs byteSource = src
		if cached {
			bs = newHeadCache(src)
		}
		srv, err := NewServer(WithLogf(t.Logf))
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		x, err := newSession(context.Background(), srv, Source{AudioIndex: -1}, bs)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.add(x); err != nil {
			t.Fatal(err)
		}
		opened := src.n.Load()
		seg := get(t, strings.TrimSuffix(x.PlaylistURL, "master.m3u8")+"0.m4s")
		return src.n.Load() - opened, seg
	}
	without, segPlain := run(false)
	with, segCached := run(true)
	if !bytes.Equal(segPlain, segCached) {
		t.Fatal("the cached session made another first segment")
	}
	if with >= without {
		t.Errorf("the first segment read %d bytes with the cache, %d without", with, without)
	}
	t.Logf("first segment: %d bytes read without the cache, %d with", without, with)
}
