// Command playerdemo plays one item with internal/player in a window of its
// own, for debugging the player without the app.
//
//	go run ./cmd/playerdemo [item-id]
//
// It signs in to PLAYER_SERVER as PLAYER_USER with PLAYER_PASS, else to
// EMBY_TEST_SERVER as EMBY_TEST_USER with EMBY_TEST_PASS, else to the
// Jellyfin demo server as demo. Without an item ID it picks the newest movie
// AVPlayer plays as it is (MP4 or MOV, H.264 or HEVC tagged hvc1).
//
// On any server but the Jellyfin demo, which is somebody's real library,
// it notes the item's played state and resume point before playing and
// puts them back afterwards (PLAYER_RESTORE=0 leaves them), then signs
// out.
//
// Other settings:
//
//	PLAYER_FROM_START=1   play from the start rather than the resume point
//	PLAYER_START=600      play from 600 s, as from a resume point
//	PLAYER_SECONDS=40     quit after so many seconds of playback (0 = never)
//	PLAYER_STATS=1        play through the phases of the frame statistics
//	                      (with MYGO_FRAME_STATS=all), then quit
//	PLAYER_SHOTS=<dir>    walk through the player's states, saving a PNG
//	                      of the window for each, then quit
//	PLAYER_ERROR_ITEM=id  the item the screenshots show the error panel
//	                      with (default: the newest MKV movie)
//	PLAYER_AUDIO_LANG, PLAYER_SUB_LANG   preferred languages
//
// Every second it logs whether the video is ready and where it stands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/player"
	"mygo-emby/internal/remux"
)

const jellyfinDemo = "https://demo.jellyfin.org/stable"

