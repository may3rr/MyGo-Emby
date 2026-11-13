package app

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// imageCache loads remote pictures for the views: posters, backdrops,
// avatars. It knows nothing of Emby: it takes URLs, already sized for what
// shows them (see imagePx), and a plain HTTP client.
//
// The views call Get in every frame they show a picture. Get returns the
// bitmap once it is in memory and nil while it loads, starting the load the
// first time; the window redraws when it arrives. Pictures are kept in
// memory up to a budget of bytes, the least recently asked for going first,
// and on disk under the user's cache directory, keyed by a hash of the URL.
// At most imageWorkers fetch at once, the most recently asked for first, so
// the cards that just scrolled into view come before those that scrolled
// past. A load that failed is not tried again before its back-off has passed.
//
// Get, Lookup and the other methods run on the main thread; the fetching and
// decoding happen on workers, which hand their results back through post.
type imageCache struct {
	client *http.Client
	dir    string       // the disk cache, "" for none
	post   func(func()) // runs a function on the main thread, then redraws
	// onMain runs a function on the main thread without redrawing, for
	// letting go of pixels once shown; nil keeps them.
	onMain func(func())
	now    func() time.Time

	// Main thread only.
	entries map[string]*imageEntry
	lru     *list.List // of *imageEntry, the most recently asked for first
	bytes   int64
	limit   int64
	failed  map[string]imageFailure

	// The work queue, guarded by mu: the URLs waiting, the latest last, and
	// those waiting or loading, until their result is back on the main
	// thread.
	mu      sync.Mutex
	cond    *sync.Cond
	stack   []string
	pending map[string]bool
	started bool
	closed  bool
	active  int // loads under way, for tests
}

// imageEntry is a picture in memory.
type imageEntry struct {
	url      string
	bmp      *ui.Bitmap
	cost     int64     // the bytes its pixels take
	loadedAt time.Time // when it arrived, to fade it in
	elem     *list.Element
}

// imageFailure is a load that failed: not tried again until retry.
type imageFailure struct {
	retry time.Time
	tries int
}

const (
	imageWorkers   = 8         // loads at once
	imageQueueMax  = 400       // URLs waiting at most; the oldest are dropped
	imageMemLimit  = 192 << 20 // bytes of pixels kept in memory
	imageMaxBytes  = 32 << 20  // the largest picture fetched
	imageTimeout   = 30 * time.Second
	imageDiskLimit = 512 << 20 // bytes kept on disk, trimmed at start
	imageRelease   = 2 * time.Second
	imageTries     = 3 // fetches of a picture before it counts as failed
)

// statusError is a server's answer other than a picture.
type statusError struct {
	url  string
	code int
	text string
}

func (e *statusError) Error() string { return fmt.Sprintf("fetching %s: %s", e.url, e.text) }

// transient reports whether a fetch that failed with err may work if tried
// again at once: the network failed, or the server was busy.
func transient(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return se.code >= 500 || se.code == http.StatusTooManyRequests
	}
	return true
}

