package player

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// The view is built for every frame of the video: with AVPlayer, twice a
// frame of the film (ui/video asks for a frame at half the film's frame
// interval), so 48 times a second for a 24 fps film. It is kept small:
//
//   - With the controls hidden it builds the root, the video and, when a
//     line shows, the subtitle: three or four elements.
//   - Nothing in it reads Hovered except the control bar's own hover (once
//     as the pointer comes over it and leaves it). Buttons are ui.Button,
//     whose hovered face the toolkit picks as it paints, with their icons
//     drawn by functions made once (no closure per frame).
//   - The time texts change once a second (clockText), the subtitle when
//     its cue changes (subtitleTrack), the countdown once a second.
//   - Timers are one-shot c.After calls, made only while something waits:
//     a paused video asks for no frame at all.

var (
	white      = ui.RGB(255, 255, 255)
	black      = ui.RGB(0, 0, 0)
	textMuted  = ui.RGBA(255, 255, 255, 0.66)
	trackColor = ui.RGBA(255, 255, 255, 0.28)
	scrimTop   = ui.LinearGradient{From: ui.RGBA(0, 0, 0, 0.62), To: ui.Transparent, Angle: 180}
	scrimBot   = ui.LinearGradient{From: ui.Transparent, To: ui.RGBA(0, 0, 0, 0.62), Angle: 180}
	tnum       = []string{"tnum"}
	barGlass   = glass.Glass{Tint: ui.RGBA(0, 0, 0, 0.32)}
	panelGlass = glass.Glass{Tint: ui.RGBA(20, 20, 22, 0.55)}

	fade = ui.ElementTransition{
		Duration: 200 * time.Millisecond,
		Enter:    &ui.Motion{Opacity: 0},
		Exit:     &ui.Motion{Opacity: 0},
	}
	barMotion = ui.ElementTransition{
		Duration: 220 * time.Millisecond,
		Enter:    &ui.Motion{Opacity: 0, Y: 12},
		Exit:     &ui.Motion{Opacity: 0, Y: 12},
	}
	posterFade = ui.ElementTransition{
		Duration: 350 * time.Millisecond,
		Exit:     &ui.Motion{Opacity: 0},
	}
	moveSmoothly = ui.ElementTransition{Duration: 200 * time.Millisecond, Position: true}
	menuAbove    = ui.PopoverMenuButtonOptions{At: ui.AnchorTopEnd, Self: ui.AnchorBottomEnd}
)

// playerTheme is the dark theme the player builds with, its controls
// without a face until hovered, as over a film.
func playerTheme() *ui.Theme {
	t := *ui.DarkTheme()
	t.Background = black
	t.Surface = ui.Transparent
	t.SurfaceHover = ui.RGBA(255, 255, 255, 0.16)
	t.SurfacePressed = ui.RGBA(255, 255, 255, 0.26)
	t.Text = white
	t.TextMuted = textMuted
	return &t
}

// View builds the player over the whole window. The app builds it alone
// while the player is Active, nothing under it.
func (p *Player) View(c *ui.Context) {
	if !p.active {
		return
	}
	now := c.Now()
	p.now = now
	if p.activity.IsZero() {
		p.activity = now
	}
	if pl := p.cur; pl != nil && pl.opened.IsZero() {
		pl.opened = now
	}
	p.keys(c, now)
	p.tick(c, now)
	if !p.active {
		return // the keys or the end left the player
	}
	shown := p.wantControls(c, now)
	p.controlsShown = shown
	root := ui.Themed(c.Key("mygo-player"), p.theme).Fill().Background(black).
		Focusable().FocusRing(false).AutoFocus().Bind(&p.focus).HandleInput(p.fnInput).
		Label(p.title)
	if !shown {
		root.Cursor(ui.CursorNone)
	}
	root.Children(func() { p.body(c, now, shown) })
}

