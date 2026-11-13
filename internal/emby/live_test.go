package emby

// Live integration tests. They are skipped unless EMBY_LIVE=1.
//
//	EMBY_LIVE=1 go test ./internal/emby -run Live -v
//
// The public Jellyfin demo (https://demo.jellyfin.org/stable, user "demo",
// empty password) is always used. An Emby server is added when
// EMBY_TEST_SERVER, EMBY_TEST_USER and EMBY_TEST_PASS are set; never commit
// those values. Against servers other than the demo the tests stay
// read-only except for a favorite toggle that is reverted at once and
// playback reports at position 0 on an item without progress (user data is
// compared before and after and restored if the server changed it).

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

type liveTarget struct {
	name     string
	server   string
	user     string
	pass     string
	readOnly bool // shared real server: minimise writes
}

func liveTargets(t *testing.T) []liveTarget {
	if os.Getenv("EMBY_LIVE") != "1" {
		t.Skip("set EMBY_LIVE=1 to run live tests")
	}
	targets := []liveTarget{{name: "jellyfin-demo", server: "https://demo.jellyfin.org/stable", user: "demo"}}
	if s := os.Getenv("EMBY_TEST_SERVER"); s != "" {
		targets = append(targets, liveTarget{name: "emby", server: s, user: os.Getenv("EMBY_TEST_USER"),
			pass: os.Getenv("EMBY_TEST_PASS"), readOnly: true})
	}
	if only := os.Getenv("EMBY_LIVE_ONLY"); only != "" {
		var f []liveTarget
		for _, tg := range targets {
			if tg.name == only {
				f = append(f, tg)
			}
		}
		targets = f
	}
	return targets
}

var liveDevice = DeviceInfo{Client: "MyGo Emby", Device: "MyGo live test", DeviceID: "mygo-emby-livetest-1", Version: "0.1.0"}

// retry runs f up to 4 times while it fails with a transient error
// (unreachable, 5xx, 429), because the network path is a flaky proxy.
func retry[T any](t *testing.T, what string, f func(ctx context.Context) (T, error)) (T, error) {
	t.Helper()
	var (
		v   T
		err error
	)
	for attempt := 1; attempt <= 4; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		v, err = f(ctx)
		cancel()
		if err == nil {
			return v, nil
		}
		code := StatusCode(err)
		transient := errors.Is(err, ErrUnreachable) || errors.Is(err, context.DeadlineExceeded) ||
			code == 502 || code == 503 || code == 504 || code == 429
		if !transient {
			return v, err
		}
		t.Logf("%s: attempt %d failed (%v), retrying", what, attempt, err)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}
	return v, err
}

func must[T any](t *testing.T, what string, f func(ctx context.Context) (T, error)) T {
	t.Helper()
	v, err := retry(t, what, f)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return v
}

// rawGet fetches a URL with a plain client (no auth headers, like AVPlayer)
// and returns status, content type and at most n bytes. Redirects are
// followed. rng adds a Range header.
func rawGet(t *testing.T, u string, n int64, rng bool) (int, string, []byte) {
	t.Helper()
	type res struct {
		code int
		ct   string
		body []byte
	}
	r, err := retry(t, "GET "+redactURL(u), func(ctx context.Context) (res, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if rng {
			req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", n-1))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return res{}, &Error{Kind: KindUnreachable, Err: err}
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, n))
		if resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			return res{}, &Error{Kind: KindHTTP, StatusCode: resp.StatusCode}
		}
		return res{resp.StatusCode, resp.Header.Get("Content-Type"), b}, nil
	})
	if err != nil {
		t.Fatalf("GET %s: %v", redactURL(u), err)
	}
	return r.code, r.ct, r.body
}

// sniffVideo names the container from magic bytes.
func sniffVideo(b []byte) string {
	switch {
	case len(b) >= 8 && string(b[4:8]) == "ftyp":
		return "mp4"
	case len(b) >= 4 && bytes.Equal(b[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}):
		return "matroska"
	case len(b) >= 1 && b[0] == 0x47:
		return "mpegts"
	case bytes.HasPrefix(bytes.TrimSpace(b), []byte("#EXTM3U")):
		return "m3u8"
	}
	return ""
}

