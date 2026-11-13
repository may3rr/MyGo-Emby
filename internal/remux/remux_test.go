package remux

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

// The clips in testdata are made by testdata/gen.sh. With REMUX_DUMP=dir
// the tests also write each remuxed stream (init + segments) there, for
// checking with ffprobe.

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type cueLog struct {
	mu   sync.Mutex
	cues map[int][]emby.Cue
}

func (l *cueLog) add(track int, cues []emby.Cue) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cues == nil {
		l.cues = map[int][]emby.Cue{}
	}
	l.cues[track] = append(l.cues[track], cues...)
}

func (l *cueLog) all(track int) []emby.Cue {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]emby.Cue(nil), l.cues[track]...)
}

// openLocal serves data through a session as if it were remote.
func openLocal(t *testing.T, data []byte, src Source) (*Server, *Session) {
	t.Helper()
	srv, err := NewServer(WithLogf(t.Logf))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	x, err := newSession(context.Background(), srv, src, &readerAtSource{r: bytes.NewReader(data), n: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.add(x); err != nil {
		t.Fatal(err)
	}
	x.startReadAhead()
	return srv, x
}

func get(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %s", url, resp.Status)
	}
	return b
}

// box is a parsed ISO BMFF box for the checks.
type box struct {
	typ  string
	data []byte
}

func boxes(b []byte) []box {
	var out []box
	for len(b) >= 8 {
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			break
		}
		out = append(out, box{string(b[4:8]), b[8:n]})
		b = b[n:]
	}
	return out
}

func child(b []byte, typ string) []byte {
	for _, x := range boxes(b) {
		if x.typ == typ {
			return x.data
		}
	}
	return nil
}

// run is one track run of a fragment as the checks see it.
type run struct {
	track    uint32
	base     int64
	durs     []int64
	ctos     []int64
	sync     []bool
	sizes    []int
	dataSize int
}

func (r run) end() int64 {
	e := r.base
	for _, d := range r.durs {
		e += d
	}
	return e
}

// parseSegment returns the runs of every fragment of a media segment.
func parseSegment(t *testing.T, seg []byte) []run {
	t.Helper()
	var runs []run
	bs := boxes(seg)
	if len(bs) == 0 || bs[0].typ != "moof" {
		t.Fatalf("segment does not start with moof")
	}
	for _, b := range bs {
		if b.typ != "moof" {
			continue
		}
		for _, tr := range boxes(b.data) {
			if tr.typ != "traf" {
				continue
			}
			var r run
			tfhd := child(tr.data, "tfhd")
			r.track = binary.BigEndian.Uint32(tfhd[4:])
			tfdt := child(tr.data, "tfdt")
			r.base = int64(binary.BigEndian.Uint64(tfdt[4:]))
			trun := child(tr.data, "trun")
			flags := binary.BigEndian.Uint32(trun) & 0xffffff
			n := int(binary.BigEndian.Uint32(trun[4:]))
			p := trun[8:]
			if flags&1 != 0 {
				p = p[4:]
			}
			for i := 0; i < n; i++ {
				var d, s, f, c int64
				if flags&0x100 != 0 {
					d, p = int64(binary.BigEndian.Uint32(p)), p[4:]
				}
				if flags&0x200 != 0 {
					s, p = int64(binary.BigEndian.Uint32(p)), p[4:]
				}
				if flags&0x400 != 0 {
					f, p = int64(binary.BigEndian.Uint32(p)), p[4:]
				} else {
					f = sampleFlagsSync
				}
				if flags&0x800 != 0 {
					c, p = int64(int32(binary.BigEndian.Uint32(p))), p[4:]
				}
				r.durs = append(r.durs, d)
				r.ctos = append(r.ctos, c)
				r.sizes = append(r.sizes, int(s))
				r.sync = append(r.sync, f&0x10000 == 0)
				r.dataSize += int(s)
			}
			runs = append(runs, r)
		}
	}
	return runs
}

type streamCheck struct {
	videoFrames, audioFrames int
	segments                 int
	cues                     *cueLog
}

