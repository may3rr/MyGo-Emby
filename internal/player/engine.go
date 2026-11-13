package player

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
)

// engine is the video the player shows: native AVPlayerLayer on macOS,
// ui/video elsewhere, and a fake in tests. Methods run on the main thread.
type engine interface {
	Play()
	Pause()
	Playing() bool
	Ended() bool
	Ready() bool
	Buffering() bool
	Err() error
	Position() time.Duration
	Duration() time.Duration
	Seek(d time.Duration)
	SetRate(r float64)
	SetVolume(v float64)
	Close()
	// Video builds the element showing it and schedules playback-state updates.
	// Native AVPlayerLayer presentation runs independently of UI frames.
	Video(c *ui.Context) ui.Element
}

// opener opens a URL as an engine.
type opener func(url string) (engine, error)

// openFrameVideo uses MyGo's frame-based system player on other platforms.
func openFrameVideo(url string) (engine, error) {
	p, err := video.OpenURL(url)
	if err != nil {
		return nil, err
	}
	return avEngine{p}, nil
}

// avEngine adapts *ui.VideoPlayer.
//
// Muting is the volume at 0, never SetMuted: ui.VideoPlayer treats a muted
// video as a background picture, which it pauses while the window is
// covered or the desktop asks for less motion, and a film the user muted
// must play on.
type avEngine struct{ p *ui.VideoPlayer }

func (e avEngine) Play()                   { e.p.Play() }
func (e avEngine) Pause()                  { e.p.Pause() }
func (e avEngine) Playing() bool           { return e.p.Playing() }
func (e avEngine) Ended() bool             { return e.p.Ended() }
func (e avEngine) Ready() bool             { return e.p.Ready() }
func (e avEngine) Buffering() bool         { return e.p.Buffering() }
func (e avEngine) Err() error              { return e.p.Err() }
func (e avEngine) Position() time.Duration { return e.p.Position() }
func (e avEngine) Duration() time.Duration { return e.p.Duration() }
func (e avEngine) Seek(d time.Duration)    { e.p.Seek(d) }
func (e avEngine) SetRate(r float64)       { e.p.SetRate(r) }
func (e avEngine) SetVolume(v float64)     { e.p.SetVolume(v) }
func (e avEngine) Close()                  { e.p.Close() }
func (e avEngine) Video(c *ui.Context) ui.Element {
	return ui.Video(c, e.p).Fit(ui.Contain)
}
