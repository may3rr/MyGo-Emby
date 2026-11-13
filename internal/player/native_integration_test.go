//go:build darwin

package player

import (
	"context"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/remux"
)

// Native tests opt in because they create an actual AppKit window. Run with a
// local HDR fixture of at least 30 seconds, e.g.:
// MYGO_NATIVE_TEST_FILE=/tmp/hdr.mp4 go test ./internal/player -run TestNativeWindowPlayback -count=1 -timeout=90s
func TestMain(m *testing.M) {
	if os.Getenv("MYGO_NATIVE_TEST_FILE") == "" {
		os.Exit(m.Run())
	}
	result := make(chan int, 1)
	mygo.App.WhenReady(func() {
		go func() {
			result <- m.Run()
			mygo.RunOnMain(mygo.App.Quit)
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(<-result)
}

func nativeWait(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		mygo.RunOnMain(func() { ok = ready() })
		if ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

type nativePoint struct{ X, Y float64 }

//go:linkname nativeAppKitClick github.com/egoist/mygo/internal/darwin.TestClick
func nativeAppKitClick(handle uintptr, x, y float64)

func nativeMouseEvent(w *mygo.Window, kind int, x, y float64) objc.ID {
	lib, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	var makeEvent func(objc.ID, objc.SEL, int, nativePoint, uint64, float64, int, objc.ID, int, int, float32) objc.ID
	purego.RegisterLibFunc(&makeEvent, lib, "objc_msgSend")
	nw := objc.ID(w.NativeHandle())
	num := nativeAV.getInt(nw, objc.RegisterName("windowNumber"))
	return makeEvent(objc.ID(objc.GetClass("NSEvent")), objc.RegisterName("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"),
		kind, nativePoint{x, y}, 0, 0, num, 0, 0, 1, 1)
}

// Send an actual AppKit event to our own window. This needs no accessibility
// permission and leaves the user's cursor and other apps alone.
func nativeMouse(w *mygo.Window, kind int, x, y float64) {
	avSend(objc.ID(w.NativeHandle()), "sendEvent:", nativeMouseEvent(w, kind, x, y))
}

func nativeHitView(w *mygo.Window, x, y float64) string {
	lib, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	var hitTest func(objc.ID, objc.SEL, nativePoint) objc.ID
	purego.RegisterLibFunc(&hitTest, lib, "objc_msgSend")
	nw := objc.ID(w.NativeHandle())
	hit := hitTest(avSend(nw, "contentView"), objc.RegisterName("hitTest:"), nativePoint{x, float64(w.Bounds().Height) - y})
	if hit == 0 {
		return ""
	}
	p := nativeAV.getCString(avSend(hit, "className"), objc.RegisterName("UTF8String"))
	if p == nil {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		ch := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if ch == 0 {
			break
		}
		b = append(b, ch)
	}
	return string(b)
}

func nativeWindowCapture(t *testing.T, w *mygo.Window) (int, int, int) {
	t.Helper()
	var number int
	mygo.RunOnMain(func() {
		number = nativeAV.getInt(objc.ID(w.NativeHandle()), objc.RegisterName("windowNumber"))
	})
	path := filepath.Join(t.TempDir(), "window.png")
	if output, err := exec.Command("screencapture", "-x", "-l", fmt.Sprint(number), path).CombinedOutput(); err != nil {
		t.Fatalf("capture window: %v: %s", err, output)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, _ := img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2).RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}

func TestNativeWindowPlayback(t *testing.T) {
	file := os.Getenv("MYGO_NATIVE_TEST_FILE")
	if file == "" {
		t.Skip("set MYGO_NATIVE_TEST_FILE for native AppKit playback checks")
	}
	absolute, err := filepath.Abs(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absolute); err != nil {
		t.Fatal(err)
	}
	fixtureURL := (&url.URL{Scheme: "file", Path: absolute}).String()
	playlist := false
	var remuxDuration time.Duration
	if strings.EqualFold(filepath.Ext(absolute), ".mkv") {
		// Exercise the app's actual MKV -> fMP4 HLS path, including Dolby
		// Vision codec tags and RPU, before native AVPlayerLayer presentation.
		files := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(absolute))))
		defer files.Close()
		mux, err := remux.NewServer()
		if err != nil {
			t.Fatal(err)
		}
		defer mux.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		sess, err := mux.Open(ctx, remux.Source{
			URL:        files.URL + "/" + url.PathEscape(filepath.Base(absolute)),
			AudioIndex: -1, AudioTranscode: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		remuxDuration = sess.Duration
		fixtureURL, playlist = sess.PlaylistURL, true
		t.Logf("MKV remux duration=%v", sess.Duration)
		if os.Getenv("MYGO_NATIVE_TEST_DV") == "1" {
			resp, err := http.Get(fixtureURL)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "dvh1.05.") || !strings.Contains(string(body), "VIDEO-RANGE=PQ") {
				t.Fatalf("Dolby Vision signaling lost: %s", body)
			}
			t.Log("remux preserved Dolby Vision profile 5 and PQ signaling")
		}
	}
	var p *Player
	var w *mygo.Window
	frames := 0
	pageFrames := 0
	mygo.RunOnMain(func() {
		w = mygo.NewWindow(mygo.WindowOptions{
			Title: "Native HDR integration", Width: 960, Height: 640,
			BackgroundColor: "#000000", TitleBarStyle: mygo.TitleBarHidden,
			Content: ui.View(func(c *ui.Context) {
				frames++
				if p != nil && p.Active() {
					p.View(c)
					return
				}
				pageFrames++
				ui.Box(c.Key("restored-page")).Fill().Background(ui.RGB(20, 180, 70))
			}),
		})
		p = New(Config{Window: w, Post: w.Update, Muted: true,
			Source: &fakeSource{answer: func(r Request) (*Stream, error) {
				st := directStream(r)
				st.URL, st.Playlist = fixtureURL, playlist
				return st, nil
			}},
		})
		mygo.App.Show()
		w.Show()
		w.Focus()
		p.Play(movie("native-hdr", 0), true)
	})
	defer mygo.RunOnMain(func() { p.Close(); w.Close() })
	lastLog := time.Time{}
	nativeWait(t, "first native picture", func() bool {
		s := p.Status()
		if time.Since(lastLog) > 2*time.Second {
			t.Logf("loading=%v ready=%v error=%q frames=%d builds=%d position=%s visible=%v occlusion=%v bounds=%+v", s.Loading, s.Ready, s.ErrorText, frames, s.VideoBuilds, s.EnginePosition, avSend(objc.ID(w.NativeHandle()), "isVisible"), avSend(objc.ID(w.NativeHandle()), "occlusionState"), w.Bounds())
			lastLog = time.Now()
		}
		return s.Ready || s.Error
	})
	var e *nativeEngine
	var problem string
	mygo.RunOnMain(func() {
		if p.Status().Error {
			problem = p.Status().ErrorText
			return
		}
		e, _ = p.cur.eng.(*nativeEngine)
		if e == nil {
			problem = "macOS did not select the native engine"
			return
		}
		if avSend(e.view, "superview") == 0 {
			problem = "native view was not embedded"
			return
		}
		if avSend(e.layer, "player") != e.player {
			problem = "AVPlayerLayer is not presenting AVPlayer"
			return
		}
		if avSend(avSend(e.item, "outputs"), "count") != 0 {
			problem = "playback still extracts pixel buffers"
			return
		}
		if !nativeAV.getBool(objc.ID(w.NativeHandle()), objc.RegisterName("acceptsMouseMovedEvents")) {
			problem = "window does not accept motion"
		}
		if w.IsAlwaysOnTop() {
			problem = "playback window is unexpectedly always on top"
		}
		if playlist {
			p.Debug("pause")
			position, duration := e.Position(), e.Duration()
			t.Logf("remux native initial position=%v duration=%v", position, duration)
			if !near(position, 0) {
				problem = fmt.Sprintf("remux started at %v instead of content zero", position)
			} else if !near(duration, remuxDuration) {
				problem = fmt.Sprintf("AVPlayer duration %v does not match remux content duration %v", duration, remuxDuration)
			}
		}
	})
	if problem != "" {
		t.Fatal(problem)
	}
	t.Log("native AVPlayerLayer attached, ready for display; zero pixel-buffer outputs")
	mygo.RunOnMain(func() {
		lib, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
		var getDouble func(objc.ID, objc.SEL) float64
		purego.RegisterLibFunc(&getDouble, lib, "objc_msgSend")
		screen := avSend(objc.ID(w.NativeHandle()), "screen")
		for _, name := range []string{"maximumExtendedDynamicRangeColorComponentValue", "maximumPotentialExtendedDynamicRangeColorComponentValue"} {
			selector := objc.RegisterName(name)
			// Query availability before messaging older macOS releases.
			if avSend(screen, "respondsToSelector:", selector) != 0 {
				t.Logf("display %s=%.3f", name, getDouble(screen, selector))
			}
		}
	})
	mygo.RunOnMain(func() { p.Debug("pause"); e.Seek(8 * time.Second); w.Invalidate() })
	nativeWait(t, "paused seek", func() bool { return near(e.Position(), 8*time.Second) })
	mygo.RunOnMain(func() { p.Debug("play"); w.Invalidate() })
	nativeWait(t, "play after seek", func() bool { return e.Position() > 8500*time.Millisecond })
	mygo.RunOnMain(p.toggleFullscreen)
	nativeWait(t, "native fullscreen", func() bool { return w.IsFullScreen() && p.fullscreen })
	// Wait for AppKit's animation/tracking handoff, then hide both bars.
	time.Sleep(600 * time.Millisecond)
	mygo.RunOnMain(func() { p.Debug("hide-controls"); w.Invalidate() })
	nativeWait(t, "fullscreen controls hidden", func() bool { return !p.controlsShown })
	mygo.RunOnMain(func() { nativeMouse(w, 5, 300, 250) })
	nativeWait(t, "fullscreen mouse motion reveals controls", func() bool { return p.controlsShown })
	t.Log("fullscreen AppKit mouse motion revealed the control bar")
	mygo.RunOnMain(func() { p.Debug("sample-subtitle"); p.Debug("pause"); w.Invalidate() })
	nativeWait(t, "paused controls", func() bool { return p.controlsShown && p.userPaused })
	if out := os.Getenv("MYGO_NATIVE_TEST_SHOT"); out != "" {
		mygo.RunOnMain(func() {
			data, err := w.CapturePage()
			if err != nil {
				problem = err.Error()
				return
			}
			if err = os.WriteFile(out, data, 0o644); err != nil {
				problem = err.Error()
			}
		})
		if problem != "" {
			t.Fatal(problem)
		}
	}
	if out := os.Getenv("MYGO_NATIVE_TEST_SCREENSHOT"); out != "" {
		var number int
		mygo.RunOnMain(func() { number = nativeAV.getInt(objc.ID(w.NativeHandle()), objc.RegisterName("windowNumber")) })
		if output, err := exec.Command("screencapture", "-x", "-l", fmt.Sprint(number), out).CombinedOutput(); err != nil {
			t.Fatalf("capture native video layer: %v: %s", err, output)
		}
	}
	focusFrames := frames
	mygo.RunOnMain(func() { w.Focus(); w.Invalidate() })
	nativeWait(t, "fullscreen player focused and redrawn", func() bool { return w.IsFocused() && frames > focusFrames })
	// Leave through the real back button while AppKit still owns the
	// full-screen transition. The player window stays alive for the page.
	if hit := nativeHitView(w, 38, 32); hit != "MyGoSurfaceView" {
		t.Fatalf("fullscreen back point hit %q, want the native UI surface", hit)
	}
	mygo.RunOnMain(func() { nativeAppKitClick(w.NativeHandle(), 38, 32) })
	nativeWait(t, "fullscreen back click leaves the player", func() bool { return !p.Active() })
	nativeWait(t, "fullscreen back restores the ordinary page", func() bool {
		return !p.Active() && !w.IsFullScreen() && !p.fullscreen && pageFrames > 0
	})
	if !w.IsVisible() || !e.closed || e.host.NativeView() != 0 {
		t.Fatal("returning from fullscreen did not leave a live window or close the native engine exactly once")
	}
	r, g, b := nativeWindowCapture(t, w)
	if g < 100 || g < r*2 || g < b*2 {
		t.Fatalf("returning from fullscreen did not show the ordinary page: center pixel=(%d,%d,%d)", r, g, b)
	}
	restoredFrames := pageFrames
	mygo.RunOnMain(func() { w.Invalidate() })
	nativeWait(t, "restored page survives another frame", func() bool { return pageFrames > restoredFrames })
	secondReturnFrom := pageFrames
	mygo.RunOnMain(func() { p.Play(movie("native-hdr-repeat", 0), true) })
	nativeWait(t, "second native playback", func() bool {
		s := p.Status()
		return s.Ready || s.Error
	})
	if p.Status().Error {
		t.Fatalf("second native playback: %s", p.Status().ErrorText)
	}
	var repeatEngine *nativeEngine
	mygo.RunOnMain(func() {
		repeatEngine, _ = p.cur.eng.(*nativeEngine)
		p.toggleFullscreen()
	})
	nativeWait(t, "second native fullscreen", func() bool { return w.IsFullScreen() && p.fullscreen })
	focusFrames = frames
	mygo.RunOnMain(func() { w.Focus(); w.Invalidate() })
	nativeWait(t, "second fullscreen player focused and redrawn", func() bool {
		return w.IsFocused() && frames > focusFrames
	})
	mygo.RunOnMain(func() { nativeAppKitClick(w.NativeHandle(), 38, 32) })
	nativeWait(t, "second fullscreen back restores the page", func() bool {
		return !p.Active() && !w.IsFullScreen() && !p.fullscreen && pageFrames > secondReturnFrom
	})
	if repeatEngine == nil || !repeatEngine.closed || repeatEngine.host.NativeView() != 0 {
		t.Fatal("the second fullscreen return retained or failed to close its native engine")
	}
	var covering *mygo.Window
	mygo.RunOnMain(func() {
		if w.IsAlwaysOnTop() {
			problem = "fullscreen exit left the player always on top"
			return
		}
		covering = mygo.NewWindow(mygo.WindowOptions{
			Title: "Window covering playback", Width: 960, Height: 640,
			Content: ui.View(func(c *ui.Context) { ui.Box(c).Fill().Background(ui.RGB(30, 30, 30)) }),
		})
		covering.SetBounds(w.Bounds())
		covering.Show()
		covering.Focus()
	})
	if problem != "" {
		t.Fatal(problem)
	}
	defer mygo.RunOnMain(covering.Close)
	nativeWait(t, "another normal window covers the player", func() bool {
		ordered := avSend(avSend(objc.ID(objc.GetClass("NSApplication")), "sharedApplication"), "orderedWindows")
		return !w.IsAlwaysOnTop() && covering.IsFocused() && avSend(ordered, "objectAtIndex:", uint64(0)) == objc.ID(covering.NativeHandle())
	})
	t.Log("normal window covered playback; fullscreen exit preserved normal window level")
	t.Log("seek, pause/resume, fullscreen back, restored page and native view cleanup passed")
}