// keys handles the player's shortcuts.
func (p *Player) keys(c *ui.Context, now time.Time) {
	switch {
	case c.Shortcut(0, ui.KeySpace), c.Shortcut(0, ui.KeyK):
		p.activity = now
		p.togglePlay(now)
	case c.Shortcut(0, ui.KeyLeft), c.Shortcut(0, ui.KeyJ):
		p.activity = now
		p.skip(now, -skipStep)
	case c.Shortcut(0, ui.KeyRight), c.Shortcut(0, ui.KeyL):
		p.activity = now
		p.skip(now, skipStep)
	case c.Shortcut(0, ui.KeyUp):
		p.activity = now
		p.setVolume(now, p.volume+volumeStep)
	case c.Shortcut(0, ui.KeyDown):
		p.activity = now
		p.setVolume(now, p.volume-volumeStep)
	case c.Shortcut(0, ui.KeyM):
		p.activity = now
		p.toggleMute(now)
	case c.Shortcut(0, ui.KeyF):
		p.activity = now
		p.toggleFullscreen()
	case c.Shortcut(0, ui.KeyN):
		if p.nextItem != nil {
			p.playNext()
		}
	case c.Shortcut(0, ui.KeyEscape):
		p.activity = now
		p.escape()
	}
}

// onInput sees the pointer over the player: moving shows the controls
// and the pointer, and builds a frame only when they are hidden.
func (p *Player) onInput(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputPointerMove:
		p.activity = ev.Time
		return !p.controlsShown
	case ui.InputPointerDown, ui.InputScroll:
		p.activity = ev.Time
	}
	return false
}

// tick moves the playback state on, once a frame: the first picture, the
// seeks due, the progress reports, the end and the countdown.
func (p *Player) tick(c *ui.Context, now time.Time) {
	pl := p.cur
	if pl == nil || p.phase != phasePlaying {
		return
	}
	eng := pl.eng
	if err := eng.Err(); err != nil {
		p.fail(fmt.Errorf("%s%w", str.ErrPlayer, err), true)
		return
	}
	if !pl.ready {
		if !eng.Ready() || !p.settled(now, pl) {
			// ui/video takes AVPlayer's first picture only as a frame
			// builds the Video, and its engine wakes the window only when
			// its state changes, which the first picture itself does: so
			// nothing would build the frame that takes it. Ask for frames
			// until it came (the view is small while loading).
			c.After(firstPicturePoll)
			return
		}
		p.firstPicture(now, pl)
	}
	dur := p.duration(now, eng)
	pos := p.framePos
	if p.controlsShown || p.subs.active() || p.seek.pending || p.upNext.shown || now.Sub(p.posAt) >= 250*time.Millisecond {
		pos = eng.Position()
		p.framePos, p.posAt = pos, now
	}
	if target, ok, wait := p.seek.take(now, pos); ok {
		target = clampSeek(target, dur)
		eng.Seek(target)
		pos = target
		p.sendProgress(now, "TimeUpdate")
		p.framePos = pos
	} else if wait > 0 {
		c.After(wait)
	}
	p.lastPos = pos
	if !p.userPaused && now.Sub(p.lastSent) >= progressInterval {
		p.sendProgress(now, "TimeUpdate")
	}
	if eng.Ended() {
		if p.cfg.AutoNext && p.nextItem != nil && !p.upNext.cancelled {
			p.playNext()
		} else {
			p.exit(true)
		}
		return
	}
	p.tickUpNext(pos, dur)
}

// firstPicture: the stream shows. A stream that replaces another takes its
// place now; the first of the item reports the start.
// settled reports whether the stream, which shows a picture, stands where
// it was asked to start; else it seeks there once and waits (up to
// settleWait) with the picture covered and the sound off.
//
// The seek waits for the first picture on purpose: AVPlayer seeked any
// earlier, even once the item loaded, plays the sound from the resume
// point while AVPlayerItemVideoOutput gives no picture for tens of seconds
// (seen with MP4s served directly, through ui/video on macOS 26).
func (p *Player) settled(now time.Time, pl *playing) bool {
	if pl.start <= 0 || near(pl.eng.Position(), pl.start) {
		return true
	}
	if !pl.settling {
		pl.settling, pl.settleBy = true, now.Add(settleWait)
		pl.eng.Seek(pl.start)
		return false
	}
	if now.After(pl.settleBy) {
		log.Printf("player: still at %v, not %v, after %v", pl.eng.Position(), pl.start, settleWait)
		return true
	}
	return false
}