// firstURI returns the first non-comment line of an m3u8 playlist,
// resolved against base.
func firstURI(t *testing.T, base string, playlist []byte) string {
	t.Helper()
	sc := bufio.NewScanner(bytes.NewReader(playlist))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		bu, _ := url.Parse(base)
		ru, err := url.Parse(l)
		if err != nil {
			t.Fatalf("bad playlist line %q", l)
		}
		return bu.ResolveReference(ru).String()
	}
	t.Fatalf("playlist has no URI:\n%s", playlist)
	return ""
}

func TestLive(t *testing.T) {
	for _, tg := range liveTargets(t) {
		t.Run(tg.name, func(t *testing.T) { runLive(t, tg) })
	}
}

func runLive(t *testing.T, tg liveTarget) {
	c, err := NewClient(tg.server, liveDevice)
	if err != nil {
		t.Fatal(err)
	}
	info := must(t, "PublicInfo", c.PublicInfo)
	t.Logf("server %q version %s product %q kind %s", info.ServerName, info.Version, info.ProductName, info.Kind())

	// Wrong password must map to ErrUnauthorized.
	{
		bad, _ := NewClient(tg.server, liveDevice)
		_, err := retry(t, "Login(wrong)", func(ctx context.Context) (Session, error) {
			return bad.Login(ctx, tg.user, tg.pass+"-definitely-wrong")
		})
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("wrong password: err = %v, want ErrUnauthorized", err)
		} else {
			t.Logf("wrong password -> %v", err)
		}
	}

	sess := must(t, "Login", func(ctx context.Context) (Session, error) { return c.Login(ctx, tg.user, tg.pass) })
	t.Logf("logged in as %s (%s), server kind %s", sess.UserName, sess.UserID, sess.ServerKind)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := c.Logout(ctx); err != nil {
			t.Logf("logout: %v", err)
		}
	}()

	// Restore from the persisted session and validate the token.
	rc, err := RestoreClient(sess, liveDevice)
	if err != nil {
		t.Fatal(err)
	}
	u := must(t, "CurrentUser", rc.CurrentUser)
	if u.ID != sess.UserID {
		t.Errorf("CurrentUser id %q != %q", u.ID, sess.UserID)
	}
	if sess.UserImageTag != "" {
		code, ct, _ := rawGet(t, c.UserImageURL(sess.UserID, sess.UserImageTag, ImageOptions{MaxWidth: 64}), 1<<16, false)
		t.Logf("user image: %d %s", code, ct)
		if code != 200 || !strings.HasPrefix(ct, "image/") {
			t.Errorf("user image: %d %s", code, ct)
		}
	}

	views := must(t, "Views", rc.Views)
	var movieView, tvView *Item
	for i := range views {
		v := &views[i]
		t.Logf("view %q type=%s collection=%s", v.Name, v.Type, v.CollectionType)
		if v.CollectionType == CollectionMovies && movieView == nil {
			movieView = v
		}
		if v.CollectionType == CollectionTVShows && tvView == nil {
			tvView = v
		}
	}
	if len(views) == 0 {
		t.Fatal("no views")
	}
	if code, ct, _ := rawGet(t, c.ItemImageURL(&views[0], RolePrimary, ImageOptions{MaxWidth: 400}), 1<<16, false); code != 200 || !strings.HasPrefix(ct, "image/") {
		t.Errorf("view image: %d %s", code, ct)
	}

	resume := must(t, "ResumeItems", func(ctx context.Context) ([]Item, error) { return rc.ResumeItems(ctx, 12) })
	t.Logf("resume: %d items", len(resume))
	for _, it := range resume {
		t.Logf("  resume %q %s at %s (%.0f%%)", it.DisplayTitle(), it.Type, FormatClock(it.ResumePosition()), it.PlayedPercentage())
	}
	next := must(t, "NextUp", func(ctx context.Context) ([]Item, error) { return rc.NextUp(ctx, "", 12) })
	t.Logf("next up: %d items", len(next))

	if movieView != nil {
		latest := must(t, "Latest", func(ctx context.Context) ([]Item, error) { return rc.Latest(ctx, movieView.ID, 8) })
		t.Logf("latest in %q: %d items", movieView.Name, len(latest))
		if len(latest) == 0 {
			t.Error("latest movies empty")
		}
	}
	if tvView != nil {
		latest := must(t, "Latest tv", func(ctx context.Context) ([]Item, error) { return rc.Latest(ctx, tvView.ID, 8) })
		t.Logf("latest in %q: %d items (types %v)", tvView.Name, len(latest), typesOf(latest))
	}

	// Paging.
	parent := ""
	if movieView != nil {
		parent = movieView.ID
	}
	q := ItemQuery{ParentID: parent, IncludeItemTypes: []string{TypeMovie}, Recursive: true,
		SortBy: []string{SortName}, SortOrder: Ascending, Limit: 4}
	p1 := must(t, "Items page 1", func(ctx context.Context) (*ItemsResult, error) { return rc.Items(ctx, q) })
	q.StartIndex = 4
	p2 := must(t, "Items page 2", func(ctx context.Context) (*ItemsResult, error) { return rc.Items(ctx, q) })
	t.Logf("movies total %d, page1 %v, page2 %v", p1.TotalRecordCount, namesOf(p1.Items), namesOf(p2.Items))
	if p1.TotalRecordCount < len(p1.Items) || len(p1.Items) == 0 {
		t.Fatalf("bad paging result %+v", p1)
	}
	if len(p2.Items) > 0 && p2.Items[0].ID == p1.Items[0].ID {
		t.Error("page 2 repeats page 1")
	}
	// Filters and sort variants used by the library page.
	for _, f := range [][]string{{FilterIsUnplayed}, {FilterIsFavorite}} {
		r := must(t, "Items filter", func(ctx context.Context) (*ItemsResult, error) {
			return rc.Items(ctx, ItemQuery{ParentID: parent, IncludeItemTypes: []string{TypeMovie}, Recursive: true,
				Filters: f, SortBy: []string{SortDateCreated}, SortOrder: Descending, Limit: 2})
		})
		t.Logf("filter %v: total %d", f, r.TotalRecordCount)
	}

	// Movie detail.
	movie := must(t, "Item(movie)", func(ctx context.Context) (*Item, error) { return rc.Item(ctx, p1.Items[0].ID) })
	t.Logf("movie %q year %d runtime %s rating %s official %q genres %v people %d studios %v sources %d chapters %d providers %v",
		movie.Name, movie.Year(), movie.RuntimeText(), movie.RatingText(), movie.OfficialRating, movie.Genres,
		len(movie.People), movie.StudioNames(), len(movie.MediaSources), len(movie.Chapters), movie.ProviderIDs)
	if len(movie.MediaSources) == 0 {
		t.Error("movie detail has no MediaSources")
	}
	posterURL := c.ItemImageURL(movie, RolePoster, ImageOptions{MaxWidth: 300, Quality: 90})
	code, ct, body := rawGet(t, posterURL, 1<<20, false)
	t.Logf("poster %s -> %d %s %d bytes", posterURL, code, ct, len(body))
	if code != 200 || !strings.HasPrefix(ct, "image/") {
		t.Errorf("poster: %d %s", code, ct)
	}
	if r, ok := movie.Image(RoleBackdrop); ok {
		code, ct, _ := rawGet(t, c.ImageRefURL(r, ImageOptions{MaxWidth: 1280}), 1<<20, false)
		if code != 200 || !strings.HasPrefix(ct, "image/") {
			t.Errorf("backdrop: %d %s", code, ct)
		}
	}
	for _, p := range movie.People {
		if url := c.PersonImageURL(p, ImageOptions{MaxWidth: 120}); url != "" {
			code, ct, _ := rawGet(t, url, 1<<20, false)
			t.Logf("person %q image -> %d %s", p.Name, code, ct)
			if code != 200 {
				t.Errorf("person image %d", code)
			}
			break
		}
	}
	similar := must(t, "Similar", func(ctx context.Context) ([]Item, error) { return rc.Similar(ctx, movie.ID, 6) })
	t.Logf("similar to %q: %v", movie.Name, namesOf(similar))

	// Series, seasons, episodes, next episode.
	sr := must(t, "Items(series)", func(ctx context.Context) (*ItemsResult, error) {
		return rc.Items(ctx, ItemQuery{IncludeItemTypes: []string{TypeSeries}, Recursive: true, SortBy: []string{SortDateCreated}, SortOrder: Descending, Limit: 5})
	})
	var firstEpisode *Item
	for _, s := range sr.Items {
		seasons := must(t, "Seasons", func(ctx context.Context) ([]Item, error) { return rc.Seasons(ctx, s.ID) })
		if len(seasons) == 0 {
			continue
		}
		eps := must(t, "Episodes", func(ctx context.Context) ([]Item, error) { return rc.Episodes(ctx, s.ID, seasons[0].ID) })
		t.Logf("series %q: %d seasons, season %q has %d episodes", s.Name, len(seasons), seasons[0].Name, len(eps))
		if len(eps) < 2 {
			continue
		}
		for i := range eps[:min(3, len(eps))] {
			e := &eps[i]
			t.Logf("  %s runtime %s landscape=%v poster=%v", e.DisplayTitle(), e.RuntimeText(), refString(e, RoleLandscape), refString(e, RolePoster))
		}
		firstEpisode = &eps[0]
		nx := must(t, "NextEpisode", func(ctx context.Context) (*Item, error) { return rc.NextEpisode(ctx, firstEpisode) })
		if nx == nil || nx.ID == firstEpisode.ID {
			t.Errorf("NextEpisode(%s) = %v", firstEpisode.DisplayTitle(), nx)
		} else {
			t.Logf("NextEpisode(%s) = %s", firstEpisode.DisplayTitle(), nx.DisplayTitle())
			if nx.ID != eps[1].ID {
				t.Logf("  note: next episode differs from list order (%s)", eps[1].DisplayTitle())
			}
		}
		last := &eps[len(eps)-1]
		nl := must(t, "NextEpisode(last)", func(ctx context.Context) (*Item, error) { return rc.NextEpisode(ctx, last) })
		if nl != nil {
			t.Logf("NextEpisode(last of season %s) = %s (crosses season)", last.DisplayTitle(), nl.DisplayTitle())
		} else {
			t.Logf("NextEpisode(last of season %s) = nil", last.DisplayTitle())
		}
		code, ct, _ := rawGet(t, c.ItemImageURL(firstEpisode, RoleLandscape, ImageOptions{MaxWidth: 480}), 1<<20, false)
		if code != 200 || !strings.HasPrefix(ct, "image/") {
			t.Errorf("episode landscape image: %d %s", code, ct)
		}
		break
	}
	if firstEpisode == nil {
		t.Error("found no series with at least two episodes")
	}

	// Search.
	term := strings.Fields(movie.Name)[0]
	sres := must(t, "Search", func(ctx context.Context) (*SearchResults, error) { return rc.Search(ctx, term, 6) })
	t.Logf("search %q: movies %v series %v episodes %d people %d boxsets %d", term, namesOf(sres.Movies), namesOf(sres.Series),
		len(sres.Episodes), len(sres.People), len(sres.BoxSets))
	if len(sres.Movies) == 0 {
		t.Errorf("search %q found no movies", term)
	}

	// Person search, and the person's filmography.
	if cast := movie.Cast(); len(cast) > 0 {
		name := cast[0].Name
		pr := must(t, "Search(person)", func(ctx context.Context) (*SearchResults, error) { return rc.Search(ctx, name, 5) })
		t.Logf("search person %q: people %v", name, namesOf(pr.People))
		if len(pr.People) == 0 {
			t.Errorf("person search for %q found nobody", name)
		}
		works, err := retry(t, "PersonItems", func(ctx context.Context) (*ItemsResult, error) { return rc.PersonItems(ctx, cast[0].ID, 0, 10) })
		switch {
		case err != nil && tg.readOnly && StatusCode(err) == 500:
			t.Logf("NOTE: server cannot filter by PersonIds: %v", err)
		case err != nil:
			t.Errorf("PersonItems: %v", err)
		default:
			t.Logf("works of %q: total %d %v", name, works.TotalRecordCount, namesOf(works.Items))
			if works.TotalRecordCount == 0 {
				t.Errorf("PersonItems(%q) is empty although the person is in %q", name, movie.Name)
			}
		}
	}

	// Favorites (reverted at once) and played toggle (demo only).
	{
		before := movie.IsFavorite()
		ud := must(t, "SetFavorite", func(ctx context.Context) (*UserData, error) { return rc.SetFavorite(ctx, movie.ID, !before) })
		if ud.IsFavorite == before {
			t.Errorf("SetFavorite(%v) returned %+v", !before, ud)
		}
		ud = must(t, "SetFavorite restore", func(ctx context.Context) (*UserData, error) { return rc.SetFavorite(ctx, movie.ID, before) })
		if ud.IsFavorite != before {
			t.Errorf("favorite not restored: %+v", ud)
		}
		if !tg.readOnly {
			wasPlayed := movie.IsPlayed()
			ud := must(t, "SetPlayed", func(ctx context.Context) (*UserData, error) { return rc.SetPlayed(ctx, movie.ID, !wasPlayed) })
			if ud.Played == wasPlayed {
				t.Errorf("SetPlayed returned %+v", ud)
			}
			must(t, "SetPlayed restore", func(ctx context.Context) (*UserData, error) { return rc.SetPlayed(ctx, movie.ID, wasPlayed) })
		}
	}

	livePlayback(t, c, rc, tg, p1.Items, firstEpisode)
}

