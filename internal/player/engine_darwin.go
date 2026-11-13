//go:build darwin

package player

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo/ui"
)

// nativeEngine leaves decoding, Dolby Vision reshaping, color management and
// HDR/EDR presentation together in AVFoundation. No CVPixelBuffer is requested
// and no 8-bit texture or SDR tone mapper sits between AVPlayer and the display.
// All methods, including Close, run on the UI thread.
type nativeEngine struct {
	player, item, layer, view objc.ID
	host                      *ui.PlatformViewHandle
	rate                      float64
	playing, started, closed  bool
}

type nativeTime struct {
	Value int64
	Scale int32
	Flags uint32
	Epoch int64
}

var nativeAV struct {
	once       sync.Once
	err        error
	getTime    func(objc.ID, objc.SEL) nativeTime
	seek       func(objc.ID, objc.SEL, nativeTime, nativeTime, nativeTime)
	setFloat   func(objc.ID, objc.SEL, float32)
	getBool    func(objc.ID, objc.SEL) bool
	getInt     func(objc.ID, objc.SEL) int
	getCString func(objc.ID, objc.SEL) *byte
}

func avSend(obj objc.ID, name string, args ...any) objc.ID {
	return obj.Send(objc.RegisterName(name), args...)
}

func loadNativeAV() error {
	nativeAV.once.Do(func() {
		for _, name := range []string{"AppKit", "AVFoundation", "QuartzCore"} {
			if _, err := purego.Dlopen("/System/Library/Frameworks/"+name+".framework/"+name, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
				nativeAV.err = err
				return
			}
		}
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			nativeAV.err = err
			return
		}
		getTime := "objc_msgSend"
		if runtime.GOARCH == "amd64" {
			getTime = "objc_msgSend_stret"
		}
		purego.RegisterLibFunc(&nativeAV.getTime, lib, getTime)
		purego.RegisterLibFunc(&nativeAV.seek, lib, "objc_msgSend")
		purego.RegisterLibFunc(&nativeAV.setFloat, lib, "objc_msgSend")
		purego.RegisterLibFunc(&nativeAV.getBool, lib, "objc_msgSend")
		purego.RegisterLibFunc(&nativeAV.getInt, lib, "objc_msgSend")
		purego.RegisterLibFunc(&nativeAV.getCString, lib, "objc_msgSend")
	})
	return nativeAV.err
}