const settleWait = 15 * time.Second

// duration returns the stream's length: AVPlayer's, read again every few
// seconds once known, else the item's.
func (p *Player) duration(now time.Time, eng engine) time.Duration {
	if p.dur <= 0 || now.Sub(p.durAt) >= 5*time.Second {
		if d := eng.Duration(); d > 0 {
			p.dur = d
		}
		p.durAt = now
	}
	if p.dur > 0 {
		return p.dur
	}
	return p.runtime
}

func (p *Player) firstPicture(now time.Time, pl *playing) {
	eng := pl.eng
	pl.ready = true
	if old := p.prev; old != nil {
		p.prev = nil
		if old.stream.Method == emby.Transcode && old.stream.Playback != nil && old.stream.Playback.PlaySessionID != pl.stream.Playback.PlaySessionID {
			p.rep.send(reportJob{kind: reportStopTranscode, session: old.stream.Playback.PlaySessionID})
		}
		old.close()
	}
	eng.SetVolume(p.effectiveVolume())
	if !pl.resume {
		eng.Pause()
	}
	p.switching = false
	if f := p.pendingSwitch; f != nil {
		p.pendingSwitch = nil
		f()
		p.sendProgress(now, "AudioTrackChange")
	}
	if !p.started {
		p.started = true
		r := p.report(pl.start)
		p.rep.send(reportJob{kind: reportStart, report: r})
		p.lastSent = now
	}
}

func (p *Player) tickUpNext(pos, dur time.Duration) {
	if p.upNextFrozen || !p.cfg.AutoNext || p.nextItem == nil || p.upNext.cancelled || dur <= 0 {
		return
	}
	rem := dur - pos
	if rem > upNextLead {
		if p.upNext.shown { // seeked back
			p.upNext.shown = false
			p.discard(p.upNext.stream)
			p.upNext.stream, p.upNext.fetching = nil, false
		}
		return
	}
	if !p.upNext.shown {
		p.upNext.shown = true
		p.prefetchNext()
	}
	secs := int(math.Ceil(rem.Seconds()))
	if secs != p.upNextSecs || p.upNextText == "" {
		p.upNextSecs = secs
		p.upNextText = strconv.Itoa(max(secs, 0)) + " " + str.UpNextIn
	}
	if rem <= 250*time.Millisecond && !p.userPaused {
		p.playNext()
	}
}

// wantControls tells whether the controls show: always while loading,
// paused, seeking, in a menu or under the pointer, else for three seconds
// after the pointer moved or a key was pressed.
func (p *Player) wantControls(c *ui.Context, now time.Time) bool {
	pl := p.cur
	if p.phase != phasePlaying || pl == nil || !pl.ready || p.switching || p.prev != nil {
		return true
	}
	if p.userPaused || p.scrubbing || p.speedOpen || p.audioOpen || p.subOpen || p.barHovered || pl.eng.Ended() {
		return true
	}
	until := p.activity.Add(controlsHideAfter)
	if now.Before(until) {
		c.After(until.Sub(now) + 10*time.Millisecond)
		return true
	}
	return false
}

func fillAbs(e ui.Element) ui.Element {
	return e.Absolute().Top(0).Left(0).Right(0).Bottom(0)
}

