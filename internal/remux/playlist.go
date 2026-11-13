package remux

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (x *Session) masterPlaylist(src Source) []byte {
	vo := x.video.out
	codecs := vo.codecs
	if x.audioOut != nil {
		codecs += "," + x.audioOut.codecs
	}
	avg := int64(0)
	if src.MediaSource != nil && src.MediaSource.Bitrate > 0 {
		avg = src.MediaSource.Bitrate
	} else if x.info.size > 0 && x.Duration > 0 {
		avg = int64(float64(x.info.size) * 8 / x.Duration.Seconds())
	}
	if avg <= 0 {
		avg = 10_000_000
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,AVERAGE-BANDWIDTH=%d,CODECS=\"%s\"", avg*3/2, avg, codecs)
	if vo.width > 0 && vo.height > 0 {
		fmt.Fprintf(&b, ",RESOLUTION=%dx%d", vo.width, vo.height)
	}
	if vo.frameRate > 0 {
		fmt.Fprintf(&b, ",FRAME-RATE=%.3f", vo.frameRate)
	}
	vr := vo.colour.videoRange()
	if vo.fourcc == "dvh1" {
		vr = "PQ" // Dolby Vision profile 5
	}
	fmt.Fprintf(&b, ",VIDEO-RANGE=%s\nmedia.m3u8\n", vr)
	return []byte(b.String())
}

func (x *Session) mediaPlaylist() []byte {
	target := 1
	for i := range x.segs {
		if d := int(math.Round(x.segs[i].duration().Seconds())); d > target {
			target = d
		}
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:7\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString("#EXT-X-MAP:URI=\"init.mp4\"\n")
	for i := range x.segs {
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s.m4s\n", x.segs[i].duration().Seconds(), strconv.Itoa(i))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return []byte(b.String())
}
