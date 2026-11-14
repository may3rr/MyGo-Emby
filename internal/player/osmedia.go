package player

import (
	"time"

	"github.com/egoist/mygo"
)

// ensureMediaCommands registers system media center command handling when
// running inside a real window (cfg.Window != nil). Safe to call repeatedly.
func (p *Player) ensureMediaCommands() {
	if p.cfg.Window == nil || p.mediaCmdOff != nil {
		return
	}
	p.mediaCmdOff = mygo.NowPlaying.OnCommand(func(cmd mygo.MediaCommand, pos time.Duration) {
		p.post(func() {
			p.handleMediaCommand(cmd, pos)
		})
	})
}

func (p *Player) handleMediaCommand(cmd mygo.MediaCommand, pos time.Duration) {
	if !p.active || p.phase != phasePlaying || p.cur == nil {
		return
	}
	now := p.now
	if now.IsZero() {
		now = time.Now()
	}
	switch cmd {
	case mygo.MediaPlay:
		if p.userPaused {
			p.togglePlay(now)
		}
	case mygo.MediaPause:
		if !p.userPaused {
			p.togglePlay(now)
		}
	case mygo.MediaStop:
		p.exit(false)
	case mygo.MediaNext:
		if p.nextItem != nil {
			p.playNext()
		}
	case mygo.MediaSeek:
		if p.cur != nil && p.cur.ready {
			p.seekTo(now, pos)
		}
	}
}

// syncKeepAwake holds a display sleep assertion only during active playback in a
// real window, releasing it immediately when paused, failed, or closed.
func (p *Player) syncKeepAwake() {
	want := p.cfg.Window != nil && p.active && p.phase == phasePlaying && !p.userPaused && p.cur != nil && !p.cur.eng.Ended()
	switch {
	case want && p.awakeRelease == nil:
		reason := p.title
		if reason == "" {
			reason = "Emby Playback"
		}
		p.awakeRelease = mygo.Power.KeepAwake(reason, true)
	case !want && p.awakeRelease != nil:
		rel := p.awakeRelease
		p.awakeRelease = nil
		rel()
	}
}

// syncNowPlaying publishes full track metadata to the OS media center.
// Gated strictly on Window != nil so headless tests never touch app-loop singletons.
func (p *Player) syncNowPlaying(now time.Time) {
	if p.cfg.Window == nil {
		return
	}
	pl := p.cur
	if !p.active || p.phase != phasePlaying || pl == nil || !pl.ready {
		return
	}
	dur := p.duration(now, pl.eng)
	state := mygo.PlaybackPlaying
	if p.userPaused || pl.eng.Ended() {
		state = mygo.PlaybackPaused
	}
	cmds := mygo.MediaPlay | mygo.MediaPause | mygo.MediaStop | mygo.MediaSeek
	if p.nextItem != nil {
		cmds |= mygo.MediaNext
	}
	mygo.NowPlaying.Set(mygo.NowPlayingInfo{
		Title:    p.title,
		Artist:   p.series,
		Duration: dur,
		Position: p.position(),
		Rate:     p.rate,
		State:    state,
		Commands: cmds,
	})
}

func (p *Player) syncNowPlayingPlayback() {
	if p.cfg.Window == nil || !p.active || p.phase != phasePlaying || p.cur == nil || !p.cur.ready {
		return
	}
	state := mygo.PlaybackPlaying
	if p.userPaused || p.cur.eng.Ended() {
		state = mygo.PlaybackPaused
	}
	mygo.NowPlaying.SetPlayback(state, p.position())
}

func (p *Player) clearOSMedia(releaseCommands bool) {
	if p.awakeRelease != nil {
		rel := p.awakeRelease
		p.awakeRelease = nil
		rel()
	}
	if p.cfg.Window != nil {
		mygo.NowPlaying.Clear()
	}
	if releaseCommands && p.mediaCmdOff != nil {
		off := p.mediaCmdOff
		p.mediaCmdOff = nil
		off()
	}
}