// body builds what shows over the black: the picture, the subtitle, the
// controls and the panels.
func (p *Player) body(c *ui.Context, now time.Time, shown bool) {
	w, h := c.Size()
	pl := p.cur
	// The picture: the stream playing, under the one it replaces until
	// the new one shows.
	if pl != nil && p.videoLoaded(c, now, pl) {
		pl.builds++
		fillAbs(pl.eng.Video(c.Key(pl)))
	}
	if old := p.prev; old != nil {
		fillAbs(old.eng.Video(c.Key(old)))
	}
	// The stage takes the pointer over the picture: a move there shows
	// the controls again, and a click plays or pauses, a double click
	// going full screen. It is above the video, whose own click handling
	// would otherwise take the moves from the root and keep the controls
	// hidden, and below the bars, which take their own.
	stage := fillAbs(ui.Box(c.Key("stage"))).HandleInput(p.fnInput)
	if stage.Clicked() {
		p.activity = now
		p.togglePlay(now)
		if stage.Clicks() == 2 {
			p.toggleFullscreen()
		}
	}
	loading := p.phase == phaseLoading || pl != nil && !pl.ready && p.prev == nil
	if loading && p.phase != phaseError {
		// Over the video until it stands where it starts, and fading out
		// as it does: black, and the item's picture dimmed on it.
		fillAbs(ui.Box(c.Key("cover"))).Background(black).Transition(posterFade)
		if bmp := p.posterBitmap(); bmp != nil {
			fillAbs(ui.Image(c.Key("poster"), bmp)).Fit(ui.Cover).Opacity(0.5).Transition(posterFade)
		}
	}
	if p.phase == phasePlaying && pl != nil && pl.ready && p.subs.active() {
		if text := p.subs.at(p.framePos); text != "" {
			p.subtitle(c, text, w, h, shown)
		}
	}
	busy := loading || p.switching || pl != nil && pl.ready && pl.eng.Buffering()
	if busy && p.phase != phaseError {
		fillAbs(ui.Row(c.Key("busy"))).Center().PassThrough().Children(func() {
			ui.Spinner(c).FontSize(34).TextColor(white).Label(str.Loading)
		})
	}
	showBar := shown && p.phase == phasePlaying && pl != nil
	if shown {
		p.topBar(c, showBar)
		if !p.active || p.cur != pl {
			// Back left the player (Close released the stream) in the
			// middle of this frame: nothing below may touch it.
			return
		}
	}
	if showBar {
		p.controlBar(c, now, w)
	} else {
		p.barHovered = false
	}
	if p.upNext.shown && !p.upNext.cancelled && p.nextItem != nil && p.phase == phasePlaying {
		p.upNextCard(c, now, showBar)
	}
	if p.phase == phaseError {
		p.errorPanel(c, now)
	}
}

// videoLoaded reports whether the view may build the stream's Video.
//
// ui/video's AVFoundation engine asks the asset for its video tracks
// (-[AVAsset tracksWithMediaType:], for the frame rate) the first time a
// frame builds the Video, on the main thread, and that call waits until
// AVFoundation has loaded the asset over the network: seconds for a file
// whose index sits at its end, and the window frozen meanwhile. AVPlayer
// loads the item on its own once opened, and its duration, which reads
// without waiting, is known once the asset is loaded; so the Video is
// built only from then on, the view looking again ten times a second.
// After loadWait it is built anyway, so that a stream that fails shows
// its error.
func (p *Player) videoLoaded(c *ui.Context, now time.Time, pl *playing) bool {
	if pl.loaded {
		return true
	}
	if (pl.stream != nil && pl.stream.Playlist) || pl.eng.Duration() > 0 || now.Sub(pl.opened) > loadWait {
		pl.loaded = true
		return true
	}
	c.After(100 * time.Millisecond)
	return false
}

const loadWait = 30 * time.Second

// firstPicturePoll is how often the view looks for the first picture.
const firstPicturePoll = 16 * time.Millisecond

func (p *Player) posterBitmap() *ui.Bitmap {
	if p.cfg.Image != nil && p.posterURL != "" {
		return p.cfg.Image(p.posterURL)
	}
	return p.poster
}

