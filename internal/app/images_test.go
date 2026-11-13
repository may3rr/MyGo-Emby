package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pngOf is a w×h picture.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageServer serves pictures, counting the requests for each path.
type imageServer struct {
	*httptest.Server
	mu      sync.Mutex
	hits    map[string]int
	status  map[string][]int // the statuses to answer a path with, in turn
	gate    chan struct{}    // while set, requests wait for it
	active  atomic.Int32
	maxSeen atomic.Int32
	body    []byte
}

func newImageServer(t *testing.T) *imageServer {
	s := &imageServer{hits: map[string]int{}, status: map[string][]int{}, body: pngOf(t, 40, 60)}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := s.active.Add(1)
		defer s.active.Add(-1)
		for {
			m := s.maxSeen.Load()
			if n <= m || s.maxSeen.CompareAndSwap(m, n) {
				break
			}
		}
		s.mu.Lock()
		s.hits[r.URL.Path]++
		code := http.StatusOK
		if q := s.status[r.URL.Path]; len(q) > 0 {
			code, s.status[r.URL.Path] = q[0], q[1:]
		}
		gate := s.gate
		s.mu.Unlock()
		if gate != nil {
			<-gate
		}
		if code != http.StatusOK {
			http.Error(w, "no", code)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(s.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *imageServer) hitsOf(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

// testCache is an imageCache whose posts the test runs.
type testCache struct {
	*imageCache
	posted chan func()
}

func newTestCache(t *testing.T, srv *imageServer, dir string) *testCache {
	tc := &testCache{posted: make(chan func(), 1024)}
	tc.imageCache = newImageCache(srv.Client(), dir, func(fn func()) { tc.posted <- fn })
	t.Cleanup(tc.Close)
	return tc
}

// pump runs what the workers post until done reports true.
func (tc *testCache) pump(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !done() {
		select {
		case fn := <-tc.posted:
			fn()
		case <-deadline:
			t.Fatal("timed out waiting for the cache")
		}
	}
}

func TestImageCacheLoadsOnceAndKeeps(t *testing.T) {
	srv := newImageServer(t)
	tc := newTestCache(t, srv, "")
	url := srv.URL + "/a.png"
	if tc.Get(url) != nil {
		t.Fatal("a picture is there before it loaded")
	}
	for range 10 {
		tc.Get(url) // as the frames do while it loads
	}
	tc.pump(t, func() bool { return tc.Get(url) != nil })
	bmp, at, failed := tc.Lookup(url)
	if bmp == nil || at.IsZero() || failed {
		t.Fatalf("lookup: %v %v %v", bmp, at, failed)
	}
	if w, h := bmp.Size(); w != 40 || h != 60 {
		t.Errorf("size %dx%d", w, h)
	}
	for range 10 {
		tc.Get(url)
	}
	if n := srv.hitsOf("/a.png"); n != 1 {
		t.Errorf("fetched %d times", n)
	}
	if tc.Get("") != nil {
		t.Error("an empty URL has a picture")
	}
}

func TestImageCacheDisk(t *testing.T) {
	srv := newImageServer(t)
	dir := t.TempDir()
	url := srv.URL + "/b.png"
	first := newTestCache(t, srv, dir)
	first.Get(url)
	first.pump(t, func() bool { return first.Get(url) != nil })
	if first.DiskUsage() == 0 {
		t.Fatal("nothing on disk")
	}
	// Another run reads it from the disk.
	second := newTestCache(t, srv, dir)
	second.Get(url)
	second.pump(t, func() bool { return second.Get(url) != nil })
	if n := srv.hitsOf("/b.png"); n != 1 {
		t.Errorf("fetched %d times; the second run did not use the disk", n)
	}
	second.TrimDisk(0)
	if second.DiskUsage() != 0 {
		t.Error("trimming to nothing left files")
	}
}

func TestImageCacheRemembersFailures(t *testing.T) {
	srv := newImageServer(t)
	srv.status["/gone.png"] = []int{404, 404}
	tc := newTestCache(t, srv, "")
	now := time.Now()
	tc.now = func() time.Time { return now }
	url := srv.URL + "/gone.png"
	tc.Get(url)
	tc.pump(t, func() bool { _, _, failed := tc.Lookup(url); return failed })
	for range 20 {
		if tc.Get(url) != nil {
			t.Fatal("a failed picture has a bitmap")
		}
	}
	time.Sleep(50 * time.Millisecond)
	if n := srv.hitsOf("/gone.png"); n != 1 {
		t.Fatalf("a 404 was fetched %d times; it is not tried again at once", n)
	}
	// After its back-off it is tried again, and shows once it is there.
	now = now.Add(time.Hour)
	tc.Get(url)
	tc.pump(t, func() bool {
		tc.mu.Lock()
		defer tc.mu.Unlock()
		return srv.hitsOf("/gone.png") == 2 && len(tc.pending) == 0
	})
	if _, _, failed := tc.Lookup(url); !failed {
		t.Error("the second failure was not remembered")
	}
	now = now.Add(time.Hour)
	tc.Get(url)
	tc.pump(t, func() bool { return tc.Get(url) != nil })
}

func TestImageCacheRetriesTransientFailures(t *testing.T) {
	srv := newImageServer(t)
	srv.status["/busy.png"] = []int{503}
	tc := newTestCache(t, srv, "")
	url := srv.URL + "/busy.png"
	tc.Get(url)
	tc.pump(t, func() bool { return tc.Get(url) != nil })
	if n := srv.hitsOf("/busy.png"); n != 2 {
		t.Errorf("fetched %d times, want a retry", n)
	}
}

func TestImageCacheFetchesAtMostEightAtOnce(t *testing.T) {
	srv := newImageServer(t)
	srv.gate = make(chan struct{})
	tc := newTestCache(t, srv, "")
	var urls []string
	for i := range 24 {
		url := srv.URL + "/" + string(rune('a'+i)) + ".png"
		urls = append(urls, url)
		tc.Get(url)
	}
	deadline := time.Now().Add(2 * time.Second)
	for srv.active.Load() < imageWorkers && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := srv.maxSeen.Load(); got != imageWorkers {
		t.Errorf("%d fetches at once, want %d", got, imageWorkers)
	}
	srv.mu.Lock()
	close(srv.gate)
	srv.gate = nil
	srv.mu.Unlock()
	tc.pump(t, func() bool {
		for _, u := range urls {
			if tc.Get(u) == nil {
				return false
			}
		}
		return true
	})
	if got := srv.maxSeen.Load(); got > imageWorkers {
		t.Errorf("%d fetches at once", got)
	}
}

func TestImageCacheEvictsLeastRecentlyUsed(t *testing.T) {
	srv := newImageServer(t)
	tc := newTestCache(t, srv, "")
	tc.limit = 2 * 40 * 60 * 4 // two pictures
	a, b, c := srv.URL+"/1.png", srv.URL+"/2.png", srv.URL+"/3.png"
	for _, u := range []string{a, b} {
		tc.Get(u)
		tc.pump(t, func() bool { return tc.Get(u) != nil })
	}
	tc.Get(a) // a is used again: b is the least recent
	tc.Get(c)
	tc.pump(t, func() bool { return tc.Get(c) != nil })
	if tc.bytes > tc.limit || len(tc.entries) != 2 {
		t.Fatalf("holds %d bytes in %d entries", tc.bytes, len(tc.entries))
	}
	if tc.entries[b] != nil || tc.entries[a] == nil {
		t.Error("evicted the wrong picture")
	}
}

func TestImagePx(t *testing.T) {
	for _, tc := range []struct {
		dips, scale float32
		want        int
	}{
		{160, 2, 320}, {150, 2, 320}, {161, 2, 400}, {288, 2, 640}, {100, 1, 160},
		{1200, 2, 2560}, {5000, 2, 3840}, {160, 0, 160},
	} {
		if got := imagePx(tc.dips, tc.scale); got != tc.want {
			t.Errorf("imagePx(%v, %v) = %d, want %d", tc.dips, tc.scale, got, tc.want)
		}
	}
}