// fetchAll reads every segment in order, checks that each starts with a
// sync sample and that both tracks are continuous, and returns counts.
func fetchAll(t *testing.T, x *Session, dump string) streamCheck {
	t.Helper()
	base := strings.TrimSuffix(x.PlaylistURL, "master.m3u8")
	master := string(get(t, x.PlaylistURL))
	if !strings.Contains(master, "CODECS=") || !strings.Contains(master, "media.m3u8") {
		t.Fatalf("master playlist:\n%s", master)
	}
	media := string(get(t, base+"media.m3u8"))
	if !strings.Contains(media, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(media, "#EXT-X-ENDLIST") {
		t.Fatalf("media playlist:\n%s", media)
	}
	init := get(t, base+"init.mp4")
	if boxes(init)[0].typ != "ftyp" || boxes(init)[1].typ != "moov" {
		t.Fatal("bad init segment")
	}
	var out bytes.Buffer
	out.Write(init)
	var c streamCheck
	ends := map[uint32]int64{}
	for k := range x.segs {
		seg := get(t, fmt.Sprintf("%s%d.m4s", base, k))
		out.Write(seg)
		c.segments++
		firstVideo := true
		for _, r := range parseSegment(t, seg) {
			if r.track == 1 {
				if firstVideo && len(r.sync) > 0 && !r.sync[0] {
					t.Errorf("segment %d starts with a non-sync video sample", k)
				}
				firstVideo = false
				c.videoFrames += len(r.durs)
			} else {
				c.audioFrames += len(r.durs)
			}
			if e, ok := ends[r.track]; ok && len(r.durs) > 0 {
				gap := r.base - e
				tol := int64(2)
				if r.track == 1 {
					tol = int64(x.video.out.timescale) / 10 // last-frame duration estimate
				}
				if gap < -tol || gap > tol {
					t.Errorf("segment %d track %d: tfdt %d after end %d (gap %d)", k, r.track, r.base, e, gap)
				}
			}
			if len(r.durs) > 0 {
				ends[r.track] = r.end()
			}
		}
	}
	if dump != "" {
		name := filepath.Join(dump, strings.ReplaceAll(t.Name(), "/", "_")+".mp4")
		if err := os.WriteFile(name, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestRemuxClips(t *testing.T) {
	dump := os.Getenv("REMUX_DUMP")
	cases := []struct {
		file         string
		strip        bool // remove the Cues
		video, audio string
		frames       int // video frames in 10 s at 24 fps
		minAudio     int
		subTrack     int
		wantCueText  string
		indexed      bool
		minSegments  int
		hevcFourcc   string
	}{
		{file: "mkv_h264_aac_ass.mkv", video: "avc1.", audio: "mp4a.40.2", frames: 240, minAudio: 465, subTrack: -1, wantCueText: "ASS line one\nsecond row", indexed: true, minSegments: 3},
		{file: "mkv_h264_aac_ass.mkv", strip: true, video: "avc1.", audio: "mp4a.40.2", frames: 240, minAudio: 465, subTrack: -1, wantCueText: "Comma, inside, text", minSegments: 2},
		{file: "mkv_hevc_eac3_srt.mkv", video: "hvc1.2.4.", audio: "ec-3", frames: 240, minAudio: 310, subTrack: -1, wantCueText: "Hello, world!", indexed: true, minSegments: 3},
		{file: "mp4_hev1_aac.mp4", video: "hvc1.1.", audio: "mp4a.40.2", frames: 240, minAudio: 465, subTrack: -1, wantCueText: "第二行字幕\nsecond line", indexed: true, minSegments: 3},
		{file: "ts_h264_aac.ts", video: "avc1.", audio: "mp4a.40.2", frames: 240, minAudio: 465, minSegments: 3},
		{file: "ts_hevc_ac3.ts", video: "hvc1.1.", audio: "ac-3", frames: 240, minAudio: 310, minSegments: 3},
	}
	for _, tc := range cases {
		name := tc.file
		if tc.strip {
			name = "nocues_" + name
		}
		t.Run(name, func(t *testing.T) {
			data := readTestdata(t, tc.file)
			if tc.strip {
				data = stripCues(t, data)
			}
			log := &cueLog{}
			_, x := openLocal(t, data, Source{AudioIndex: -1, OnCues: log.add})
			master := string(x.master)
			if !strings.Contains(master, tc.video) || !strings.Contains(master, tc.audio) {
				t.Fatalf("codecs: %s", master)
			}
			if indexed := x.dmx.keyframes() != nil; indexed != tc.indexed {
				t.Errorf("indexed %v, want %v", indexed, tc.indexed)
			}
			if x.Duration < 9900*time.Millisecond || x.Duration > 10200*time.Millisecond {
				t.Errorf("duration %v", x.Duration)
			}
			c := fetchAll(t, x, dump)
			if c.videoFrames != tc.frames {
				t.Errorf("video frames %d, want %d", c.videoFrames, tc.frames)
			}
			if c.audioFrames < tc.minAudio {
				t.Errorf("audio frames %d, want at least %d", c.audioFrames, tc.minAudio)
			}
			if c.segments < tc.minSegments {
				t.Errorf("%d segments", c.segments)
			}
			if tc.wantCueText != "" {
				found := false
				for _, cue := range log.all(tc.subTrack) {
					if cue.Text == tc.wantCueText {
						found = true
					}
				}
				if !found {
					t.Errorf("cue %q missing in %q", tc.wantCueText, log.all(tc.subTrack))
				}
			}
		})
	}
}

// stripCues turns a Matroska file's Cues element into a Void element of
// the same size.
func stripCues(t *testing.T, b []byte) []byte {
	t.Helper()
	b = append([]byte(nil), b...)
	i := bytes.Index(b, []byte{0x1c, 0x53, 0xbb, 0x6b})
	for i >= 0 {
		size, n := readVint(b[i+4:])
		if n > 0 && size > 0 && i+4+n+int(size) <= len(b) {
			break
		}
		j := bytes.Index(b[i+1:], []byte{0x1c, 0x53, 0xbb, 0x6b})
		if j < 0 {
			i = -1
			break
		}
		i += 1 + j
	}
	if i < 0 {
		t.Fatal("no Cues element")
	}
	size, n := readVint(b[i+4:])
	// Void ID (1 byte) and a size vint of n+3 bytes, same total length.
	l := n + 3
	v := uint64(size) | uint64(1)<<(7*uint(l))
	b[i] = 0xec
	for k := 0; k < l; k++ {
		b[i+1+k] = byte(v >> (8 * uint(l-1-k)))
	}
	return b
}

func TestDecide(t *testing.T) {
	stream := func(i int, typ, codec, tag string, def bool) emby.MediaStream {
		return emby.MediaStream{Index: i, Type: typ, Codec: codec, CodecTag: tag, IsDefault: def}
	}
	cases := []struct {
		name  string
		ms    emby.MediaSource
		audio int
		mode  Mode
		aidx  int
		unsup bool
		tx    bool
	}{
		{"mp4 avc1 aac", emby.MediaSource{Container: "mov,mp4,m4a", MediaStreams: []emby.MediaStream{stream(0, "Video", "h264", "avc1", true), stream(1, "Audio", "aac", "mp4a", true)}}, -1, DirectPlay, 1, false, false},
		{"mp4 hev1", emby.MediaSource{Container: "mov", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "hev1", true), stream(1, "Audio", "eac3", "ec-3", true)}}, -1, Remux, 1, false, false},
		{"mp4 second audio", emby.MediaSource{Container: "mp4", MediaStreams: []emby.MediaStream{stream(0, "Video", "h264", "avc1", true), stream(1, "Audio", "aac", "", true), stream(2, "Audio", "ac3", "", false)}}, 2, Remux, 2, false, false},
		{"mkv", emby.MediaSource{Container: "matroska", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "", true), stream(1, "Audio", "eac3", "", true), stream(2, "Subtitle", "subrip", "", false)}}, -1, Remux, 1, false, false},
		{"mkv dts default", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "", true), stream(1, "Audio", "dts", "", true), stream(2, "Audio", "eac3", "", false)}}, -1, Remux, 2, false, false},
		{"mkv dts chosen", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "", true), stream(1, "Audio", "dts", "", true), stream(2, "Audio", "eac3", "", false)}}, 1, Remux, 1, false, true},
		{"mkv truehd only", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "", true), stream(1, "Audio", "truehd", "", true)}}, -1, Remux, 1, false, true},
		{"mkv opus only", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "h264", "", true), stream(1, "Audio", "opus", "", true)}}, -1, Remux, 1, false, true},
		{"mkv flac only", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "h264", "", true), stream(1, "Audio", "flac", "", true)}}, -1, Remux, 1, false, true},
		{"iso", emby.MediaSource{Container: "blurayiso", MediaStreams: []emby.MediaStream{stream(0, "Video", "hevc", "", true)}}, -1, Unsupported, -1, false, false},
		{"vc1", emby.MediaSource{Container: "mkv", MediaStreams: []emby.MediaStream{stream(0, "Video", "vc1", "", true)}}, -1, Unsupported, -1, false, false},
		{"unprobed", emby.MediaSource{Container: "mkv"}, -1, Remux, -1, false, false},
		{"mov cover art", emby.MediaSource{Container: "mov", MediaStreams: []emby.MediaStream{stream(0, "Video", "h264", "avc1", true), stream(1, "Video", "mjpeg", "", false), stream(2, "Audio", "aac", "mp4a", true)}}, -1, DirectPlay, 2, false, false},
	}
	for _, tc := range cases {
		d := Decide(&tc.ms, tc.audio)
		if d.Mode != tc.mode || d.AudioIndex != tc.aidx || d.AudioUnsupported != tc.unsup || d.AudioTranscode != tc.tx {
			t.Errorf("%s: %+v", tc.name, d)
		}
	}
}