// subtitle draws the line over the picture, above the controls when they
// show, at a size following the window's height.
func (p *Player) subtitle(c *ui.Context, text string, w, h float32, shown bool) {
	size := min(max(h*0.042, 18), 46)
	bottom := max(h*0.06, 28)
	if shown && p.cur != nil {
		bottom = 148
	}
	ui.Row(c.Key("subtitle")).Absolute().Left(0).Right(0).Bottom(bottom).Justify(ui.Center).
		PassThrough().Transition(moveSmoothly).Children(func() {
		ui.Text(c, text).FontSize(size).FontWeight(500).TextColor(white).TextAlign(ui.Center).
			TextShadow(0, 1, 4, ui.RGBA(0, 0, 0, 0.9)).TextStroke(0.6, ui.RGBA(0, 0, 0, 0.55)).
			MaxWidth(w * 0.84).LineHeight(1.25)
	})
}

// topBar: back, and the title of what plays.
func (p *Player) topBar(c *ui.Context, withScrim bool) {
	tb := c.TitleBar()
	left := float32(20)
	if !p.fullscreen && tb.Left > 0 {
		left = tb.Left + 12
	}
	bar := ui.Row(c.Key("top")).Absolute().Top(0).Left(0).Right(0).Height(110).
		Padding(14, 24, 0, left).Gap(12).AlignItems(ui.Start).DragWindow().Transition(fade)
	if withScrim {
		bar.LinearGradient(scrimTop)
	}
	bar.Children(func() {
		b := ui.Button(c.Key("back"), "").Label(str.Back).Tooltip(str.Back).Size(36, 36).Padding(0).
			Radius(999).Material(barGlass).Draw(p.drawIcons[iconBackIdx])
		if b.Clicked() {
			p.exit(false)
		}
		if p.title == "" {
			return
		}
		ui.Column(c).Gap(2).Shrink(1).PaddingY(1).Children(func() {
			ui.Text(c, p.title).FontSize(17).FontWeight(600).TextColor(white).SingleLine().
				TextShadow(0, 1, 3, ui.RGBA(0, 0, 0, 0.5))
			if p.series != "" {
				ui.Text(c, p.series).FontSize(13).TextColor(textMuted).SingleLine()
			}
		})
	})
}

