// Command remuxdemo plays one Emby item through internal/remux in a MyGo
// window and measures it: time to the first picture, seek recovery, CPU,
// memory and upstream traffic.
//
//	EMBY_TEST_SERVER=… EMBY_TEST_USER=… EMBY_TEST_PASS=… \
//	  go run ./cmd/remuxdemo -item <id> [-audio N] [-start 600] \
//	  [-seek 50%,90%] [-play 15s] [-shots dir] [-force]
//
// It plays from -start (seconds), then seeks to each -seek target (a
// percentage of the length or seconds), playing -play at each place, and
// quits. -probe only prints what Decide and the session found. -headless
// plays no video: it reads the segments the way AVPlayer would, one after
// another for -play of media time, to measure the remuxer alone.
//
// It never reports playback to the server, so watch history is left alone,
// reads only what the player asks for, and signs out at the end. The
// password comes from the environment only.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/remux"
)

var (
	itemID   = flag.String("item", "", "Emby item ID")
	audio    = flag.Int("audio", -1, "audio MediaStream index (-1: default)")
	startSec = flag.Float64("start", 0, "start position in seconds")
	seeks    = flag.String("seek", "50%,90%", "seek targets: percentages or seconds, comma separated")
	playFor  = flag.Duration("play", 15*time.Second, "how long to play at each place")
	shots    = flag.String("shots", "", "directory for a screenshot at each place")
	force    = flag.Bool("force", false, "remux even when AVPlayer could play the file")
	probe    = flag.Bool("probe", false, "print the decision and tracks, then quit")
	headless = flag.Bool("headless", false, "read segments without a window, for measurements")
)

