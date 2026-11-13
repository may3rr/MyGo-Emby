package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"mygo-emby/internal/emby"
	"mygo-emby/internal/remux"
)

// TestGoodPlaceSubtitleMetadataDiagnostic compares PlaybackInfo's subtitle
// list with the tracks in the actual Matroska file and with remux's own
// enumeration. It is opt-in and read-only: it never reports playback and
// never logs the token or an authenticated URL.
func TestGoodPlaceSubtitleMetadataDiagnostic(t *testing.T) {
	if os.Getenv("MYGO_GOODPLACE_LIVE") != "1" {
		t.Skip("set MYGO_GOODPLACE_LIVE=1 to compare live Good Place subtitle metadata")
	}
	settingsPath := os.Getenv("MYGO_SETTINGS_PATH")
	if settingsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		settingsPath = filepath.Join(home, "Library", "Application Support", "MyGo Emby", "settings.json")
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	var accounts []storedServer
	if err := json.Unmarshal(settings.Servers, &accounts); err != nil {
		t.Fatalf("parse saved servers: %v", err)
	}
	var account *storedServer
	for i := range accounts {
		if accounts[i].ServerURL != "" {
			account = &accounts[i]
			break
		}
	}
	if account == nil {
		t.Fatal("no saved server")
	}
	if account.Token == "" && account.TokenRef != "" {
		service := os.Getenv("MYGO_SECRET_SERVICE")
		if service == "" {
			service = "dev.jackie.mygo-emby"
		}
		token, err := mygo.Secrets.Service(service).Get(account.TokenRef)
		if err != nil {
			t.Fatalf("read saved session token: %v", err)
		}
		account.Token = string(token)
	}
	if account.Token == "" {
		t.Fatal("saved server has no token")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := emby.RestoreClient(account.saved().Session, deviceInfo(settings.DeviceID))
	if err != nil {
		t.Fatalf("restore client: %v", err)
	}

	results, err := client.Search(ctx, "善地", 6)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var items []emby.Item
	items = append(items, results.Movies...)
	items = append(items, results.Episodes...)
	resume, resumeErr := client.ResumeItems(ctx, 12)
	if resumeErr != nil {
		t.Logf("resume items: %v", resumeErr)
	}
	nextUp, nextErr := client.NextUp(ctx, "", 12)
	if nextErr != nil {
		t.Logf("next up: %v", nextErr)
	}
	for _, item := range append(resume, nextUp...) {
		t.Logf("recent item=%s title=%q series=%q resume=%v", item.ID, item.DisplayTitle(), item.SeriesName, item.CanResume())
		if strings.Contains(item.SeriesName, "善地") || strings.Contains(item.Name, "善地") {
			items = appendGoodPlaceUnique(items, item)
		}
	}
	for _, series := range results.Series {
		seasons, err := client.Seasons(ctx, series.ID)
		if err != nil || len(seasons) == 0 {
			continue
		}
		episodes, err := client.Episodes(ctx, series.ID, seasons[0].ID)
		if err == nil && len(episodes) > 0 {
			items = appendGoodPlaceUnique(items, episodes[0])
		}
	}
	if len(items) == 0 {
		t.Fatal("search returned no playable item")
	}

	for _, item := range items {
		pb, err := client.PlaybackInfo(ctx, item.ID, emby.PlaybackOptions{SubtitleStreamIndex: emby.Index(-1)})
		if err != nil {
			t.Logf("item=%s playback-info skipped: %v", item.ID, err)
			continue
		}
		t.Logf("item=%s title=%q method=%s source=%s", item.ID, item.DisplayTitle(), pb.Method, pb.MediaSourceID)
		logServerSources(t, pb)
		logSubtitleTracks(t, "playback-info", pb.SubtitleTracks)
		logFileProbe(t, ctx, client.Token(), pb.StreamURL)
		logRemuxTracks(t, ctx, pb)
		for _, source := range pb.MediaSources {
			if len(source.MediaStreams) != 0 || source.ID == pb.MediaSourceID {
				continue
			}
			explicit, err := client.PlaybackInfo(ctx, item.ID, emby.PlaybackOptions{
				MediaSourceID:       source.ID,
				SubtitleStreamIndex: emby.Index(-1),
			})
			if err != nil {
				t.Logf("empty source=%s explicit playback-info: %v", source.ID, err)
				continue
			}
			t.Logf("empty source=%s actual tracks:", source.ID)
			logFileProbe(t, ctx, client.Token(), explicit.StreamURL)
			logRemuxTracks(t, ctx, explicit)
		}
	}
}

func appendGoodPlaceUnique(items []emby.Item, item emby.Item) []emby.Item {
	for i := range items {
		if items[i].ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func logServerSources(t *testing.T, pb *emby.Playback) {
	t.Helper()
	for _, source := range pb.MediaSources {
		t.Logf("source=%s selected=%v container=%q direct-play=%v direct-stream=%v transcode=%v streams=%d subtitles=%d",
			source.ID, source.ID == pb.MediaSourceID, source.Container,
			source.SupportsDirectPlay, source.SupportsDirectStream, source.SupportsTranscoding,
			len(source.MediaStreams), len(source.Streams(emby.StreamSubtitle)))
		for _, stream := range source.Streams(emby.StreamSubtitle) {
			t.Logf("  server subtitle index=%d codec=%q language=%q title=%q display=%q text=%v external=%v delivery=%q",
				stream.Index, stream.Codec, stream.Language, stream.Title, stream.DisplayTitle,
				stream.IsTextSubtitle(), stream.IsExternal, stream.DeliveryMethod)
		}
	}
}

func logSubtitleTracks(t *testing.T, origin string, tracks []emby.Track) {
	t.Helper()
	for _, track := range tracks {
		t.Logf("%s subtitle index=%d label=%q codec=%q language=%q title=%q display=%q text=%v burn=%v external=%v",
			origin, track.Index, track.Label(), track.Codec, track.Language, track.Title, track.DisplayTitle,
			track.IsText, track.NeedsBurnIn, track.IsExternal)
	}
}

func logFileProbe(t *testing.T, ctx context.Context, token, rawURL string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		t.Logf("file probe request: %v", err)
		return
	}
	req.Header.Set("Range", "bytes=0-4194303")
	req.Header.Set("X-Emby-Token", token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		t.Logf("file probe fetch: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		t.Logf("file probe status: %s", resp.Status)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		t.Logf("file probe read: %v", err)
		return
	}
	probe := "/opt/homebrew/bin/ffprobe"
	cmd := exec.CommandContext(ctx, probe, "-v", "error",
		"-probesize", "4194304", "-analyzeduration", "1000000",
		"-show_entries", "stream=index,codec_name,codec_type:stream_tags=language,title",
		"-of", "json", "pipe:0")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("ffprobe over %d range bytes: %v", len(data), err)
		return
	}
	var result struct {
		Streams []struct {
			Index     int    `json:"index"`
			CodecName string `json:"codec_name"`
			CodecType string `json:"codec_type"`
			Tags      struct {
				Language string `json:"language"`
				Title    string `json:"title"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Logf("parse ffprobe output: %v", err)
		return
	}
	for _, stream := range result.Streams {
		if stream.CodecType != "subtitle" {
			continue
		}
		t.Logf("  ffprobe subtitle index=%d codec=%q language=%q title=%q",
			stream.Index, stream.CodecName, stream.Tags.Language, stream.Tags.Title)
	}
}

func logRemuxTracks(t *testing.T, ctx context.Context, pb *emby.Playback) {
	t.Helper()
	srv, err := remux.NewServer()
	if err != nil {
		t.Logf("open remux server: %v", err)
		return
	}
	defer srv.Close()
	for _, source := range []struct {
		name string
		ms   *emby.MediaSource
	}{
		{name: "container-order"},
		{name: "server-mapped", ms: &pb.Source},
	} {
		session, err := srv.Open(ctx, remux.Source{
			URL:         pb.StreamURL,
			MediaSource: source.ms,
			AudioIndex:  -1,
			NoAudio:     true,
			Duration:    pb.Runtime(),
		})
		if err != nil {
			t.Logf("remux %s: %v", source.name, err)
			continue
		}
		for _, track := range session.Tracks {
			if track.Kind != remux.KindSubtitle {
				continue
			}
			t.Logf("  remux %s subtitle index=%d codec=%q language=%q title=%q supported=%v reason=%q",
				source.name, track.Index, track.Codec, track.Language, track.Title,
				track.Supported, track.Reason)
		}
		_ = session.Close()
	}
}