// controlBar: the glass bar of the transport, the progress and the menus.
func (p *Player) controlBar(c *ui.Context, now time.Time, w float32) {
	pl := p.cur
	if pl == nil {
		return
	}
	eng := pl.eng
	dur := p.duration(now, eng)
	pos := p.lastPos
	if t, ok := p.seek.preview(pos); ok {
		pos = clampSeek(t, dur)
	}
	if p.scrubbing {
		pos = time.Duration(p.scrub * float64(time.Second))
	}
	durSec := max(dur.Seconds(), 0.001)
	if !p.scrubbing {
		p.scrub = pos.Seconds()
	}
	p.barFrac = float32(min(max(pos.Seconds()/durSec, 0), 1))

	ui.Box(c.Key("scrim-bottom")).Absolute().Left(0).Right(0).Bottom(0).Height(190).PassThrough().
		LinearGradient(scrimBot).Transition(fade)

	bar := ui.Column(c.Key("bar")).Absolute().Left(24).Right(24).Bottom(20).Padding(10, 14, 8, 14).Gap(2).
		Radius(22).Material(barGlass).KeepFocus().Transition(barMotion)
	p.barHovered = bar.Hovered()
	bar.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.posText.at(pos, false)).FontSize(12).TextColor(textMuted).FontFeatures(tnum...).
				MinWidth(48).TextAlign(ui.End)
			s := ui.SliderBase(c.Key("progress"), &p.scrub, 0, durSec).Grow(1).Height(22).PaddingX(8).
				Label(str.Progress).Draw(p.fnDrawProgress)
			switch {
			case s.Pressed():
				p.scrubbing = true
				p.activity = now
			case s.PressCanceled():
				p.scrubbing = false
				p.scrub = p.lastPos.Seconds()
			case p.scrubbing:
				p.scrubbing = false
				p.seek.seekTo(now, time.Duration(p.scrub*float64(time.Second)))
			case s.Changed():
				p.seek.seekTo(now, time.Duration(p.scrub*float64(time.Second)))
			}
			p.seekPreview(c, s, dur)
			ui.Text(c, p.remText.at(max(dur-pos, 0), true)).FontSize(12).TextColor(textMuted).
				FontFeatures(tnum...).MinWidth(54)
		})
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			play := str.Pause
			if p.userPaused || eng.Ended() {
				play = str.Play
			}
			if p.button(c, "play", iconPlayIdx, play, 40).Clicked() {
				p.togglePlay(now)
			}
			if p.button(c, "rewind", iconRewindIdx, str.Rewind, 36).Clicked() {
				p.skip(now, -skipStep)
			}
			if p.button(c, "forward", iconForwardIdx, str.Forward, 36).Clicked() {
				p.skip(now, skipStep)
			}
			if p.nextItem != nil {
				if p.button(c, "next", iconNextIdx, str.NextEpisode, 36).Clicked() {
					p.playNext()
					return
				}
			}
			ui.Box(c).Grow(1)
			mute := str.Mute
			if p.muted {
				mute = str.Unmute
			}
			if p.button(c, "mute", iconVolumeIdx, mute, 36).Clicked() {
				p.toggleMute(now)
			}
			if w >= 760 {
				p.volValue = p.effectiveVolume()
				v := ui.SliderBase(c.Key("volume"), &p.volValue, 0, 1).Width(88).Height(22).PaddingX(6).
					Label(str.Volume).Draw(p.fnDrawVolume)
				if v.Changed() {
					p.setVolume(now, p.volValue)
				}
			}
			p.menuButton(c, "speed", iconSpeedIdx, str.Speed, p.fnSpeedMenu, &p.speedOpen)
			if len(p.audioTracks) > 1 {
				p.menuButton(c, "audio", iconAudioIdx, str.Audio, p.fnAudioMenu, &p.audioOpen)
			}
			if len(p.subTracks) > 0 {
				p.menuButton(c, "subtitles", iconSubtitlesIdx, str.Subtitles, p.fnSubMenu, &p.subOpen)
			}
			fs := str.FullScreen
			if p.fullscreen {
				fs = str.ExitFullScreen
			}
			if p.button(c, "fullscreen", iconFullscreenIdx, fs, 36).Clicked() {
				p.toggleFullscreen()
			}
		})
	})
}

// seekPreview follows the pointer without seeking. While dragging it shows
// the pending destination; the slider commits the seek only on release.
func (p *Player) seekPreview(c *ui.Context, slider ui.Element, dur time.Duration) {
	x, _, over := slider.PointerPosition()
	width := slider.LayoutBounds().W
	if dur <= 0 || width <= 16 || (!over && !p.scrubbing) {
		return
	}
	fraction := float64(min(max((x-8)/(width-16), 0), 1))
	target := time.Duration(fraction * float64(dur))
	if p.scrubbing {
		target = time.Duration(p.scrub * float64(time.Second))
		x = 8 + float32(float64(target)/float64(dur))*(width-16)
	}
	target = clampSeek(target, dur)
	x = min(max(x, 40), max(width-40, 40))
	ui.Text(c.Key("seek-preview"), p.seekText.at(target, false)).
		AttachTo(slider, ui.AnchorTopLeft, ui.AnchorBottom).Translate(x, -8).
		Padding(5, 10).Radius(7).Background(ui.RGBA(18, 18, 20, 0.94)).
		TextColor(white).FontSize(12).FontFeatures(tnum...).
		Label(str.ProgressPreview).PassThrough()
}

// button is a round control showing icon, its face the theme's hover.
func (p *Player) button(c *ui.Context, key string, icon int, label string, size float32) ui.Element {
	return ui.Button(c.Key(key), "").Label(label).Tooltip(label).Size(size, size).Padding(0).Radius(999).
		Draw(p.drawIcons[icon])
}

func (p *Player) menuButton(c *ui.Context, key string, icon int, label string, menu func(*ui.Menu), open *bool) {
	opts := menuAbove
	opts.Open = open
	p.button(c, key, icon, label, 36).PopoverMenuButton(menu, opts)
}