type demo struct {
	client *emby.Client
	srv    *remux.Server
	sess   *remux.Session
	pb     *emby.Playback
	win    *mygo.Window
	player *ui.VideoPlayer
	t0     time.Time // before Open

	mu   sync.Mutex
	cues map[int][]emby.Cue
	sub  int // subtitle track shown

	status string
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	if *itemID == "" {
		log.Fatal("-item is required")
	}
	server, user := os.Getenv("EMBY_TEST_SERVER"), os.Getenv("EMBY_TEST_USER")
	if server == "" || user == "" {
		log.Fatal("set EMBY_TEST_SERVER, EMBY_TEST_USER and EMBY_TEST_PASS")
	}
	d := &demo{cues: map[int][]emby.Cue{}, sub: -1000}
	ctx := context.Background()
	c, err := emby.NewClient(server, emby.DeviceInfo{Client: "MyGo Emby remuxdemo", Device: "Mac", DeviceID: emby.NewDeviceID(), Version: "0.1.0"})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := c.Login(ctx, user, os.Getenv("EMBY_TEST_PASS")); err != nil {
		log.Fatal(err)
	}
	d.client = c
	defer d.logout()

	pb, err := c.PlaybackInfo(ctx, *itemID, emby.PlaybackOptions{SubtitleStreamIndex: emby.Index(-1)})
	if err != nil {
		d.fatal(err)
	}
	d.pb = pb
	src := &pb.Source
	dec := remux.Decide(src, *audio)
	log.Printf("item %s: container %s, %.1f Mbps, %v; decision %v (%s) audio %d unsupported=%v",
		*itemID, src.Container, float64(src.Bitrate)/1e6, pb.Runtime().Round(time.Second), dec.Mode, dec.Reason, dec.AudioIndex, dec.AudioUnsupported)
	if dec.Mode == remux.Unsupported {
		d.fatal(fmt.Errorf("unsupported: %s", dec.Reason))
	}
	if dec.Mode == remux.DirectPlay && !*force && !*probe {
		log.Printf("AVPlayer plays this file directly; use -force to remux it anyway")
	}
	d.srv, err = remux.NewServer(remux.WithLogf(log.Printf))
	if err != nil {
		d.fatal(err)
	}
	start := time.Duration(*startSec * float64(time.Second))
	d.t0 = time.Now()
	d.sess, err = d.srv.Open(ctx, remux.Source{
		URL:         pb.StreamURL,
		MediaSource: src,
		AudioIndex:  dec.AudioIndex,
		NoAudio:     dec.AudioUnsupported,
		Duration:    pb.Runtime(),
		Start:       start,
		OnCues:      d.onCues,
	})
	if err != nil {
		d.fatal(err)
	}
	log.Printf("session open in %v: %v, audio %d, %s", time.Since(d.t0).Round(time.Millisecond), d.sess.Duration.Round(time.Second), d.sess.AudioIndex, d.sess.PlaylistURL)
	for _, t := range d.sess.Tracks {
		log.Printf("  track %-8s #%-3d %-18s %-4s %-24q default=%v playing=%v supported=%v %s", t.Kind, t.Index, t.Codec, t.Language, t.Title, t.Default, t.Playing, t.Supported, t.Reason)
		if t.Kind == remux.KindSubtitle && t.Supported && d.sub == -1000 {
			d.sub = t.Index
		}
	}
	if *probe {
		d.dumpPlaylist()
		d.finish(0)
		return
	}
	if *headless {
		d.runHeadless(start)
		d.finish(0)
		return
	}
	mygo.App.WhenReady(func() {
		d.win = mygo.NewWindow(mygo.WindowOptions{Title: "remuxdemo", Width: 1280, Height: 760, Content: ui.View(d.view)})
		p, err := video.OpenURL(d.sess.PlaylistURL)
		if err != nil {
			d.fatal(err)
		}
		d.player = p
		if start > 0 {
			p.Seek(start)
		}
		go d.script(start)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func (d *demo) onCues(track int, cues []emby.Cue) {
	d.mu.Lock()
	d.cues[track] = append(d.cues[track], cues...)
	d.mu.Unlock()
}

func (d *demo) cueAt(t time.Duration) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var lines []string
	for _, c := range d.cues[d.sub] {
		if c.Start <= t && t < c.End {
			lines = append(lines, c.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func (d *demo) view(c *ui.Context) {
	white := ui.RGB(255, 255, 255)
	ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Children(func() {
		if d.player != nil {
			ui.Video(c, d.player).Fill().Fit(ui.Contain)
			p := d.player
			info := fmt.Sprintf("%s / %s  ready=%v buffering=%v  %s", clock(p.Position()), clock(p.Duration()), p.Ready(), p.Buffering(), d.status)
			if err := p.Err(); err != nil {
				info += "  error: " + err.Error()
			}
			ui.Text(c, info).TextColor(white).FontFeatures("tnum").Absolute().Padding(12).Background(ui.RGBA(0, 0, 0, 160))
			if text := d.cueAt(p.Position()); text != "" {
				ui.Box(c).Absolute().Fill().Center().Children(func() {
					ui.Text(c, text).TextColor(white).FontSize(28).Padding(8).Background(ui.RGBA(0, 0, 0, 140))
				})
			}
		}
	})
}

// onMain runs fn on the main thread and waits for its result.
func (d *demo) onMain(fn func()) {
	done := make(chan struct{})
	d.win.Update(func() {
		fn()
		close(done)
	})
	<-done
}

type sample struct {
	pos        time.Duration
	ready, buf bool
	err        error
}

func (d *demo) state() sample {
	var s sample
	d.onMain(func() {
		p := d.player
		s = sample{p.Position(), p.Ready(), p.Buffering(), p.Err()}
	})
	return s
}

// waitPlaying waits until the picture shows and the position passed from.
func (d *demo) waitPlaying(from time.Duration, limit time.Duration) (time.Duration, error) {
	t := time.Now()
	for time.Since(t) < limit {
		s := d.state()
		if s.err != nil {
			return 0, s.err
		}
		if s.ready && !s.buf && s.pos > from+300*time.Millisecond {
			return time.Since(t), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, fmt.Errorf("not playing after %v", limit)
}

func (d *demo) script(start time.Duration) {
	code := 0
	defer func() { d.finish(code) }()
	stop := make(chan struct{})
	defer close(stop)
	go d.watchCPU(stop)

	ready := time.Now()
	for {
		s := d.state()
		if s.err != nil {
			log.Printf("FAIL: player error %v", s.err)
			code = 1
			return
		}
		if s.ready {
			break
		}
		if time.Since(ready) > 60*time.Second {
			log.Printf("FAIL: not ready after 60 s")
			code = 1
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	log.Printf("RESULT first picture: %v after Open began (%v after the window asked)", time.Since(d.t0).Round(time.Millisecond), time.Since(ready).Round(time.Millisecond))
	if _, err := d.waitPlaying(start, 60*time.Second); err != nil {
		log.Printf("FAIL: %v", err)
		code = 1
		return
	}
	d.playAt("start", start)

	dur := d.sess.Duration
	for _, s := range strings.Split(*seeks, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var target time.Duration
		if strings.HasSuffix(s, "%") {
			pct, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
			target = time.Duration(float64(dur) * pct / 100)
		} else {
			sec, _ := strconv.ParseFloat(s, 64)
			target = time.Duration(sec * float64(time.Second))
		}
		d.status = "seek " + s
		d.onMain(func() { d.player.Seek(target) })
		took, err := d.waitPlaying(target, 60*time.Second)
		if err != nil {
			log.Printf("FAIL: after seeking to %v: %v", target, err)
			code = 1
			return
		}
		log.Printf("RESULT seek to %s (%v): playing again after %v", s, target.Round(time.Second), took.Round(time.Millisecond))
		d.playAt("seek "+s, target)
	}
}

// playAt plays for -play and checks that the position follows the clock.
func (d *demo) playAt(name string, at time.Duration) {
	st0 := d.sess.Stats()
	s0, t0 := d.state(), time.Now()
	time.Sleep(*playFor / 2)
	if *shots != "" {
		d.shot(name)
	}
	time.Sleep(*playFor / 2)
	s1, wall := d.state(), time.Since(t0)
	st1 := d.sess.Stats()
	adv := s1.pos - s0.pos
	mb := float64(st1.UpstreamBytes-st0.UpstreamBytes) / 1e6
	log.Printf("RESULT %s: position %v → %v (advanced %.1fs in %.1fs wall, buffering=%v); upstream %.1f MB (%.1f Mbps over the wall time), %d requests",
		name, s0.pos.Round(100*time.Millisecond), s1.pos.Round(100*time.Millisecond), adv.Seconds(), wall.Seconds(), s1.buf,
		mb, mb*8/wall.Seconds(), st1.UpstreamRequests-st0.UpstreamRequests)
	d.mu.Lock()
	n := len(d.cues[d.sub])
	d.mu.Unlock()
	log.Printf("RESULT subtitle cues so far (track %d): %d; on screen: %q", d.sub, n, d.cueAt(s1.pos))
}

func (d *demo) shot(name string) {
	var png []byte
	var err error
	d.onMain(func() { png, err = d.win.CapturePage() })
	if err != nil {
		log.Printf("screenshot: %v", err)
		return
	}
	_ = os.MkdirAll(*shots, 0o755)
	safe := regexp.MustCompile(`[^a-zA-Z0-9]+`).ReplaceAllString(name, "_")
	f := filepath.Join(*shots, *itemID+"_"+safe+".png")
	if err := os.WriteFile(f, png, 0o644); err == nil {
		log.Printf("screenshot %s", f)
	}
}

// watchCPU logs the process's CPU and memory every 5 s.
func (d *demo) watchCPU(stop chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	pid := strconv.Itoa(os.Getpid())
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		out, err := exec.Command("ps", "-o", "%cpu=,rss=", "-p", pid).Output()
		if err != nil {
			continue
		}
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		log.Printf("CPU/RSS (ps): %s; Go heap in use %.1f MB", strings.TrimSpace(string(out)), float64(ms.HeapInuse)/1e6)
	}
}

// runHeadless reads segments from start like AVPlayer would, as fast as
// they come, for -play of media time, and reports the CPU time used.
func (d *demo) runHeadless(start time.Duration) {
	base := strings.TrimSuffix(d.sess.PlaylistURL, "master.m3u8")
	media := string(mustGet(base + "media.m3u8"))
	var durs []float64
	for _, l := range strings.Split(media, "\n") {
		if strings.HasPrefix(l, "#EXTINF:") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
			durs = append(durs, v)
		}
	}
	mustGet(base + "init.mp4")
	k, acc := 0, 0.0
	for k < len(durs) && acc+durs[k] <= start.Seconds() {
		acc += durs[k]
		k++
	}
	cpu0 := cpuTime()
	t0 := time.Now()
	media0 := acc
	var bytes int
	for ; k < len(durs) && acc-media0 < playFor.Seconds(); k++ {
		ts := time.Now()
		b := mustGet(fmt.Sprintf("%s%d.m4s", base, k))
		bytes += len(b)
		acc += durs[k]
		log.Printf("segment %d: %d KB in %v", k, len(b)>>10, time.Since(ts).Round(time.Millisecond))
	}
	wall := time.Since(t0)
	cpu := cpuTime() - cpu0
	st := d.sess.Stats()
	log.Printf("RESULT headless: %.1f s of media in %.1f s wall (%.1f× real time), CPU %.2f s (%.2f%% of one core per media second), served %.1f MB, upstream %.1f MB in %d requests",
		acc-media0, wall.Seconds(), (acc-media0)/wall.Seconds(), cpu.Seconds(), 100*cpu.Seconds()/(acc-media0), float64(bytes)/1e6, float64(st.UpstreamBytes)/1e6, st.UpstreamRequests)
}

func cpuTime() time.Duration {
	out, err := exec.Command("ps", "-o", "time=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return 0
	}
	// [[hh:]mm:]ss.cc
	parts := strings.Split(strings.TrimSpace(string(out)), ":")
	var secs float64
	for _, p := range parts {
		v, _ := strconv.ParseFloat(p, 64)
		secs = secs*60 + v
	}
	return time.Duration(secs * float64(time.Second))
}

func mustGet(url string) []byte {
	resp, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != 200 {
		log.Fatalf("%s: %v %s", url, err, resp.Status)
	}
	return b
}

func (d *demo) dumpPlaylist() {
	base := strings.TrimSuffix(d.sess.PlaylistURL, "master.m3u8")
	fmt.Print(string(mustGet(d.sess.PlaylistURL)))
	media := strings.Split(string(mustGet(base+"media.m3u8")), "\n")
	var durs []float64
	for _, l := range media {
		if strings.HasPrefix(l, "#EXTINF:") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
			durs = append(durs, v)
		}
	}
	sort.Float64s(durs)
	if n := len(durs); n > 0 {
		fmt.Printf("%d segments, EXTINF min %.2f median %.2f max %.2f\n", n, durs[0], durs[n/2], durs[n-1])
	}
	for _, l := range media[:min(len(media), 12)] {
		fmt.Println(l)
	}
}

func (d *demo) finish(code int) {
	if d.player != nil && d.win != nil {
		d.onMain(func() {
			d.player.Close()
			d.player = nil // the view must not touch a closed player
		})
	}
	if d.sess != nil {
		st := d.sess.Stats()
		log.Printf("RESULT totals: upstream %.1f MB in %d requests, %d segments made, %.1f MB served", float64(st.UpstreamBytes)/1e6, st.UpstreamRequests, st.Segments, float64(st.Served)/1e6)
		d.sess.Close()
	}
	if d.srv != nil {
		d.srv.Close()
	}
	d.logout()
	if d.win != nil {
		d.win.Update(func() { mygo.App.Quit() })
		return
	}
	os.Exit(code)
}

func (d *demo) logout() {
	if d.client != nil && d.client.LoggedIn() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.client.Logout(ctx); err != nil {
			log.Printf("logout: %v", err)
		} else {
			log.Printf("signed out")
		}
	}
}

func (d *demo) fatal(err error) {
	log.Print(err)
	if d.sess != nil {
		d.sess.Close()
	}
	d.logout()
	os.Exit(1)
}

func clock(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}
