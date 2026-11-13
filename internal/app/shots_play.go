package app

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"mygo-emby/internal/emby"
)

// With EMBY_SHOTS_PLAY=<item id>, the screenshot walk also plays that item
// in the window, through the player and, for a file AVPlayer cannot read,
// the local remuxer: the player loading, playing, with its controls hidden,
// and the item's page as the player closes, scrolled where it was. Playing
// reports to the server, so the item's watched state and resume point are
// noted first and put back after.
var shotPlay struct {
	id     string
	saved  *emby.UserData
	done   bool
	logged time.Time
}

func init() {
	shotPlay.id = os.Getenv("EMBY_SHOTS_PLAY")
	playing := func(a *app) bool { return shotPlay.id != "" }
	addShots(90,
		shotStep{If: playing, Do: func(a *app) {
			a.navigate(pathItem(shotPlay.id))
			cl, id := a.client, shotPlay.id
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if it, err := cl.Item(ctx, id); err == nil && it.UserData != nil {
					u := *it.UserData
					a.update(func() { shotPlay.saved = &u })
				}
			}()
		}, Until: func(a *app) bool {
			d := a.details[strings.TrimPrefix(a.router.Path(), "/item/")]
			return d != nil && d.item != nil && shotPlay.saved != nil
		}, Settle: 2 * time.Second},
		shotStep{If: playing, Do: func(a *app) { a.scrollPageTo(400) }, Wait: time.Second},
		shotStep{If: playing, Name: "play-page-before"},
		shotStep{If: playing, Do: func(a *app) {
			if d := a.details[shotPlay.id]; d != nil {
				a.play(d.item, playOptions{FromStart: true})
			}
		}, Wait: 1500 * time.Millisecond},
		shotStep{If: playing, Name: "play-loading"},
		shotStep{If: playing, Until: func(a *app) bool {
			if a.player == nil {
				return false
			}
			s := a.player.Status()
			if time.Since(shotPlay.logged) > 3*time.Second {
				shotPlay.logged = time.Now()
				log.Printf("shots: loading=%v ready=%v engineReady=%v engine=%v builds=%d buffering=%v method=%s audio=%d sub=%d err=%q",
					s.Loading, s.Ready, s.EngineReady, s.EnginePosition, s.VideoBuilds, s.Buffering, s.Method, s.AudioIndex, s.SubtitleIndex, s.ErrorText)
			}
			return s.Ready
		}, Wait: 40 * time.Second, Settle: 2 * time.Second},
		shotStep{If: playing, Name: "play-controls"},
		shotStep{If: playing, Do: func(a *app) {
			if a.player != nil {
				_ = a.player.Debug("hide-controls")
			}
		}, Wait: 2 * time.Second},
		shotStep{If: playing, Name: "play-picture"},
		shotStep{If: playing, Do: func(a *app) {
			if a.player != nil {
				s := a.player.Status()
				log.Printf("shots: playing at %v of %v, %s, ready=%v", s.Position, s.Duration, s.Method, s.Ready)
			} else {
				log.Printf("shots: nothing played")
			}
			a.closePlayer(5 * time.Second)
			a.refresh()
			cl, id, u := a.client, shotPlay.id, shotPlay.saved
			if u == nil {
				shotPlay.done = true
				return
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				r := emby.PlaybackReport{ItemID: id, PositionTicks: u.PlaybackPositionTicks, CanSeek: true, VolumeLevel: 100}
				if err := cl.ReportPlaybackStopped(ctx, r); err != nil {
					log.Printf("shots: restoring the resume point: %v", err)
				}
				if _, err := cl.SetPlayed(ctx, id, u.Played); err != nil {
					log.Printf("shots: restoring the played mark: %v", err)
				}
				if it, err := cl.Item(ctx, id); err == nil && it.UserData != nil {
					v := it.UserData
					log.Printf("shots: user data restored: %v (played=%v playCount=%d position=%d)",
						v.Played == u.Played && v.PlayCount == u.PlayCount && v.PlaybackPositionTicks == u.PlaybackPositionTicks,
						v.Played, v.PlayCount, v.PlaybackPositionTicks)
				}
				a.update(func() { shotPlay.done = true })
			}()
		}, Until: func(a *app) bool { return shotPlay.done }, Settle: 2 * time.Second},
		shotStep{If: playing, Name: "play-page-after"},
	)
}
