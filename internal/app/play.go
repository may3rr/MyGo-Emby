package app

import (
	"log"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/player"
	"mygo-emby/internal/remux"
)

// playOptions say how to play an item, as its page chose: from its start
// or from where the user left it, and which version, audio track and
// subtitles. Those left nil or "" are the player's to choose, by the
// settings' languages.
type playOptions struct {
	FromStart     bool
	StartAt       time.Duration
	HasStartAt    bool
	MediaSourceID string
	AudioIndex    *int
	// SubtitleIndex is the subtitle stream to show; -1 shows none.
	SubtitleIndex *int
}

// playRequest is what was last asked to play, for the tests.
type playRequest struct {
	item emby.Item
	opts playOptions
}

// play plays it as o says, over the whole window. Every play button and
// menu item of the app comes here.
func (a *app) play(it *emby.Item, o playOptions) {
	if it == nil {
		return
	}
	a.lastPlay = &playRequest{item: *it, opts: o}
	if a.client == nil || a.win == nil {
		return // tests, and a play asked as the user signs out
	}
	p := a.ensurePlayer()
	if p == nil {
		return
	}
	a.accountOpen = false
	p.SetSubtitleScale(a.settings.SubtitleScale)
	p.PlayWith(it, player.Options{
		FromStart:     o.FromStart,
		StartAt:       o.StartAt,
		HasStartAt:    o.HasStartAt,
		MediaSourceID: o.MediaSourceID,
		AudioIndex:    o.AudioIndex,
		SubtitleIndex: o.SubtitleIndex,
	})
}

// ensurePlayer makes the player for the signed-in client, and the local
// remuxer it plays files AVPlayer cannot read through, the first time.
func (a *app) ensurePlayer() *player.Player {
	if a.player != nil {
		return a.player
	}
	if a.remuxer == nil {
		srv, err := remux.NewServer()
		if err != nil {
			// Files AVPlayer reads as they are still play.
			log.Printf("remux: %v", err)
		}
		a.remuxer = srv
	}
	src := &player.ServerSource{Client: a.client}
	if a.remuxer != nil {
		src.Fallback = player.RemuxFallback(a.remuxer)
	}
	s := a.settings
	cfg := player.Config{
		Client:        a.client,
		Window:        a.win,
		Post:          a.update,
		MaxBitrate:    int64(s.MaxBitrate),
		AudioLang:     s.AudioLang,
		SubtitleLang:  s.SubtitleLang,
		SubtitleScale: s.SubtitleScale,
		AutoNext:      s.AutoPlayNext,
		Volume:        s.Volume,
		Image:         a.img.Get,
		Source:        src,
		OnExit:        a.playerExited,
	}
	if len(a.extPlayers) > 0 {
		cfg.ExternalPlayerLabel = a.extPlayers[0].Name
		cfg.OnOpenExternal = func(it *emby.Item, msID string, pos time.Duration) {
			if len(a.extPlayers) > 0 {
				a.openExternal(a.extPlayers[0], it, msID, pos)
			}
		}
	}
	a.player = player.New(cfg)
	return a.player
}

// playerExited takes back the window as the player closes: the volume the
// user left is kept, and the pages load again what playing changed (the
// resume points, what is next, what is watched).
func (a *app) playerExited(r player.Result) {
	scale := normalizeSubtitleScale(r.SubtitleScale)
	if r.Volume != a.settings.Volume || scale != a.settings.SubtitleScale {
		a.settings.Volume = r.Volume
		a.settings.SubtitleScale = scale
		a.form.subScale = labelOf(subScaleChoices, scale)
		a.saveSettings()
	}
	a.refresh()
}

// closePlayer ends what plays, as the account changes or the app quits,
// and waits up to wait for the stop report to reach the server.
func (a *app) closePlayer(wait time.Duration) {
	p := a.player
	if p == nil {
		return
	}
	a.player = nil
	p.Close()
	if wait > 0 {
		p.Flush(wait)
	}
}

// playerShown builds the player alone over the whole window while it
// plays, and reports so: each frame of the video builds the window, so
// nothing under it is built meanwhile (the pages keep their place in
// pageMems).
func (a *app) playerShown(c *ui.Context) bool {
	if a.player == nil || !a.player.Active() {
		return false
	}
	a.player.View(c)
	return true
}
