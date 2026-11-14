package app

import (
	"testing"

	"mygo-emby/internal/emby"
)

func TestSourcePlayableWithoutProbe(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  emby.MediaSource
		want bool
	}{
		{"unprobed ISO", emby.MediaSource{Container: "blurayiso"}, false},
		{"server can transcode ISO", emby.MediaSource{Container: "blurayiso", SupportsTranscoding: true}, true},
		{"unprobed MKV", emby.MediaSource{Container: "mkv"}, true},
		{"unknown video codec", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{{Type: emby.StreamVideo}}}, true},
		{"audio without video", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{{Type: emby.StreamAudio, Codec: "aac"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourcePlayable(&tc.src); got != tc.want {
				t.Fatalf("sourcePlayable=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestItemDefaultsToMP4AfterUnprobedISO(t *testing.T) {
	a, tt, f := signedInApp(t, 1280, 900)
	f.set(func(f *fakeServer) {
		f.children[f.views[0].ID][0].MediaSources = []emby.MediaSource{
			{ID: "iso", Name: "70G ISO", Container: "blurayiso"},
			{ID: "mp4", Name: "MP4", Container: "mp4"},
		}
	})
	a.navigate(pathItem("Home-m-0"))
	settle(tt)
	a.waitFor(t, tt, "the detail page", func() bool { return tt.HasText(str.AddFavorite) })
	if tt.HasText("70G ISO") {
		t.Fatal("unprobed ISO is offered as a playable version")
	}
	if err := tt.Click(str.Play); err != nil {
		t.Fatal(err)
	}
	if a.lastPlay == nil || a.lastPlay.opts.MediaSourceID != "mp4" {
		t.Fatalf("default play: %+v", a.lastPlay)
	}
}