func livePlayback(t *testing.T, c, rc *Client, tg liveTarget, movies []Item, episode *Item) {
	// Candidates: a few movies with media info.
	cand := must(t, "Items(media sources)", func(ctx context.Context) (*ItemsResult, error) {
		return rc.Items(ctx, ItemQuery{IncludeItemTypes: []string{TypeMovie, TypeEpisode}, Recursive: true, Limit: 60,
			SortBy: []string{SortDateCreated}, SortOrder: Descending, Fields: []string{FieldMediaSources}})
	})
	var direct, needsTranscode, withTextSub []*Item
	for i := range cand.Items {
		it := &cand.Items[i]
		if len(it.MediaSources) == 0 {
			continue
		}
		ms := &it.MediaSources[0]
		if avPlayerCanPlay(ms, effectiveAudioIndex(ms, nil)) {
			direct = append(direct, it)
		} else {
			needsTranscode = append(needsTranscode, it)
		}
		for _, s := range ms.MediaStreams {
			if s.IsTextSubtitle() {
				withTextSub = append(withTextSub, it)
				break
			}
		}
	}
	t.Logf("playback candidates: %d natively playable, %d needing transcode, %d with text subtitles", len(direct), len(needsTranscode), len(withTextSub))

	check := func(name string, it *Item, opts PlaybackOptions) *Playback {
		t.Helper()
		p := must(t, "PlaybackInfo "+name, func(ctx context.Context) (*Playback, error) { return rc.PlaybackInfo(ctx, it.ID, opts) })
		t.Logf("%s: %q container=%s method=%s hls=%v native=%v transcodeUnavailable=%v reasons=%v audio=%d sub=%d tracks a%d/s%d",
			name, it.Name, p.Source.Container, p.Method, p.IsHLS, p.NativelyPlayable, p.TranscodeUnavailable, p.TranscodeReasons,
			p.AudioStreamIndex, p.SubtitleStreamIndex, len(p.AudioTracks), len(p.SubtitleTracks))
		t.Logf("  stream %s", strings.ReplaceAll(p.StreamURL, rc.Token(), "<token>"))
		if !strings.Contains(p.StreamURL, "api_key=") || !strings.Contains(p.StreamURL, "ApiKey=") {
			if strings.HasPrefix(p.StreamURL, rc.BaseURL()) {
				t.Errorf("stream url lacks token params")
			}
		}
		if p.IsHLS {
			code, _, pl := rawGet(t, p.StreamURL, 1<<16, false)
			if code != 200 || sniffVideo(pl) != "m3u8" {
				t.Fatalf("master playlist: %d %q", code, pl)
			}
			variant := firstURI(t, p.StreamURL, pl)
			code, _, vpl := rawGet(t, variant, 1<<18, false)
			if code != 200 || sniffVideo(vpl) != "m3u8" {
				t.Fatalf("variant playlist: %d %q", code, vpl)
			}
			t.Logf("  master + variant playlists OK (%d bytes variant)", len(vpl))
			if !tg.readOnly {
				seg := firstURI(t, variant, vpl)
				code, ct, sb := rawGet(t, seg, 4096, false)
				t.Logf("  first segment: %d %s sniff=%s", code, ct, sniffVideo(sb))
				if code != 200 || sniffVideo(sb) != "mpegts" {
					t.Errorf("first segment not MPEG-TS: %d %s % x", code, ct, sb[:min(8, len(sb))])
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := rc.StopTranscoding(ctx, p.PlaySessionID); err != nil {
				t.Errorf("StopTranscoding: %v", err)
			}
			cancel()
		} else {
			code, ct, b := rawGet(t, p.StreamURL, 1024, true)
			t.Logf("  first bytes: %d %s sniff=%s", code, ct, sniffVideo(b))
			if (code != 200 && code != 206) || sniffVideo(b) == "" {
				t.Errorf("stream did not answer with video: %d %s % x", code, ct, b[:min(16, len(b))])
			}
		}
		return p
	}

	var reported *Playback
	if len(direct) > 0 {
		reported = check("direct", direct[0], PlaybackOptions{})
		if reported.Method != DirectPlay {
			t.Errorf("expected DirectPlay for %q, got %s", direct[0].Name, reported.Method)
		}
	} else {
		t.Log("no natively playable item found among candidates")
	}
	if len(needsTranscode) > 0 {
		p := check("needs-transcode", needsTranscode[0], PlaybackOptions{MaxStreamingBitrate: 4_000_000})
		if p.Method == Transcode && !p.IsHLS {
			t.Errorf("transcode is not HLS")
		}
	}
	if len(direct) > 0 {
		p := check("forced-transcode", direct[0], PlaybackOptions{ForceTranscode: true, MaxStreamingBitrate: 4_000_000})
		if p.Method != Transcode && !p.TranscodeUnavailable {
			t.Errorf("ForceTranscode gave %s", p.Method)
		}
	}
	if episode != nil {
		check("episode", episode, PlaybackOptions{})
	}

	// Subtitles: fetch the first text track that yields cues.
	got, answered := false, 0
	for _, it := range withTextSub[:min(3, len(withTextSub))] {
		p := must(t, "PlaybackInfo(sub)", func(ctx context.Context) (*Playback, error) { return rc.PlaybackInfo(ctx, it.ID, PlaybackOptions{}) })
		for _, tr := range p.SubtitleTracks {
			if !tr.IsText {
				continue
			}
			subs, err := retry(t, "FetchSubtitles", func(ctx context.Context) (*Subtitles, error) { return rc.FetchSubtitles(ctx, tr.URL) })
			t.Logf("subtitle %q track %d %s (%s, external=%v delivery=%s): %d cues, err=%v", it.Name, tr.Index, tr.Label(), tr.Codec,
				tr.IsExternal, tr.DeliveryMethod, subs.Len(), err)
			t.Logf("  url %s", strings.ReplaceAll(tr.URL, rc.Token(), "<token>"))
			if err != nil && !errors.Is(err, ErrNoCues) {
				t.Errorf("subtitle fetch failed: %v", err)
				continue
			}
			answered++
			if err == nil && subs.Len() > 0 {
				c0 := subs.Cues[0]
				t.Logf("  first cue %s-%s %q; At(first)=%q", FormatClock(c0.Start), FormatClock(c0.End), c0.Text, subs.TextAt(c0.Start))
				got = true
			}
			break // one text track per item keeps request volume low
		}
		if got {
			break
		}
	}
	if !got {
		t.Logf("NOTE: no text subtitle with cues found on %s (%d subtitle URLs answered with a valid but empty WebVTT)", tg.name, answered)
	}

	// Playback reporting at position 0 on an item whose user data is
	// pristine (never played), so it can be restored exactly afterwards.
	var target *Item
	pristine := false
	for _, it := range direct {
		if ud := it.UserData; ud != nil && ud.PlayCount == 0 && !ud.Played && ud.PlaybackPositionTicks == 0 {
			target, pristine = it, true
			break
		}
	}
	if target == nil && !tg.readOnly && len(direct) > 0 {
		target = direct[0] // public demo: report at the existing position
	}
	if target == nil {
		t.Logf("skip reporting: no natively playable item with pristine user data")
		return
	}
	pb := reported
	if pb == nil || pb.ItemID != target.ID {
		pb = must(t, "PlaybackInfo(report)", func(ctx context.Context) (*Playback, error) {
			return rc.PlaybackInfo(ctx, target.ID, PlaybackOptions{})
		})
	}
	r := pb.Report(target.ResumePosition(), false)
	for _, step := range []struct {
		name string
		f    func(context.Context, PlaybackReport) error
	}{{"start", rc.ReportPlaybackStart}, {"progress", rc.ReportPlaybackProgress}, {"stopped", rc.ReportPlaybackStopped}} {
		if step.name == "progress" {
			r.IsPaused = true
			r.EventName = "pause"
		}
		if _, err := retry(t, "report "+step.name, func(ctx context.Context) (struct{}, error) { return struct{}{}, step.f(ctx, r) }); err != nil {
			t.Errorf("report %s: %v", step.name, err)
		}
	}
	after := must(t, "Item(after report)", func(ctx context.Context) (*Item, error) { return rc.Item(ctx, target.ID) })
	ud := after.UserData
	t.Logf("reported start/progress/stopped for %q; user data after: PlayCount=%d Played=%v Position=%d", target.Name, ud.PlayCount, ud.Played, ud.PlaybackPositionTicks)
	if !pristine {
		if ud.PlaybackPositionTicks != target.UserData.PlaybackPositionTicks {
			t.Errorf("resume position changed from %d to %d", target.UserData.PlaybackPositionTicks, ud.PlaybackPositionTicks)
		}
	} else if ud.PlayCount != 0 || ud.Played || ud.PlaybackPositionTicks != 0 {
		restored := must(t, "SetPlayed(false) restore", func(ctx context.Context) (*UserData, error) { return rc.SetPlayed(ctx, target.ID, false) })
		if restored.PlayCount != 0 || restored.Played || restored.PlaybackPositionTicks != 0 {
			t.Errorf("could not restore user data of %q: %+v", target.Name, restored)
		} else {
			t.Logf("restored user data of %q (server counted a play on start)", target.Name)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rc.StopTranscoding(ctx, pb.PlaySessionID); err != nil {
		t.Errorf("StopTranscoding: %v", err)
	}
}

func namesOf(items []Item) []string {
	out := make([]string, len(items))
	for i := range items {
		out[i] = items[i].DisplayTitle()
	}
	return out
}

func typesOf(items []Item) []string {
	seen := map[string]bool{}
	var out []string
	for _, it := range items {
		if !seen[it.Type] {
			seen[it.Type] = true
			out = append(out, it.Type)
		}
	}
	return out
}

func refString(it *Item, role ImageRole) string {
	r, ok := it.Image(role)
	if !ok {
		return "none"
	}
	return string(r.Type) + "@" + r.ItemID
}