func (p *Player) speedMenu(m *ui.Menu) {
	for i, s := range speeds {
		if m.Item(speedLabels[i]).Radio(p.rate == s).Chosen() {
			p.setRate(p.now, s)
		}
	}
}

func (p *Player) audioMenu(m *ui.Menu) {
	for i := range p.audioTracks {
		idx := p.audioTracks[i].Index
		if m.Item(p.audioLabels[i]).Radio(idx == p.audioIdx).Chosen() {
			p.selectAudio(idx)
		}
	}
}

func (p *Player) subtitleMenu(m *ui.Menu) {
	if m.Item(str.SubtitlesOff).Radio(p.subIdx < 0).Chosen() {
		p.selectSubtitle(-1)
	}
	m.Separator()
	for i := range p.subTracks {
		t := &p.subTracks[i]
		it := m.Item(p.subLabels[i]).Radio(t.Index == p.subIdx)
		local, localTrack := p.localSubtitle(t.Index)
		if localTrack && local.Reason != "" {
			it.Disabled(true).Subtitle(local.Reason)
		} else if !t.IsText && !p.canBurnIn {
			it.Disabled(true).Subtitle(str.BurnInUnavailable)
		}
		if it.Chosen() {
			p.selectSubtitle(t.Index)
		}
	}
}

var speedLabels = func() []string {
	out := make([]string, len(speeds))
	for i, s := range speeds {
		if s == 1 {
			out[i] = str.NormalSpeed
		} else {
			out[i] = strconv.FormatFloat(s, 'f', -1, 64) + "×"
		}
	}
	return out
}()

// upNextCard counts down to the next episode, above the controls.
func (p *Player) upNextCard(c *ui.Context, now time.Time, barShown bool) {
	bottom := float32(28)
	if barShown {
		bottom = 132
	}
	ui.Column(c.Key("upnext")).Absolute().Right(28).Bottom(bottom).Width(300).Padding(14, 16).Gap(6).
		Radius(18).Material(panelGlass).KeepFocus().Transition(moveSmoothly).Children(func() {
		ui.Text(c, str.UpNext).FontSize(12).TextColor(textMuted)
		ui.Text(c, p.nextTitle).FontSize(15).FontWeight(600).TextColor(white).MaxLines(2)
		ui.Text(c, p.upNextText).FontSize(12).TextColor(textMuted).FontFeatures(tnum...)
		ui.Row(c).Gap(8).PaddingY(4).Children(func() {
			if ui.Button(c.Key("upnext-play"), str.PlayNow).Background(white).TextColor(black).
				Radius(999).Padding(6, 14).Clicked() {
				p.playNext()
				return
			}
			if ui.Button(c.Key("upnext-cancel"), str.Cancel).Background(ui.RGBA(255, 255, 255, 0.14)).
				Radius(999).Padding(6, 14).Clicked() {
				p.cancelUpNext()
			}
		})
	})
}

// errorPanel says why nothing plays, with a way out: never a black screen.
func (p *Player) errorPanel(c *ui.Context, now time.Time) {
	fillAbs(ui.Column(c.Key("error"))).Center().Children(func() {
		ui.Column(c).Width(420).Padding(26, 28).Gap(10).Radius(20).Material(panelGlass).
			AlignItems(ui.Center).Transition(fade).Role(ui.RoleAlert).Children(func() {
			ui.Icon(c, iconAlert).Size(34, 34).TextColor(ui.RGB(255, 196, 92))
			ui.Text(c, str.ErrTitle).FontSize(18).FontWeight(600).TextColor(white)
			ui.Text(c, p.errText).FontSize(13).TextColor(textMuted).TextAlign(ui.Center).LineHeight(1.35)
			ui.Row(c).Gap(10).PaddingY(8).Children(func() {
				if p.errRetry {
					if ui.Button(c.Key("retry"), str.Retry).Background(ui.RGBA(255, 255, 255, 0.14)).
						Radius(999).Padding(7, 18).Clicked() {
						p.retry()
						return
					}
				}
				if ui.Button(c.Key("close"), str.Close).Background(white).TextColor(black).
					Radius(999).Padding(7, 18).AutoFocus().Clicked() {
					p.exit(false)
				}
			})
		})
	})
}