func openVideo(url string) (engine, error) {
	if err := loadNativeAV(); err != nil {
		return nil, fmt.Errorf("native video: %w", err)
	}
	pool := avSend(objc.ID(objc.GetClass("NSAutoreleasePool")), "new")
	defer avSend(pool, "drain")
	str := avSend(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", url)
	u := avSend(objc.ID(objc.GetClass("NSURL")), "URLWithString:", str)
	if u == 0 {
		return nil, errors.New("native video: invalid URL")
	}
	e := &nativeEngine{rate: 1, playing: true}
	e.item = avSend(avSend(objc.ID(objc.GetClass("AVPlayerItem")), "alloc"), "initWithURL:", u)
	e.player = avSend(avSend(objc.ID(objc.GetClass("AVPlayer")), "alloc"), "initWithPlayerItem:", e.item)
	e.layer = avSend(avSend(objc.ID(objc.GetClass("AVPlayerLayer")), "alloc"), "init")
	e.view = avSend(avSend(objc.ID(objc.GetClass("NSView")), "alloc"), "init")
	if e.item == 0 || e.player == 0 || e.layer == 0 || e.view == 0 {
		e.Close()
		return nil, errors.New("native video: could not create AVPlayerLayer")
	}
	avSend(e.layer, "setPlayer:", e.player)
	gravity := avSend(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", "AVLayerVideoGravityResizeAspect")
	avSend(e.layer, "setVideoGravity:", gravity)
	avSend(e.view, "setWantsLayer:", true)
	avSend(e.view, "setLayer:", e.layer)
	avSend(e.player, "setPreventsDisplaySleepDuringVideoPlayback:", true)
	e.host = ui.HostPlatformView(uintptr(e.view))
	// Prepare silently; Video starts playback once the view can actually be
	// shown. A window on another Space must not consume the film invisibly.
	// firstPicture restores volume after the resume seek has settled.
	e.SetVolume(0)
	return e, nil
}

func (e *nativeEngine) Play() {
	if e.closed {
		return
	}
	e.playing = true
	if !e.started {
		return
	}
	if e.Ended() {
		e.Seek(0)
	}
	nativeAV.setFloat(e.player, objc.RegisterName("setRate:"), float32(e.rate))
}
func (e *nativeEngine) Pause() {
	if e.closed {
		return
	}
	e.playing = false
	avSend(e.player, "pause")
}
func (e *nativeEngine) Playing() bool { return !e.closed && e.playing && !e.Ended() }
func (e *nativeEngine) Ended() bool {
	if e.closed {
		return false
	}
	d := e.Duration()
	return d > 0 && e.Position() >= d-time.Millisecond
}
func (e *nativeEngine) Ready() bool {
	return !e.closed && nativeAV.getBool(e.layer, objc.RegisterName("isReadyForDisplay"))
}
func (e *nativeEngine) Buffering() bool {
	return !e.closed && e.playing && nativeAV.getInt(e.player, objc.RegisterName("timeControlStatus")) == 1
}
func (e *nativeEngine) Err() error {
	if e.closed || nativeAV.getInt(e.item, objc.RegisterName("status")) != 2 {
		return nil
	}
	err := avSend(e.item, "error")
	if err == 0 {
		return errors.New("AVFoundation could not play the video")
	}
	p := nativeAV.getCString(avSend(err, "localizedDescription"), objc.RegisterName("UTF8String"))
	if p == nil {
		return errors.New("AVFoundation could not play the video")
	}
	// The string belongs to NSError and stays alive for this call.
	var b []byte
	for i := uintptr(0); ; i++ {
		ch := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if ch == 0 {
			break
		}
		b = append(b, ch)
	}
	return errors.New(string(b))
}
func (e *nativeEngine) time(selector string) time.Duration {
	if e.closed {
		return 0
	}
	t := nativeAV.getTime(e.item, objc.RegisterName(selector))
	if t.Flags&1 == 0 || t.Flags&28 != 0 || t.Scale <= 0 {
		return 0
	}
	return time.Duration(float64(t.Value) / float64(t.Scale) * float64(time.Second))
}
func (e *nativeEngine) Position() time.Duration { return e.time("currentTime") }
func (e *nativeEngine) Duration() time.Duration { return e.time("duration") }
func (e *nativeEngine) Seek(d time.Duration) {
	if e.closed {
		return
	}
	t := nativeTime{Value: int64(max(d, 0)), Scale: int32(time.Second), Flags: 1}
	zero := nativeTime{Scale: 1, Flags: 1}
	nativeAV.seek(e.player, objc.RegisterName("seekToTime:toleranceBefore:toleranceAfter:"), t, zero, zero)
}
func (e *nativeEngine) SetRate(rate float64) {
	if e.closed || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return
	}
	e.rate = rate
	if e.playing && e.started {
		nativeAV.setFloat(e.player, objc.RegisterName("setRate:"), float32(rate))
	}
}
func (e *nativeEngine) SetVolume(v float64) {
	if !e.closed {
		nativeAV.setFloat(e.player, objc.RegisterName("setVolume:"), float32(min(max(v, 0), 1)))
	}
}
func (e *nativeEngine) Close() {
	if e.closed {
		return
	}
	e.closed, e.playing = true, false
	if e.host != nil {
		e.host.Close()
	}
	avSend(e.player, "pause")
	avSend(e.layer, "setPlayer:", objc.ID(0))
	avSend(e.player, "replaceCurrentItemWithPlayerItem:", objc.ID(0))
	for _, obj := range []objc.ID{e.view, e.layer, e.player, e.item} {
		if obj != 0 {
			avSend(obj, "release")
		}
	}
	e.view, e.layer, e.player, e.item = 0, 0, 0, 0
}
func (e *nativeEngine) Video(c *ui.Context) ui.Element {
	if !e.closed && !e.started {
		e.started = true
		if e.playing {
			e.Play()
		}
	}
	// Video presentation runs independently of UI frames. Only the progress,
	// subtitles and playback state need polling; paused playback stays idle.
	if !e.closed && (e.Playing() || !e.Ready()) {
		c.After(50 * time.Millisecond)
	}
	return ui.NativeView(c, e.host)
}
