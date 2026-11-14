//go:build darwin

package player

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
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
)

func TestNativeLayerOpaqueBlackBars(t *testing.T) {
	fixture := os.Getenv("MYGO_NATIVE_TEST_FILE")
	if fixture == "" {
		t.Skip("set MYGO_NATIVE_TEST_FILE for native AppKit layer checks")
	}
	absolute, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absolute); err != nil {
		t.Fatal(err)
	}
	e, err := openVideo((&url.URL{Scheme: "file", Path: absolute}).String())
	if err != nil {
		t.Fatal(err)
	}
	engine := e.(*nativeEngine)
	engine.playing = false

	var w *mygo.Window
	frames := 0
	mygo.RunOnMain(func() {
		w = mygo.NewWindow(mygo.WindowOptions{
			Title: "Native layer black bars", Width: 960, Height: 640, UseContentSize: true,
			BackgroundColor: "#ff00ff", TitleBarStyle: mygo.TitleBarHidden,
			Content: ui.View(func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.RGB(255, 0, 255)).Children(func() {
					frames++
					ui.NativeView(c, engine.host).Fill()
					ui.Box(c.Key("mygo-overlay")).Absolute().Left(360).Top(288).Size(240, 64).Background(ui.RGB(0, 255, 0))
				})
			}),
		})
		mygo.App.Show()
		w.Show()
		w.Focus()
	})
	defer mygo.RunOnMain(func() {
		engine.Close()
		w.Close()
	})

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ready := false
		mygo.RunOnMain(func() {
			ready = frames > 1 && engine.host.NativeView() != 0
		})
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	present := false
	mygo.RunOnMain(func() {
		present = frames > 1 && engine.host.NativeView() != 0
	})
	if !present {
		t.Fatal("native layer was not embedded in the MyGo window")
	}
	mygo.RunOnMain(func() {
		// Give the embedding pane a color unlike the player so uncovered
		// native pixels are visible in the window capture.
		pane := avSend(engine.view, "superview")
		background := avSend(avSend(objc.ID(w.NativeHandle()), "backgroundColor"), "CGColor")
		avSend(avSend(pane, "layer"), "setBackgroundColor:", background)
		engine.Pause()
	})

	var problems []string
	mygo.RunOnMain(func() {
		if avSend(engine.layer, "player") != engine.player {
			problems = append(problems, "AVPlayerLayer is not presenting AVPlayer")
		}
		gravity := nativeCString(nativeAV.getCString(avSend(engine.layer, "videoGravity"), objc.RegisterName("UTF8String")))
		if gravity != "AVLayerVideoGravityResizeAspect" {
			problems = append(problems, fmt.Sprintf("video gravity = %q, want resize aspect", gravity))
		}
		if outputs := avSend(avSend(engine.item, "outputs"), "count"); outputs != 0 {
			problems = append(problems, fmt.Sprintf("native playback has %d pixel-buffer outputs", outputs))
		}
		background := avSend(engine.layer, "backgroundColor")
		if background == 0 {
			problems = append(problems, "AVPlayerLayer background is transparent")
			return
		}
		r, g, b, a, ok := nativeColorComponents(background)
		if !ok || r > 0.001 || g > 0.001 || b > 0.001 || a < 0.999 {
			problems = append(problems, fmt.Sprintf("AVPlayerLayer background = rgba(%g,%g,%g,%g), want opaque black", r, g, b, a))
		}
	})

	time.Sleep(300 * time.Millisecond)
	img := nativeBlackBarsCapture(t, w)
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	topBar := img.At(iw/2, ih/20)
	bottomBar := img.At(iw/2, ih-ih/20)
	overlay := img.At(iw/2, ih/2)
	if r, g, b := nativeRGB(topBar); r > 4 || g > 4 || b > 4 {
		problems = append(problems, fmt.Sprintf("top black bar = rgb(%d,%d,%d); transparent layer exposed the magenta window", r, g, b))
	}
	if r, g, b := nativeRGB(bottomBar); r > 4 || g > 4 || b > 4 {
		problems = append(problems, fmt.Sprintf("bottom black bar = rgb(%d,%d,%d); transparent layer exposed the magenta window", r, g, b))
	}
	if r, g, b := nativeRGB(overlay); g < 150 || r > 60 || b > 60 {
		problems = append(problems, fmt.Sprintf("MyGo overlay = rgb(%d,%d,%d), want green above the native layer", r, g, b))
	}
	tr, tg, tb := nativeRGB(topBar)
	br, bg, bb := nativeRGB(bottomBar)
	or, og, ob := nativeRGB(overlay)
	t.Logf("native layer: opaque black bars, ResizeAspect, zero outputs, MyGo overlay visible; top=(%d,%d,%d) bottom=(%d,%d,%d) overlay=(%d,%d,%d)",
		tr, tg, tb, br, bg, bb, or, og, ob)
	if len(problems) > 0 {
		t.Fatal(strings.Join(problems, "; "))
	}
}

func nativeCString(p *byte) string {
	if p == nil {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		ch := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if ch == 0 {
			return string(b)
		}
		b = append(b, ch)
	}
}

func nativeColorComponents(cgColor objc.ID) (r, g, b, a float64, ok bool) {
	lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	var getDouble func(objc.ID, objc.SEL) float64
	purego.RegisterLibFunc(&getDouble, lib, "objc_msgSend")
	space := avSend(objc.ID(objc.GetClass("NSColorSpace")), "sRGBColorSpace")
	color := avSend(objc.ID(objc.GetClass("NSColor")), "colorWithCGColor:", cgColor)
	color = avSend(color, "colorUsingColorSpace:", space)
	if color == 0 {
		return 0, 0, 0, 0, false
	}
	return getDouble(color, objc.RegisterName("redComponent")),
		getDouble(color, objc.RegisterName("greenComponent")),
		getDouble(color, objc.RegisterName("blueComponent")),
		getDouble(color, objc.RegisterName("alphaComponent")), true
}

func nativeBlackBarsCapture(t *testing.T, w *mygo.Window) image.Image {
	t.Helper()
	var number int
	mygo.RunOnMain(func() {
		number = nativeAV.getInt(objc.ID(w.NativeHandle()), objc.RegisterName("windowNumber"))
	})
	path := filepath.Join(t.TempDir(), "black-bars.png")
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
	return img
}

func nativeRGB(c color.Color) (int, int, int) {
	r, g, b, _ := c.RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8)
}