func TestCueText(t *testing.T) {
	cases := []struct{ codec, in, want string }{
		{"subrip", "Hello, <i>world</i>!\r\n", "Hello, world!"},
		{"subrip", "{\\an8}Top &amp; up", "Top & up"},
		{"ass", "0,0,Default,,0,0,0,,{\\i1}One{\\i0}\\NTwo\\hthree", "One\nTwo three"},
		{"ass", "3,0,Sign,,0,0,0,,{\\p1}m 0 0 l 10 10{\\p0}", ""},
		{"mov_text", "plain", "plain"},
	}
	for _, c := range cases {
		cue, ok := makeCue(&packet{t: &track{codec: c.codec}, data: []byte(c.in), dur: time.Second})
		if got := cue.Text; got != c.want || ok != (c.want != "") {
			t.Errorf("%s %q: %q", c.codec, c.in, got)
		}
	}
}

func TestCodecStrings(t *testing.T) {
	// HEVC Main 10, level 5.1, progressive and frame-only constraints.
	hdr := []byte{1, 0x02, 0x20, 0, 0, 0, 0xb0, 0, 0, 0, 0, 0, 153}
	if got := hevcCodecString("hvc1", hdr); got != "hvc1.2.4.L153.B0" {
		t.Errorf("hevc codec string %q", got)
	}
	if got := packLanguage("eng"); got != 0x15c7 {
		t.Errorf("language %x", got)
	}
}

func TestPlanSegments(t *testing.T) {
	var kfs []keyframe
	for s := 0; s < 60; s += 3 {
		kfs = append(kfs, keyframe{t: time.Duration(s) * time.Second, pos: int64(s)})
	}
	segs := planSegments(60*time.Second, kfs, 0)
	var starts []time.Duration
	for _, s := range segs {
		starts = append(starts, s.start)
	}
	want := "[0s 3s 6s 12s 15s 18s 24s 27s 30s 36s 39s 42s 48s 51s 54s]"
	if fmt.Sprint(starts) != want {
		t.Errorf("starts %v", starts)
	}
	if !segs[len(segs)-1].last || segs[len(segs)-1].end != 60*time.Second {
		t.Error("last segment")
	}
	grid := planSegments(10500*time.Millisecond, nil, 0)
	if len(grid) != 3 || grid[2].end != 10500*time.Millisecond {
		t.Errorf("grid %+v", grid)
	}
}