type demo struct {
	client  *emby.Client
	win     *mygo.Window
	pl      *player.Player
	item    *emby.Item
	errItem *emby.Item
	restore bool
	saved   *emby.UserData
	quitAt  time.Time
	shots   string
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	d := &demo{shots: os.Getenv("PLAYER_SHOTS")}
	server, user, pass := env("PLAYER_SERVER", "EMBY_TEST_SERVER"), env("PLAYER_USER", "EMBY_TEST_USER"), env("PLAYER_PASS", "EMBY_TEST_PASS")
	if server == "" {
		server, user, pass = jellyfinDemo, "demo", ""
	}
	d.restore = server != jellyfinDemo && os.Getenv("PLAYER_RESTORE") != "0"

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := emby.NewClient(server, emby.DeviceInfo{Client: "MyGo Emby", Device: "playerdemo", DeviceID: "mygo-emby-playerdemo-1", Version: "0.1.0"})
	if err != nil {
		log.Fatal(err)
	}
	if _, err := c.Login(ctx, user, pass); err != nil {
		log.Fatalf("signing in to %s: %v", c.BaseURL(), err)
	}
	d.client = c
	log.Printf("signed in to %s (%s)", c.BaseURL(), c.Session().ServerKind)
	if id := flag.Arg(0); id != "" {
		d.item, err = c.Item(ctx, id)
	} else {
		d.item, err = pickPlayable(ctx, c)
	}
	if err != nil {
		d.logout()
		log.Fatal(err)
	}
	log.Printf("item %s %q (%s)", d.item.ID, d.item.DisplayTitle(), d.item.Type)
	if d.restore {
		d.saved = userData(ctx, c, d.item.ID)
	}
	if d.shots != "" {
		if id := os.Getenv("PLAYER_ERROR_ITEM"); id != "" {
			d.errItem, _ = c.Item(ctx, id)
		} else {
			d.errItem = pickMKV(ctx, c)
		}
		if d.errItem != nil && d.restore {
			log.Printf("error item %s %q", d.errItem.ID, d.errItem.Name)
		}
	}
	if s, err := strconv.Atoi(os.Getenv("PLAYER_SECONDS")); err == nil && s > 0 {
		d.quitAt = time.Now().Add(time.Duration(s) * time.Second)
	}

	mygo.App.WhenReady(d.start)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { d.finish() })
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func env(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func (d *demo) start() {
	d.win = mygo.NewWindow(mygo.WindowOptions{
		Title:                "MyGo Emby Player",
		Width:                1280,
		Height:               820,
		MinWidth:             640,
		MinHeight:            400,
		TitleBarStyle:        mygo.TitleBarHidden,
		TrafficLightPosition: &mygo.Point{X: 20, Y: 20},
		BackgroundColor:      "#000000",
		Content:              ui.View(d.view),
	})
	// A window that other windows cover takes no frames of its video
	// (ui/video holds them until some of it shows): come to the front.
	d.win.Focus()
	srv, err := remux.NewServer()
	if err != nil {
		log.Fatalf("remux: %v", err)
	}
	d.pl = player.New(player.Config{
		Client:       d.client,
		Source:       &player.ServerSource{Client: d.client, Fallback: player.RemuxFallback(srv)},
		Window:       d.win,
		Post:         d.win.Update,
		AudioLang:    os.Getenv("PLAYER_AUDIO_LANG"),
		SubtitleLang: os.Getenv("PLAYER_SUB_LANG"),
		AutoNext:     true,
		Volume:       0.6,
		OnExit: func(r player.Result) {
			log.Printf("exit: %+v", r)
			if d.shots == "" {
				mygo.App.Quit()
			}
		},
	})
	if sec, err := strconv.ParseFloat(os.Getenv("PLAYER_START"), 64); err == nil && sec > 0 {
		// Start there, as from a resume point (the server's is left alone).
		u := emby.UserData{}
		if d.item.UserData != nil {
			u = *d.item.UserData
		}
		u.PlaybackPositionTicks = emby.DurationToTicks(time.Duration(sec * float64(time.Second)))
		d.item.UserData = &u
	}
	d.pl.Play(d.item, os.Getenv("PLAYER_FROM_START") == "1")
	go d.watch()
	if d.shots != "" {
		go d.runShots()
	} else if os.Getenv("PLAYER_STATS") == "1" {
		go d.runStats()
	}
}

// runStats plays through the phases the frame statistics are measured in
// (MYGO_FRAME_STATS=all): controls hidden, controls shown, a subtitle
// changing every two seconds, paused; it logs each phase's start, then
// quits.
func (d *demo) runStats() {
	do := func(actions ...string) {
		mygo.RunOnMain(func() {
			for _, a := range actions {
				if err := d.pl.Debug(a); err != nil {
					log.Print(err)
				}
			}
			d.win.Invalidate()
		})
	}
	ready := func() (ok bool) {
		done := make(chan struct{})
		mygo.RunOnMain(func() { ok = d.pl.Status().Ready; close(done) })
		<-done
		return ok
	}
	for deadline := time.Now().Add(150 * time.Second); !ready() && time.Now().Before(deadline); {
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	phase := func(name string) { log.Printf("PHASE %s", name) }
	if os.Getenv("PLAYER_STATS_PAUSE_ONLY") == "1" {
		phase("paused")
		do("pause")
		time.Sleep(12 * time.Second)
		phase("end")
		mygo.RunOnMain(mygo.App.Quit)
		return
	}
	phase("hidden")
	do("hide-controls")
	time.Sleep(20 * time.Second)
	phase("shown")
	for range 20 {
		do("show-controls")
		time.Sleep(time.Second)
	}
	phase("subtitle")
	do("sample-subtitle", "hide-controls")
	time.Sleep(20 * time.Second)
	phase("paused")
	do("pause")
	time.Sleep(10 * time.Second)
	phase("end")
	mygo.RunOnMain(mygo.App.Quit)
}

func (d *demo) view(c *ui.Context) {
	if d.pl.Active() {
		d.pl.View(c)
		return
	}
	ui.Column(c).Fill().Center().Background(ui.RGB(0, 0, 0)).Children(func() {
		ui.Text(c, "播放器已关闭").TextColor(ui.RGB(255, 255, 255))
	})
}

// watch logs the playback every second, and quits after PLAYER_SECONDS.
func (d *demo) watch() {
	for range time.Tick(time.Second) {
		mygo.RunOnMain(func() {
			s := d.pl.Status()
			log.Printf("status: active=%v loading=%v ready=%v playing=%v buffering=%v pos=%s engine=%s dur=%s method=%s err=%q engineReady=%v builds=%d",
				s.Active, s.Loading, s.Ready, s.Playing, s.Buffering, s.Position.Round(100*time.Millisecond),
				s.EnginePosition.Round(100*time.Millisecond), s.Duration.Round(time.Second), s.Method, s.ErrorText,
				s.EngineReady, s.VideoBuilds)
			if !d.quitAt.IsZero() && time.Now().After(d.quitAt) && d.shots == "" {
				d.quitAt = time.Time{}
				mygo.App.Quit()
			}
		})
	}
}

// finish stops playback, waits for the reports, puts the item's user data
// back and signs out.
func (d *demo) finish() {
	if d.pl != nil {
		d.pl.Close()
		if !d.pl.Flush(8 * time.Second) {
			log.Printf("reports still pending at quit")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if d.restore && d.saved != nil {
		restoreUserData(ctx, d.client, d.item.ID, d.saved)
	}
	d.logout()
}

func (d *demo) logout() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := d.client.Logout(ctx); err != nil {
		log.Printf("signing out: %v", err)
	} else {
		log.Printf("signed out")
	}
}

func userData(ctx context.Context, c *emby.Client, id string) *emby.UserData {
	it, err := c.Item(ctx, id)
	if err != nil || it.UserData == nil {
		log.Printf("user data of %s: %v", id, err)
		return nil
	}
	u := *it.UserData
	log.Printf("user data: played=%v playCount=%d position=%d", u.Played, u.PlayCount, u.PlaybackPositionTicks)
	return &u
}

// restoreUserData puts back the played mark, the play count (marking
// unplayed resets it on this server) and the resume point (a stopped
// report at the old position).
func restoreUserData(ctx context.Context, c *emby.Client, id string, u *emby.UserData) {
	r := emby.PlaybackReport{ItemID: id, MediaSourceID: "", PositionTicks: u.PlaybackPositionTicks, CanSeek: true, VolumeLevel: 100}
	if err := c.ReportPlaybackStopped(ctx, r); err != nil {
		log.Printf("restoring the resume point: %v", err)
	}
	if _, err := c.SetPlayed(ctx, id, u.Played); err != nil {
		log.Printf("restoring the played mark: %v", err)
	}
	if after := userData(ctx, c, id); after != nil {
		ok := after.Played == u.Played && after.PlayCount == u.PlayCount && after.PlaybackPositionTicks == u.PlaybackPositionTicks
		log.Printf("user data restored: %v (played=%v playCount=%d position=%d)", ok, after.Played, after.PlayCount, after.PlaybackPositionTicks)
	}
}

// pickPlayable returns the newest movie whose file AVPlayer plays.
func pickPlayable(ctx context.Context, c *emby.Client) (*emby.Item, error) {
	res, err := c.Items(ctx, emby.ItemQuery{
		IncludeItemTypes: []string{emby.TypeMovie}, Recursive: true,
		SortBy: []string{"DateCreated"}, SortOrder: emby.Descending, Limit: 120,
		Fields: []string{"MediaSources"},
	})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		it := &res.Items[i]
		if len(it.MediaSources) > 0 && nativeFile(&it.MediaSources[0]) {
			return c.Item(ctx, it.ID)
		}
	}
	return nil, errors.New("no movie AVPlayer plays as it is among the newest")
}

// pickMKV returns the newest Matroska movie, for the error panel.
func pickMKV(ctx context.Context, c *emby.Client) *emby.Item {
	res, err := c.Items(ctx, emby.ItemQuery{
		IncludeItemTypes: []string{emby.TypeMovie}, Recursive: true,
		SortBy: []string{"DateCreated"}, SortOrder: emby.Descending, Limit: 60,
		Fields: []string{"MediaSources"},
	})
	if err != nil {
		return nil
	}
	for i := range res.Items {
		it := &res.Items[i]
		if len(it.MediaSources) > 0 && strings.Contains(it.MediaSources[0].Container, "mkv") || len(it.MediaSources) > 0 && it.MediaSources[0].Container == "matroska" {
			return it
		}
	}
	return nil
}

func nativeFile(m *emby.MediaSource) bool {
	switch strings.ToLower(m.Container) {
	case "mp4", "m4v", "mov":
	default:
		return false
	}
	v := m.VideoStream()
	if v == nil {
		return false
	}
	switch strings.ToLower(v.Codec) {
	case "h264":
		return v.BitDepth <= 8
	case "hevc":
		return !strings.EqualFold(v.CodecTag, "hev1")
	}
	return false
}

// runShots walks the player through its states, saving the window after
// each, then quits.
func (d *demo) runShots() {
	dir := d.shots
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("shots: %v", err)
	}
	shot := func(name string) {
		png, err := d.win.CapturePage()
		if err != nil {
			log.Printf("shots: %s: %v", name, err)
			return
		}
		path := filepath.Join(dir, name+".png")
		if err := os.WriteFile(path, png, 0o644); err != nil {
			log.Printf("shots: %v", err)
			return
		}
		log.Printf("shot %s", path)
	}
	do := func(action string) {
		mygo.RunOnMain(func() {
			if err := d.pl.Debug(action); err != nil {
				log.Print(err)
			}
			d.win.Invalidate()
		})
	}
	status := func() (s player.Status) {
		done := make(chan struct{})
		mygo.RunOnMain(func() { s = d.pl.Status(); close(done) })
		<-done
		return s
	}
	time.Sleep(700 * time.Millisecond)
	shot("01-loading")
	deadline := time.Now().Add(40 * time.Second)
	for !status().Ready && !status().Error && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	do("show-controls")
	time.Sleep(600 * time.Millisecond)
	shot("02-playing-controls")
	do("hide-controls")
	time.Sleep(900 * time.Millisecond)
	shot("03-controls-hidden")
	do("sample-tracks")
	do("pause")
	time.Sleep(700 * time.Millisecond)
	shot("04-paused")
	do("menu-audio")
	time.Sleep(700 * time.Millisecond)
	shot("05-menu-audio")
	do("close-menus")
	do("menu-subtitles")
	time.Sleep(700 * time.Millisecond)
	shot("06-menu-subtitles")
	do("close-menus")
	do("menu-speed")
	time.Sleep(700 * time.Millisecond)
	shot("07-menu-speed")
	do("close-menus")
	do("play")
	do("sample-subtitle")
	do("hide-controls")
	time.Sleep(900 * time.Millisecond)
	shot("08-subtitle")
	do("show-controls")
	time.Sleep(600 * time.Millisecond)
	shot("09-subtitle-controls")
	do("up-next")
	time.Sleep(700 * time.Millisecond)
	shot("10-up-next")
	mygo.RunOnMain(func() { d.win.SetSize(960, 620) })
	time.Sleep(900 * time.Millisecond)
	shot("11-small-window")
	mygo.RunOnMain(func() { d.win.SetSize(1280, 820) })
	if d.errItem != nil {
		mygo.RunOnMain(func() { d.pl.Play(d.errItem, true) })
		deadline := time.Now().Add(30 * time.Second)
		for !status().Error && time.Now().Before(deadline) {
			time.Sleep(250 * time.Millisecond)
		}
		time.Sleep(600 * time.Millisecond)
		shot("12-error")
	} else {
		log.Printf("shots: no item for the error panel")
	}
	fmt.Fprintln(os.Stderr, "shots done")
	mygo.RunOnMain(mygo.App.Quit)
}