// newImageCache makes a cache that fetches with client, keeps pictures in
// dir ("" for memory only), and posts results with post.
func newImageCache(client *http.Client, dir string, post func(func())) *imageCache {
	if client == nil {
		client = http.DefaultClient
	}
	c := &imageCache{
		client:  client,
		dir:     dir,
		post:    post,
		now:     time.Now,
		entries: map[string]*imageEntry{},
		lru:     list.New(),
		limit:   imageMemLimit,
		failed:  map[string]imageFailure{},
		pending: map[string]bool{},
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// Get returns the picture at url, or nil while it loads, after it failed,
// or for an empty url.
func (c *imageCache) Get(url string) *ui.Bitmap {
	bmp, _, _ := c.Lookup(url)
	return bmp
}

// Lookup returns the picture at url with when it arrived, or nil while it
// loads; failed reports that the last load failed, for the view to show
// what stands in for it rather than a placeholder that waits forever.
func (c *imageCache) Lookup(url string) (bmp *ui.Bitmap, loadedAt time.Time, failed bool) {
	if url == "" {
		return nil, time.Time{}, false
	}
	if e := c.entries[url]; e != nil {
		c.lru.MoveToFront(e.elem)
		return e.bmp, e.loadedAt, false
	}
	if f, ok := c.failed[url]; ok {
		if c.now().Before(f.retry) {
			return nil, time.Time{}, true
		}
	}
	c.enqueue(url)
	_, isFailed := c.failed[url]
	return nil, time.Time{}, isFailed
}

// Prefetch starts loading pictures about to show, as the next page of a
// shelf.
func (c *imageCache) Prefetch(urls ...string) {
	for _, u := range urls {
		c.Lookup(u)
	}
}

// ForgetFailures lets failed pictures be tried again at once, as the user
// refreshes.
func (c *imageCache) ForgetFailures() {
	clear(c.failed)
}

// enqueue asks a worker to load url, unless one has been asked.
func (c *imageCache) enqueue(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.pending[url] {
		return
	}
	if !c.started {
		c.started = true
		for range imageWorkers {
			go c.work()
		}
	}
	c.pending[url] = true
	c.stack = append(c.stack, url)
	if len(c.stack) > imageQueueMax {
		// Asked for long ago and not since: it scrolled away. Asked for
		// again, it queues again.
		old := c.stack[0]
		c.stack = c.stack[1:]
		delete(c.pending, old)
	}
	c.cond.Signal()
}

// work loads the URLs asked for last first, until the cache closes.
func (c *imageCache) work() {
	for {
		c.mu.Lock()
		for len(c.stack) == 0 && !c.closed {
			c.cond.Wait()
		}
		if c.closed {
			c.mu.Unlock()
			return
		}
		url := c.stack[len(c.stack)-1]
		c.stack = c.stack[:len(c.stack)-1]
		c.active++
		c.mu.Unlock()

		bmp, err := c.load(url)

		c.mu.Lock()
		c.active--
		c.mu.Unlock()
		c.post(func() { c.finish(url, bmp, err) })
	}
}

// finish keeps what a worker loaded, on the main thread.
func (c *imageCache) finish(url string, bmp *ui.Bitmap, err error) {
	c.mu.Lock()
	delete(c.pending, url)
	c.mu.Unlock()
	if err != nil {
		f := c.failed[url]
		f.tries++
		backoff := min(30*time.Second<<(f.tries-1), 10*time.Minute)
		f.retry = c.now().Add(backoff)
		c.failed[url] = f
		return
	}
	delete(c.failed, url)
	if old := c.entries[url]; old != nil {
		c.remove(old)
	}
	w, h := bmp.Size()
	e := &imageEntry{url: url, bmp: bmp, cost: int64(w) * int64(h) * 4, loadedAt: c.now()}
	e.elem = c.lru.PushFront(e)
	c.entries[url] = e
	c.bytes += e.cost
	c.evict()
	if c.onMain != nil {
		// Shown by the frame that post draws, it drops its pixels on the
		// CPU: the GPU keeps its texture, and its data decodes it again if
		// it is needed.
		onMain := c.onMain
		time.AfterFunc(imageRelease, func() { onMain(bmp.ReleasePixels) })
	}
}

// evict drops the pictures asked for least recently while the cache holds
// more than its budget. Those showing were asked for in the last frame, and
// stay.
func (c *imageCache) evict() {
	for c.bytes > c.limit && c.lru.Len() > 1 {
		c.remove(c.lru.Back().Value.(*imageEntry))
	}
}

func (c *imageCache) remove(e *imageEntry) {
	c.lru.Remove(e.elem)
	delete(c.entries, e.url)
	c.bytes -= e.cost
}

// load returns the picture at url, from disk or the network, decoded.
func (c *imageCache) load(url string) (*ui.Bitmap, error) {
	path := c.diskPath(url)
	if path != "" {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			if bmp, err := ui.DecodeBitmap(data); err == nil {
				now := time.Now()
				_ = os.Chtimes(path, now, now) // recently used, for trimming
				return bmp, nil
			}
			_ = os.Remove(path) // damaged: fetch it again
		}
	}
	var data []byte
	var err error
	for try := range imageTries {
		if try > 0 {
			time.Sleep(time.Duration(try) * 600 * time.Millisecond)
		}
		data, err = c.fetch(url)
		if err == nil || !transient(err) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	bmp, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil, fmt.Errorf("decoding %s: %w", url, err)
	}
	if path != "" {
		_ = writeFileAtomic(path, data)
	}
	return bmp, nil
}

func (c *imageCache) fetch(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), imageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/webp,image/jpeg,image/png,image/*")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, &statusError{url, resp.StatusCode, resp.Status}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, imageMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > imageMaxBytes {
		return nil, &statusError{url, http.StatusRequestEntityTooLarge, "larger than allowed"}
	}
	if len(data) == 0 {
		return nil, &statusError{url, http.StatusNoContent, "empty picture"}
	}
	return data, nil
}

// diskPath is where the picture at url is kept on disk, "" without a disk
// cache.
func (c *imageCache) diskPath(url string) string {
	if c.dir == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(url))
	name := hex.EncodeToString(sum[:16])
	return filepath.Join(c.dir, name[:2], name)
}

// Close stops the workers. Loads under way finish, and their results are
// posted as usual.
func (c *imageCache) Close() {
	c.mu.Lock()
	c.closed = true
	c.stack = nil
	c.mu.Unlock()
	c.cond.Broadcast()
}

// DiskUsage returns the bytes the disk cache takes. It reads the disk: call
// it off the main thread.
func (c *imageCache) DiskUsage() int64 {
	var n int64
	for _, f := range c.diskFiles() {
		n += f.size
	}
	return n
}

// ClearDisk empties the disk cache. Call it off the main thread.
func (c *imageCache) ClearDisk() error {
	if c.dir == "" {
		return nil
	}
	return os.RemoveAll(c.dir)
}

// TrimDisk drops the pictures used least recently until the disk cache
// takes at most max bytes. Call it off the main thread.
func (c *imageCache) TrimDisk(max int64) {
	files := c.diskFiles()
	var total int64
	for _, f := range files {
		total += f.size
	}
	slices.SortFunc(files, func(x, y diskFile) int { return x.mod.Compare(y.mod) })
	for _, f := range files {
		if total <= max {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}

type diskFile struct {
	path string
	size int64
	mod  time.Time
}

func (c *imageCache) diskFiles() []diskFile {
	if c.dir == "" {
		return nil
	}
	var files []diskFile
	_ = filepath.WalkDir(c.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			files = append(files, diskFile{path, info.Size(), info.ModTime()})
		}
		return nil
	})
	return files
}

// writeFileAtomic writes data to path through a temporary file renamed over
// it, so that a reader never sees half a file.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// imageSizes are the widths pictures are asked for at: a few, so that a
// picture shown at slightly different sizes, as a window is resized, is one
// entry of the caches rather than many.
var imageSizes = []int{160, 240, 320, 400, 480, 640, 800, 960, 1280, 1600, 1920, 2560, 3200, 3840}

// imagePx is how many pixels wide to ask a server for a picture shown dips
// wide, at the screen's scale: the first of imageSizes that is as wide.
func imagePx(dips, scale float32) int {
	if scale < 1 {
		scale = 1
	}
	px := int(dips*scale + 0.5)
	for _, s := range imageSizes {
		if s >= px {
			return s
		}
	}
	return imageSizes[len(imageSizes)-1]
}