// Drawing, by functions made once.

const (
	iconBackIdx = iota
	iconPlayIdx
	iconRewindIdx
	iconForwardIdx
	iconNextIdx
	iconVolumeIdx
	iconSpeedIdx
	iconAudioIdx
	iconSubtitlesIdx
	iconFullscreenIdx
	iconCount
)

// makeDrawers makes the functions drawing the buttons' icons, which read
// the player's state as they paint (play or pause, the volume, full
// screen).
func (p *Player) makeDrawers() {
	fixed := func(s *ui.SVG, size float32) func(*ui.Painter, ui.Rect) {
		return func(pt *ui.Painter, r ui.Rect) { drawIcon(pt, r, s, size) }
	}
	p.drawIcons[iconBackIdx] = fixed(iconBack, 20)
	p.drawIcons[iconPlayIdx] = func(pt *ui.Painter, r ui.Rect) {
		s := iconPause
		if p.userPaused || p.cur == nil || p.cur.eng.Ended() {
			s = iconPlay
		}
		drawIcon(pt, r, s, 24)
	}
	p.drawIcons[iconRewindIdx] = fixed(iconSkipBack10, 22)
	p.drawIcons[iconForwardIdx] = fixed(iconSkipForward10, 22)
	p.drawIcons[iconNextIdx] = fixed(iconNextTrack, 20)
	p.drawIcons[iconVolumeIdx] = func(pt *ui.Painter, r ui.Rect) {
		s := iconVolume
		switch v := p.effectiveVolume(); {
		case v == 0:
			s = iconMute
		case v < 0.5:
			s = iconVolumeLow
		}
		drawIcon(pt, r, s, 21)
	}
	p.drawIcons[iconSpeedIdx] = fixed(iconSpeed, 21)
	p.drawIcons[iconAudioIdx] = fixed(iconAudioTrack, 21)
	p.drawIcons[iconSubtitlesIdx] = fixed(iconSubtitles, 21)
	p.drawIcons[iconFullscreenIdx] = func(pt *ui.Painter, r ui.Rect) {
		s := iconFullscreen
		if p.fullscreen {
			s = iconExitFullscreen
		}
		drawIcon(pt, r, s, 20)
	}
}

func drawIcon(pt *ui.Painter, r ui.Rect, s *ui.SVG, size float32) {
	pt.Icon(s, ui.Rect{X: r.X + (r.W-size)/2, Y: r.Y + (r.H-size)/2, W: size, H: size}, white)
}

// drawProgress draws the progress bar: the track, the part played and the
// knob, larger while dragged.
func (p *Player) drawProgress(pt *ui.Painter, r ui.Rect) {
	const pad = 8
	th, knob := float32(4), float32(12)
	if p.scrubbing {
		th, knob = 6, 16
	}
	x, w := r.X+pad, r.W-2*pad
	y := r.Y + r.H/2
	pt.Fill(ui.Rect{X: x, Y: y - th/2, W: w, H: th}, trackColor, th/2)
	done := w * p.barFrac
	pt.Fill(ui.Rect{X: x, Y: y - th/2, W: done, H: th}, white, th/2)
	pt.Fill(ui.Rect{X: x + done - knob/2, Y: y - knob/2, W: knob, H: knob}, white, knob/2)
}

func (p *Player) drawVolume(pt *ui.Painter, r ui.Rect) {
	const pad = 6
	x, w := r.X+pad, r.W-2*pad
	y := r.Y + r.H/2
	pt.Fill(ui.Rect{X: x, Y: y - 2, W: w, H: 4}, trackColor, 2)
	done := w * float32(p.effectiveVolume())
	pt.Fill(ui.Rect{X: x, Y: y - 2, W: done, H: 4}, white, 2)
	pt.Fill(ui.Rect{X: x + done - 5, Y: y - 5, W: 10, H: 10}, white, 5)
}
