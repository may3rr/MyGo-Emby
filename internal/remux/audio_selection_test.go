package remux

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"mygo-emby/internal/emby"
)

func TestSessionRejectsUnmappedExplicitAudio(t *testing.T) {
	data := readTestdata(t, "mkv_h264_aac_ass.mkv")
	srv, err := NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	for _, index := range []int{2, 99} {
		src := Source{AudioIndex: index, MediaSource: &emby.MediaSource{
			Container: "mkv", MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "h264"},
				{Index: 1, Type: emby.StreamAudio, Codec: "aac"},
				{Index: 2, Type: emby.StreamAudio, Codec: "aac", IsExternal: true},
			},
		}}
		bs := &readerAtSource{r: bytes.NewReader(data), n: int64(len(data))}
		x, err := newSession(context.Background(), srv, src, bs)
		if x != nil {
			x.Close()
		}
		if !errors.Is(err, ErrUnsupportedAudio) {
			t.Fatalf("audio %d silently changed to embedded audio: session=%v, err=%v", index, x != nil, err)
		}
	}
}
