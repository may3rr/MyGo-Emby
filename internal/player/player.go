package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Config is what the app gives the player.
type Config struct {
	Client *emby.Client
	// Window is the window the player shows in, for full screen; nil in
	// tests.
	Window *mygo.Window
	// Post runs fn on the main thread and draws a frame after it: how the
	// player's goroutines hand results to it (the app's update).
	Post func(fn func())
	// MaxBitrate caps a server's transcode, in bits per second; 0 = none.
	MaxBitrate int64
	// AudioLang and SubtitleLang are the languages preferred, as ISO 639
	// codes ("chi", "zh", "eng"…); "" keeps the server's default, and
	// SubtitleLang SubtitlesOff shows none.
	AudioLang, SubtitleLang string
	// SubtitleScale scales rendered subtitle text (<=0 uses 1.0).
	SubtitleScale float64
	// AutoNext plays the next episode when one ends, after a countdown.
	AutoNext bool
	// Volume, from 0 to 1, and Muted are how the sound starts.
	Volume float64
	Muted  bool
	// OnExit runs on the main thread when the user leaves the player, or
	// an item played to its end with nothing after it.
	OnExit func(Result)
	// PickSubtitleFile picks a local subtitle file path; nil uses
	// mygo.Dialog.Open when Window is non-nil.
	PickSubtitleFile func() (string, error)
	// ExternalPlayerLabel and OnOpenExternal offer a fallback button on the
	// error panel to open the item in an external player.
	ExternalPlayerLabel string
	OnOpenExternal      func(item *emby.Item, mediaSourceID string, pos time.Duration)

	// Image, when set, returns the picture at a URL, nil while it loads,
	// asking for a frame when it comes (the app's image cache). Without
	// it the player loads the pictures it shows while loading itself.
	Image func(url string) *ui.Bitmap
	// Source, when set, makes items into streams in place of the server's
	// own (&ServerSource{Client: Client}), as one that remuxes locally.
	Source Source
}

// Result is how playback ended, for the app: where to resume, and the
// sound settings to keep.
type Result struct {
	ItemID   string
	Position time.Duration
	// Finished: the item played to its end (or nearly), which the server
	// marks played.
	Finished      bool
	Volume        float64
	Muted         bool
	SubtitleScale float64
}

type phase uint8

const (
	phaseIdle phase = iota
	phaseLoading
	phasePlaying
	phaseError
)

// Timings of the interface.
const (
	controlsHideAfter = 3 * time.Second
	volumeStep        = 0.05
	skipStep          = 10 * time.Second
	upNextLead        = 15 * time.Second // the card shows this long before the end
	serverSeekGap     = 800 * time.Millisecond
)

var speeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 2}

// playing is one opened stream and its video.
type playing struct {
	stream *Stream
	eng    engine
	ready  bool
	// start is where the stream was asked to start, which the player
	// checks once it is ready (a seek before AVPlayer loaded is honored
	// by most streams, not promised by all).
	start time.Duration
	// resume: play once ready (the stream before was playing).
	resume bool
	// loaded tells AVPlayer knows the stream's duration, so its tracks
	// are loaded; until then the view does not build its Video (see
	// videoLoaded). opened is when it was opened.
	loaded bool
	opened time.Time
	// builds counts the frames that built the Video, for Status.
	builds int
	// settling: the first picture came, away from start, and a seek
	// there is on its way; the picture stays covered until it lands, or
	// until settleBy.
	settling bool
	settleBy time.Time
}

// near reports whether a and b are within the tolerance of a resume point.
func near(a, b time.Duration) bool {
	d := a - b
	return d < 3*time.Second && d > -3*time.Second
}

func (pl *playing) close() {
	if pl == nil {
		return
	}
	if pl.eng != nil {
		pl.eng.Close()
	}
	pl.stream.close()
}

// Player plays items in a window. Every method runs on the main thread.
type Player struct {
	// want is what PlayWith asked, while begin starts the item.
	want Options
	cfg  Config
	src  Source
	open opener
	post func(func())
	http *http.Client
	// What the player asks of the server, which tests replace.
	reportTo        reportClient
	nextEpisode     func(ctx context.Context, it *emby.Item) (*emby.Item, error)
	fetchSubs       func(ctx context.Context, url string) (*emby.Subtitles, error)
	fetchSegments   func(ctx context.Context, itemID string) ([]emby.MediaSegment, error)
	fetchTrickSheet func(ctx context.Context, itemID, mediaSourceID string, width, sheetIndex int) ([]byte, error)

	theme *ui.Theme
	focus ui.Handle

	active bool
	// gen tells sessions (items) apart, sgen streams within one: answers
	// of an older one are dropped.
	gen, sgen  uint64
	cancel     context.CancelFunc
	ctx        context.Context
	reports    sync.WaitGroup
	rep        *reporter
	transcodes *transcodeSessions
	started    bool
	lastSent   time.Time
	finished   bool

	item              *emby.Item
	mediaSourceID     string // final version, also retained after engine failure
	title, series     string
	posterURL         string
	poster            *ui.Bitmap
	runtime           time.Duration
	startAt           time.Duration
	phase             phase
	errText           string
	errRetry          bool
	cur, prev         *playing
	switching         bool
	pendingSwitch     func()
	lastPos           time.Duration
	userPaused        bool
	audioTracks       []emby.Track
	subTracks         []emby.Track
	audioLabels       []string
	subLabels         []string
	localSubs          map[int]localSubtitle
	userSubs           map[int]*emby.Subtitles
	chapters           []emby.Chapter
	segments           []emby.MediaSegment
	segmentsFromServer bool
	dismissedSeg       int
	trickSheets        map[trickSheetKey]*ui.Bitmap
	trickOrder         []trickSheetKey
	trickPending       map[trickSheetKey]bool
	trickFailed        map[trickSheetKey]bool
	chapThumbs         map[string]*ui.Bitmap
	chapPending        map[string]bool
	seekThumbShown     bool
	audioIdx, subIdx   int
	canBurnIn         bool
	subs              subtitleTrack
	subGen            uint64
	subOffset         time.Duration
	subScale          float64
	nextItem          *emby.Item
	nextTitle         string
	upNext            upNextState
	upNextFrozen      bool // a sample card of Debug, left as it is
	volume            float64
	muted             bool
	rate              float64
	seek              seeker
	posText, remText  clockText
	seekText          clockText
	seekPreviewText   string
	upNextText        string
	upNextSecs        int
	fullscreen        bool
	enteredFullscreen bool

	// The view's state.
	activity                                   time.Time
	controlsShown                              bool
	barHovered                                 bool
	scrubbing                                  bool
	scrub, volValue                            float64
	speedOpen, audioOpen, subOpen, chapterOpen bool
	statsOpen                                  bool
	diag                                       DiagnosticsInfo

	// OS media integration handles (when Window != nil).
	awakeRelease func()
	mediaCmdOff  func()

	// Functions made once, so that building the view allocates none.
	fnInput        func(ui.InputEvent) bool
	fnSpeedMenu    func(*ui.Menu)
	fnAudioMenu    func(*ui.Menu)
	fnSubMenu      func(*ui.Menu)
	fnChapterMenu  func(*ui.Menu)
	fnDrawProgress func(*ui.Painter, ui.Rect)
	fnDrawVolume   func(*ui.Painter, ui.Rect)
	drawIcons      [iconCount]func(*ui.Painter, ui.Rect)
	barFrac        float32

	// Where the stream stands, read once a frame at most: each read of
	// AVPlayer's position or duration is a call into Objective-C that
	// allocates (purego). With nothing on screen following the time, the
	// position is read four times a second; the duration every few
	// seconds once known.
	// now is the time of the frame being built, or of the last one: the
	// clock every action goes by (Context.Now), a Tester's in tests.
	now          time.Time
	framePos     time.Duration
	posAt, durAt time.Time
	dur          time.Duration
}

type upNextState struct {
	shown, cancelled bool
	stream           *Stream
	err              error
	fetching         bool
}

// New returns a player; it shows nothing until Play.
func New(cfg Config) *Player {
	p := &Player{cfg: cfg, open: openVideo, rate: 1}
	p.src = cfg.Source
	if p.src == nil {
		p.src = &ServerSource{Client: cfg.Client}
	}
	p.post = cfg.Post
	if p.post == nil {
		p.post = func(func()) {}
	}
	p.volume = min(max(cfg.Volume, 0), 1)
	if cfg.Volume == 0 && !cfg.Muted {
		p.volume = 1
	}
	p.muted = cfg.Muted
	p.http = &http.Client{Timeout: 20 * time.Second}
	if c := cfg.Client; c != nil {
		p.reportTo, p.nextEpisode, p.fetchSubs = c, c.NextEpisode, c.FetchSubtitles
		p.fetchSegments, p.fetchTrickSheet = c.MediaSegments, c.FetchTrickplaySheet
	}
	p.theme = playerTheme()
	p.subScale = clampSubScale(cfg.SubtitleScale)
	p.fnInput = p.onInput
	p.fnSpeedMenu = p.speedMenu
	p.fnAudioMenu = p.audioMenu
	p.fnSubMenu = p.subtitleMenu
	p.fnChapterMenu = p.chapterMenu
	p.fnDrawProgress = p.drawProgress
	p.fnDrawVolume = p.drawVolume
	p.makeDrawers()
	if w := cfg.Window; w != nil {
		p.fullscreen = w.IsFullScreen()
		w.OnEnterFullScreen(func() { p.fullscreen = true; w.Invalidate() })
		w.OnLeaveFullScreen(func() { p.fullscreen = false; p.enteredFullscreen = false; w.Invalidate() })
		p.ensureMediaCommands()
	}
	return p
}

// Active reports whether the player shows: between Play and its end.
func (p *Player) Active() bool { return p.active }

// Play plays it, from its resume point unless fromStart, in place of what
// plays. It returns at once: the stream is asked for on a goroutine while
// the player shows the item's picture.
func (p *Player) Play(it *emby.Item, fromStart bool) {
	p.PlayWith(it, Options{FromStart: fromStart})
}

// Options say how to play an item, as its page chose. Those left nil or
// "" are the player's to choose, by the languages of Config.
type Options struct {
	FromStart  bool
	StartAt    time.Duration
	HasStartAt bool
	// MediaSourceID chooses a version; "" lets the server choose.
	MediaSourceID string
	// AudioIndex and SubtitleIndex are Emby stream indexes; a
	// SubtitleIndex of -1 shows none.
	AudioIndex, SubtitleIndex *int
}

// PlayWith plays it as o says. Main thread. The choices hold for this
// item only: the next episode picks its tracks by language again.
func (p *Player) PlayWith(it *emby.Item, o Options) {
	start := time.Duration(0)
	switch {
	case o.HasStartAt:
		start = max(o.StartAt, 0)
	case !o.FromStart && it != nil:
		start = it.ResumePosition()
	}
	p.want = o
	p.begin(it, start, nil)
	p.want = Options{}
}

// begin starts a session for it at start, with a stream already opened
// (prefetched) or nil to ask for one.
func (p *Player) begin(it *emby.Item, start time.Duration, st *Stream) {
	p.endSession(false)
	p.active = true
	p.ensureMediaCommands()
	p.gen++
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.item = it
	p.mediaSourceID = p.want.MediaSourceID
	p.startAt = start
	p.lastPos = start
	p.runtime = 0
	p.chapters = nil
	p.resetSegmentsAndTrickplay()
	if it != nil {
		p.runtime = it.Runtime()
		if len(it.Chapters) > 0 {
			p.chapters = slices.Clone(it.Chapters)
			sort.SliceStable(p.chapters, func(i, j int) bool {
				return p.chapters[i].StartPositionTicks < p.chapters[j].StartPositionTicks
			})
			p.segments = emby.SegmentsFromChapters(p.chapters, p.runtime)
		}
	}
	p.title, p.series = titles(it)
	p.phase, p.errText, p.errRetry = phaseLoading, "", false
	p.started, p.finished, p.lastSent = false, false, time.Time{}
	p.userPaused = false
	p.audioTracks, p.subTracks, p.audioLabels, p.subLabels = nil, nil, nil, nil
	p.localSubs, p.userSubs = nil, nil
	p.audioIdx, p.subIdx, p.canBurnIn = -1, -1, false
	p.subOffset = 0
	p.subs.reset()
	p.nextItem, p.upNext, p.upNextFrozen = nil, upNextState{}, false
	p.seek.reset()
	p.scrubbing = false
	p.speedOpen, p.audioOpen, p.subOpen, p.chapterOpen = false, false, false, false
	p.diag = DiagnosticsInfo{}
	p.activity = p.now // the controls show as it starts
	p.rep = newReporter(p.reportTo, &p.reports)
	p.loadPoster(it)
	if it == nil {
		p.fail(&UnplayableError{Reason: str.ErrNoStream}, false)
		return
	}
	if st != nil {
		p.resolved(st, nil, p.pickSubFor(st), p.sgen)
	} else {
		p.resolve(Request{Item: it, Start: start, MaxBitrate: p.cfg.MaxBitrate,
			MediaSourceID: p.want.MediaSourceID, AudioIndex: p.want.AudioIndex, SubtitleIndex: p.want.SubtitleIndex})
	}
	if it.Type == emby.TypeEpisode && p.nextEpisode != nil {
		p.fetchNext(it)
	}
	p.loadSegments(it.ID)
}

// resolve asks the source for a stream on a goroutine, and applies the
// language preferences: a second request for another audio track, or a
// bitmap subtitle to burn in, before AVPlayer opens anything.
func (p *Player) resolve(req Request) {
	p.sgen++
	gen, sgen, ctx, open := p.gen, p.sgen, p.ctx, p.streamOpener()
	audioPref, subPref := p.cfg.AudioLang, p.cfg.SubtitleLang
	wantAudio, wantSub := req.AudioIndex, p.want.SubtitleIndex
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		st, err := open(ctx, req)
		sub := -1
		if err == nil && st.Playback != nil {
			pb := st.Playback
			subTracks, fromServer := subtitleChoices(st, pb)
			audio := pickAudio(pb.AudioTracks, audioPref, st.AudioIndex)
			if wantAudio != nil {
				audio = *wantAudio
			}
			sub = pickSubtitle(subTracks, subPref, defaultSubtitle(pb), fromServer && pb.Source.SupportsTranscoding)
			if wantSub != nil {
				sub = *wantSub
			}
			burn := -1
			if t := trackByIndex(subTracks, sub); t != nil && !t.IsText && fromServer {
				burn = sub
			}
			if audio != st.AudioIndex || burn != st.BurnedIn {
				again := req
				again.MediaSourceID = pb.MediaSourceID
				if audio != st.AudioIndex {
					again.AudioIndex = emby.Index(audio)
				}
				if burn >= 0 {
					again.BurnIn = emby.Index(burn)
				}
				if st2, err2 := open(ctx, again); err2 == nil {
					p.discard(st)
					st = st2
				} else if burn >= 0 {
					sub = -1 // plays, without the bitmap subtitle
				}
			}
		}
		if ctx.Err() != nil {
			p.discard(st)
			return
		}
		p.post(func() {
			if gen != p.gen || sgen != p.sgen || !p.active {
				p.discard(st)
				return
			}
			p.resolved(st, err, sub, sgen)
		})
	}()
}

// discard releases a stream nobody plays, from any goroutine.
func (p *Player) discard(st *Stream) {
	if st == nil {
		return
	}
	st.close()
}

func defaultSubtitle(pb *emby.Playback) int {
	if d := pb.Source.DefaultSubtitleStreamIndex; d != nil {
		return *d
	}
	return -1
}

func (p *Player) pickSubFor(st *Stream) int {
	if st == nil || st.Playback == nil {
		return -1
	}
	if st.BurnedIn >= 0 {
		return st.BurnedIn
	}
	pb := st.Playback
	subTracks, fromServer := subtitleChoices(st, pb)
	sub := pickSubtitle(subTracks, p.cfg.SubtitleLang, defaultSubtitle(pb), fromServer && pb.Source.SupportsTranscoding)
	if t := trackByIndex(subTracks, sub); t != nil && !t.IsText {
		return -1 // a bitmap one needs another stream: the user picks it
	}
	return sub
}

// resolved opens the stream the source gave, on the main thread.
func (p *Player) resolved(st *Stream, err error, sub int, sgen uint64) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		p.fail(err, !errors.Is(err, ErrUnplayable))
		return
	}
	if st.Playback != nil {
		p.mediaSourceID = st.Playback.MediaSourceID
	}
	eng, err := p.open(st.URL)
	if err != nil {
		p.discard(st)
		p.fail(fmt.Errorf("%s%w", str.ErrPlayer, err), true)
		return
	}
	pb := st.Playback
	if pb != nil && p.audioTracks == nil {
		p.audioTracks = pb.AudioTracks
		var fromServer bool
		p.subTracks, fromServer = subtitleChoices(st, pb)
		if !fromServer {
			p.localSubs = make(map[int]localSubtitle, len(st.localSubtitles))
			for _, local := range st.localSubtitles {
				p.localSubs[local.ID] = local
			}
		}
		p.audioLabels = labels(p.audioTracks)
		p.subLabels = labels(p.subTracks)
		p.canBurnIn = fromServer && pb.Source.SupportsTranscoding
		if p.runtime == 0 {
			p.runtime = pb.Runtime()
		}
		if !p.segmentsFromServer && len(p.segments) == 0 && len(p.chapters) > 0 && p.runtime > 0 {
			p.segments = emby.SegmentsFromChapters(p.chapters, p.runtime)
		}
	}
	p.audioIdx = st.AudioIndex
	// Silent until it stands where it starts (firstPicture sets it).
	eng.SetVolume(0)
	eng.SetRate(p.rate)
	p.seek.reset()
	p.seek.minGap = 0
	if st.ServerWork {
		p.seek.minGap = serverSeekGap
	}
	p.cur = &playing{stream: st, eng: eng, start: p.startAt, resume: true, opened: p.now}
	p.dur, p.durAt, p.posAt = 0, time.Time{}, time.Time{}
	p.phase = phasePlaying
	p.showSubtitle(sub)
	p.syncKeepAwake()
	p.refreshDiagnostics()
}

// fail shows the error panel.
func (p *Player) fail(err error, retry bool) {
	p.phase = phaseError
	p.errText = errorText(err)
	p.errRetry = retry
	log.Printf("player: %v", err)
	p.lastPos = p.position()
	p.finishPlayback(false)
	p.clearOSMedia(false)
}

// errorText says what went wrong, in the user's words.
func errorText(err error) string {
	var u *UnplayableError
	switch {
	case errors.As(err, &u):
		return u.Reason
	case errors.Is(err, emby.ErrUnreachable):
		return str.ErrNetwork
	case errors.Is(err, emby.ErrUnauthorized), errors.Is(err, emby.ErrNotLoggedIn):
		return str.ErrUnauthorized
	case errors.Is(err, emby.ErrNotFound):
		return str.ErrNotFound
	case errors.Is(err, emby.ErrNotPlayable):
		return str.ErrNoStream
	}
	var e *emby.Error
	if errors.As(err, &e) {
		return str.ErrServer + e.Error()
	}
	return err.Error()
}

// retry plays the item again from where it stood.
func (p *Player) retry() {
	if p.item == nil {
		return
	}
	pos := p.lastPos
	p.want = Options{MediaSourceID: p.mediaSourceID}
	p.begin(p.item, pos, nil)
	p.want = Options{}
}

// endSession reports the end of what plays and releases it. finished
// tells the item played to its end.
func (p *Player) endSession(finished bool) {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if p.item == nil {
		return
	}
	p.finishPlayback(finished)
	p.discard(p.upNext.stream)
	p.upNext = upNextState{}
	p.subGen++
	p.subs.reset()
	p.clearOSMedia(false)
	p.item = nil
	p.chapters = nil
	p.userSubs = nil
	p.phase = phaseIdle
	p.poster = nil
}

// finishPlayback also runs on engine failure, while item and version stay
// available for Retry. Both sides of an unfinished switch must be released.
func (p *Player) finishPlayback(finished bool) {
	r := p.report(p.position())
	if finished && p.runtime > 0 {
		r.PositionTicks = emby.DurationToTicks(max(p.position(), p.runtime))
	}
	var releases []func()
	for _, pl := range []*playing{p.cur, p.prev} {
		if pl != nil {
			if release := pl.stream.takeServerRelease(); release != nil {
				releases = append(releases, release)
			}
			pl.close()
		}
	}
	release := func() {
		for _, f := range releases {
			f()
		}
	}
	if p.started {
		p.rep.finishWithRelease(r, release)
	} else {
		release()
		p.rep.end()
	}
	p.rep = nil
	p.started = false
	p.cur, p.prev = nil, nil
	p.switching = false
	p.pendingSwitch = nil
	p.resetSegmentsAndTrickplay()
}

// Close reports the end of playback and releases everything, without
// OnExit; the player shows nothing after it. Reports go on a goroutine:
// Flush waits for them, as the app does before it quits.
func (p *Player) Close() {
	if p.enteredFullscreen && p.cfg.Window != nil {
		p.cfg.Window.SetFullScreen(false)
		p.enteredFullscreen = false
	}
	p.endSession(p.finished)
	p.clearOSMedia(true)
	p.active = false
}

// Flush waits up to timeout for the reports sent so far, the stopped
// report of Close among them, reporting whether they all went.
func (p *Player) Flush(timeout time.Duration) bool {
	return waitGroup(&p.reports, timeout)
}

// Result returns how things stand: the item, where it stands and the
// sound settings.
func (p *Player) Result() Result {
	r := Result{Volume: p.volume, Muted: p.muted, Finished: p.finished, Position: p.position(), SubtitleScale: p.subScale}
	if p.item != nil {
		r.ItemID = p.item.ID
	}
	return r
}

// exit leaves the player, as the user asked or because nothing comes next.
func (p *Player) exit(finished bool) {
	p.finished = p.finished || finished
	res := p.Result()
	p.Close()
	if p.cfg.OnExit != nil {
		p.cfg.OnExit(res)
	}
}

// position is where playback stands: the video's position once it showed
// a picture, else where it was asked to start (so that leaving before the
// first picture never resets the resume point to 0).
func (p *Player) position() time.Duration {
	if p.cur != nil && p.cur.ready {
		return p.cur.eng.Position()
	}
	if p.prev != nil && p.prev.ready {
		return p.prev.eng.Position()
	}
	return p.lastPos
}

// report makes a report at pos with the sound and stream as they are.
func (p *Player) report(pos time.Duration) emby.PlaybackReport {
	var r emby.PlaybackReport
	pl := p.cur
	if pl == nil || !pl.ready && p.prev != nil {
		pl = p.prev
	}
	if pl != nil && pl.stream != nil && pl.stream.Playback != nil {
		r = pl.stream.Playback.Report(pos, false)
		r.PlayMethod = pl.stream.Method
		if pl.stream.AudioIndex >= 0 {
			r.AudioStreamIndex = emby.Index(pl.stream.AudioIndex)
		}
	} else if p.item != nil {
		r = emby.PlaybackReport{ItemID: p.item.ID, PositionTicks: emby.DurationToTicks(pos), CanSeek: true}
	}
	r.IsPaused = p.userPaused
	r.VolumeLevel = int(math.Round(p.volume * 100))
	r.IsMuted = p.muted
	switch {
	case p.subIdx == -1:
		r.SubtitleStreamIndex = emby.Index(-1)
	default:
		if _, local := p.localSubtitle(p.subIdx); !local && p.subIdx >= 0 {
			r.SubtitleStreamIndex = emby.Index(p.subIdx)
		}
	}
	if p.rate != 1 {
		r.PlaybackRate = p.rate
	}
	return r
}

func (p *Player) sendProgress(now time.Time, event string) {
	if !p.started || p.rep == nil {
		return
	}
	r := p.report(p.position())
	r.EventName = event
	p.rep.send(reportJob{kind: reportProgress, report: r})
	p.lastSent = now
}

// Actions, from the controls and the keys.

func (p *Player) effectiveVolume() float64 {
	if p.muted {
		return 0
	}
	return p.volume
}

func (p *Player) togglePlay(now time.Time) {
	pl := p.cur
	if pl == nil || p.phase != phasePlaying {
		return
	}
	if pl.eng.Ended() {
		pl.eng.Seek(0)
		pl.eng.Play()
		p.userPaused = false
		p.syncKeepAwake()
		p.syncNowPlayingPlayback()
		return
	}
	if p.userPaused {
		pl.eng.Play()
		p.userPaused = false
		p.sendProgress(now, "Unpause")
	} else {
		pl.eng.Pause()
		p.userPaused = true
		p.sendProgress(now, "Pause")
	}
	p.syncKeepAwake()
	p.syncNowPlayingPlayback()
}

func (p *Player) skip(now time.Time, d time.Duration) {
	if p.cur == nil || !p.cur.ready {
		return
	}
	p.seek.step(now, p.cur.eng.Position(), d)
}

func (p *Player) setVolume(now time.Time, v float64) {
	p.volume = min(max(v, 0), 1)
	p.muted = false
	p.applyVolume()
	p.sendProgress(now, "VolumeChange")
}

func (p *Player) toggleMute(now time.Time) {
	p.muted = !p.muted
	p.applyVolume()
	p.sendProgress(now, "VolumeChange")
}

func (p *Player) applyVolume() {
	// A stream not yet showing stays silent (it may still play from the
	// start before seeking to the resume point, or replace another): it
	// takes the volume as it shows (firstPicture).
	if p.cur != nil && p.cur.ready {
		p.cur.eng.SetVolume(p.effectiveVolume())
	}
}

func (p *Player) setRate(now time.Time, r float64) {
	p.rate = r
	if p.cur != nil {
		p.cur.eng.SetRate(r)
	}
	p.sendProgress(now, "PlaybackRateChange")
	p.syncNowPlaying(now)
}

func (p *Player) toggleFullscreen() {
	if w := p.cfg.Window; w != nil {
		on := !w.IsFullScreen()
		p.enteredFullscreen = on
		w.SetFullScreen(on)
		return
	}
	p.fullscreen = !p.fullscreen
}

func (p *Player) escape() {
	if p.fullscreen {
		p.toggleFullscreen()
		return
	}
	p.exit(false)
}

// selectAudio plays another audio track: AVPlayer plays one track of a
// file, so another stream is opened (a server transcode or a local remux
// with that track), starting where playback stands. The picture stays as
// it was until the new stream shows its first.
func (p *Player) selectAudio(index int) {
	if index == p.audioIdx || p.cur == nil || p.switching {
		return
	}
	burn := emby.Index(p.cur.stream.BurnedIn)
	p.switchStream(Request{AudioIndex: emby.Index(index), BurnIn: burn}, func() { p.audioIdx = index })
}

// selectSubtitle shows another subtitle (-1 for none): a text one is drawn
// over the picture at once; a bitmap one needs a stream with it burned in.
func (p *Player) selectSubtitle(index int) {
	if index == p.subIdx || p.cur == nil || p.switching {
		return
	}
	if local, ok := p.localSubtitle(index); ok {
		if local.Reason != "" {
			return
		}
		p.showSubtitle(index)
		p.sendProgress(p.now, "SubtitleTrackChange")
		return
	}
	if _, ok := p.userSubs[index]; ok {
		if p.cur.stream.BurnedIn >= 0 {
			p.switchStream(Request{AudioIndex: emby.Index(p.audioIdx)}, func() { p.showSubtitle(index) })
		} else {
			p.showSubtitle(index)
		}
		p.sendProgress(p.now, "SubtitleTrackChange")
		return
	}
	t := trackByIndex(p.subTracks, index)
	burned := p.cur.stream.BurnedIn
	switch {
	case t != nil && !t.IsText:
		if !p.canBurnIn {
			return
		}
		p.switchStream(Request{AudioIndex: emby.Index(p.audioIdx), BurnIn: emby.Index(index)}, func() {
			p.subs.reset()
			p.subGen++
			p.subIdx = index
		})
	case burned >= 0:
		// From a burned-in one: a stream without it.
		p.switchStream(Request{AudioIndex: emby.Index(p.audioIdx)}, func() { p.showSubtitle(index) })
	default:
		p.showSubtitle(index)
	}
	p.sendProgress(p.now, "SubtitleTrackChange")
}

// switchStream opens another stream of the item at the position playing,
// keeping the picture until it shows; done runs once it does.
func (p *Player) switchStream(req Request, done func()) {
	old := p.cur
	pos := old.eng.Position()
	if !old.ready {
		pos = old.start
	}
	req.Item, req.Start, req.MaxBitrate = p.item, pos, p.cfg.MaxBitrate
	req.MediaSourceID = p.mediaSourceID
	wasPlaying := !p.userPaused
	old.eng.Pause()
	p.switching = true
	p.seek.reset()
	p.sgen++
	gen, sgen, ctx, open := p.gen, p.sgen, p.ctx, p.streamOpener()
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		st, err := open(ctx, req)
		if ctx.Err() != nil {
			p.discard(st)
			return
		}
		p.post(func() {
			if gen != p.gen || sgen != p.sgen || !p.active || p.cur != old {
				p.discard(st)
				return
			}
			p.switching = false
			if err != nil {
				log.Printf("player: switching stream: %v", err)
				if wasPlaying {
					old.eng.Play()
				}
				return
			}
			eng, err := p.open(st.URL)
			if err != nil {
				p.discard(st)
				log.Printf("player: switching stream: %v", err)
				if wasPlaying {
					old.eng.Play()
				}
				return
			}
			eng.SetVolume(0) // silent until it replaces the old one
			eng.SetRate(p.rate)
			p.prev = old
			p.cur = &playing{stream: st, eng: eng, start: pos, resume: wasPlaying, opened: p.now}
			p.dur, p.durAt, p.posAt = 0, time.Time{}, time.Time{}
			p.seek.minGap = 0
			if st.ServerWork {
				p.seek.minGap = serverSeekGap
			}
			p.switching = true
			p.pendingSwitch = done
		})
	}()
}

// showSubtitle shows text subtitle index (-1 none), fetching it.
func (p *Player) showSubtitle(index int) {
	p.subGen++
	p.subs.reset()
	p.subIdx = index
	if us, ok := p.userSubs[index]; ok {
		p.subs.load(us)
		p.refreshDiagnostics()
		return
	}
	t := trackByIndex(p.subTracks, index)
	if t == nil || !t.IsText || p.cur == nil {
		if t != nil && !t.IsText && p.cur != nil && p.cur.stream.BurnedIn == index {
			p.refreshDiagnostics()
			return // burned in: nothing to draw
		}
		if t == nil {
			p.subIdx = -1
		}
		p.refreshDiagnostics()
		return
	}
	if local, ok := p.localSubtitle(index); ok && local.Reason != "" {
		p.subIdx = -1
		p.refreshDiagnostics()
		return
	}
	if emb := p.cur.stream.Embedded; emb != nil {
		if feed := emb(index); feed != nil {
			p.subs.follow(feed)
			p.refreshDiagnostics()
			return
		}
	}
	if t.URL == "" || p.fetchSubs == nil {
		p.refreshDiagnostics()
		return
	}
	p.refreshDiagnostics()
	gen, sgen, url, ctx, fetch := p.gen, p.subGen, t.URL, p.ctx, p.fetchSubs
	go func() {
		sub, err := fetch(ctx, url)
		if err != nil && !errors.Is(err, emby.ErrNoCues) {
			log.Printf("player: %s: %v", str.SubtitleFailed, err)
			return
		}
		p.post(func() {
			if gen != p.gen || sgen != p.subGen {
				return
			}
			p.subs.load(sub)
		})
	}()
}

func (p *Player) localSubtitle(index int) (localSubtitle, bool) {
	local, ok := p.localSubs[index]
	return local, ok
}

// SetSubtitleScale updates the subtitle font scale immediately while playing.
func (p *Player) SetSubtitleScale(scale float64) {
	p.setSubScale(scale)
}

func clampSubScale(s float64) float64 {
	if s <= 0 {
		return 1
	}
	return min(max(s, 0.6), 2.0)
}

func (p *Player) effectiveSubScale() float64 {
	if p.subScale <= 0 {
		return 1
	}
	return p.subScale
}

func (p *Player) setSubScale(s float64) {
	p.subScale = clampSubScale(s)
}

func (p *Player) adjustSubOffset(delta time.Duration) {
	p.setSubOffset(p.subOffset + delta)
}

func (p *Player) setSubOffset(off time.Duration) {
	p.subOffset = off
	p.refreshDiagnostics()
}

func (p *Player) canPickSubtitle() bool {
	return p.cfg.PickSubtitleFile != nil || p.cfg.Window != nil
}

func (p *Player) loadLocalSubtitleFile() {
	if !p.active || p.cur == nil || !p.canPickSubtitle() {
		return
	}
	gen := p.gen
	pick := p.cfg.PickSubtitleFile
	if pick == nil {
		win := p.cfg.Window
		pick = func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
				Parent: win,
				Title:  str.LoadLocalSubtitle,
				Filters: []mygo.FileFilter{{
					Name:       str.Subtitles,
					Extensions: []string{"srt", "vtt", "ass", "ssa", "sub"},
				}},
			})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		}
	}
	go func() {
		path, err := pick()
		if err != nil || path == "" {
			if err != nil {
				log.Printf("player: pick subtitle: %v", err)
			}
			return
		}
		f, err := os.Open(path)
		if err != nil {
			log.Printf("player: open subtitle %s: %v", filepath.Base(path), err)
			return
		}
		defer f.Close()
		const maxLocalSubBytes = 16 << 20
		data, err := io.ReadAll(io.LimitReader(f, maxLocalSubBytes+1))
		if err != nil {
			log.Printf("player: read subtitle %s: %v", filepath.Base(path), err)
			return
		}
		if len(data) > maxLocalSubBytes {
			log.Printf("player: local subtitle %s exceeds %d bytes", filepath.Base(path), maxLocalSubBytes)
			return
		}
		parsed, err := emby.ParseSubtitles(data)
		if err != nil && !errors.Is(err, emby.ErrNoCues) {
			log.Printf("player: parse subtitle %s: %v", filepath.Base(path), err)
			return
		}
		name := filepath.Base(path)
		p.post(func() {
			if gen != p.gen || !p.active {
				return
			}
			p.addUserSubtitle(name, parsed)
		})
	}()
}

func (p *Player) addUserSubtitle(name string, parsed *emby.Subtitles) {
	if parsed == nil || p.cur == nil {
		return
	}
	if p.userSubs == nil {
		p.userSubs = map[int]*emby.Subtitles{}
	}
	id := -1000 - len(p.userSubs)
	p.userSubs[id] = parsed
	title := name + " (" + str.LocalSubtitleTag + ")"
	tr := emby.Track{
		Index:        id,
		Type:         emby.StreamSubtitle,
		IsText:       true,
		IsExternal:   true,
		DisplayTitle: title,
	}
	p.subTracks = append(p.subTracks, tr)
	p.subLabels = append(p.subLabels, tr.Label())
	p.selectSubtitle(id)
}

func (p *Player) chapterAt(pos time.Duration) int {
	cur := -1
	for i := range p.chapters {
		if p.chapters[i].Start() <= pos {
			cur = i
		} else {
			break
		}
	}
	return cur
}

func (p *Player) seekTo(now time.Time, target time.Duration) {
	pl := p.cur
	if pl == nil || !pl.ready {
		return
	}
	dur := p.duration(now, pl.eng)
	p.seek.seekTo(now, clampSeek(target, dur))
}

func (p *Player) effectiveDur() time.Duration {
	if p.dur > 0 {
		return p.dur
	}
	return p.runtime
}

// fetchNext finds the episode after it, for the next button and the
// countdown at the end.
func (p *Player) fetchNext(it *emby.Item) {
	gen, ctx, find := p.gen, p.ctx, p.nextEpisode
	go func() {
		next, err := find(ctx, it)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("player: next episode: %v", err)
			}
			return
		}
		p.post(func() {
			if gen == p.gen {
				p.nextItem = next
				p.nextTitle, _ = titles(next)
				p.syncNowPlaying(p.now)
			}
		})
	}()
}

// prefetchNext asks for the next episode's stream while the countdown
// runs, so that it starts the moment the countdown ends.
func (p *Player) prefetchNext() {
	if p.nextItem == nil || p.upNext.fetching || p.upNext.stream != nil {
		return
	}
	p.upNext.fetching = true
	gen, ctx, open := p.gen, p.ctx, p.streamOpener()
	req := Request{Item: p.nextItem, Start: p.nextItem.ResumePosition(), MaxBitrate: p.cfg.MaxBitrate}
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		st, err := open(ctx, req)
		if ctx.Err() != nil {
			p.discard(st)
			return
		}
		p.post(func() {
			if gen != p.gen || !p.upNext.shown || p.upNext.cancelled {
				p.discard(st)
				return
			}
			p.upNext.fetching = false
			p.upNext.stream, p.upNext.err = st, err
		})
	}()
}

// playNext plays the next episode, with the stream prefetched if any.
func (p *Player) playNext() {
	next := p.nextItem
	if next == nil {
		return
	}
	st := p.upNext.stream
	p.upNext.stream = nil
	finished := false
	if pl := p.cur; pl != nil && pl.ready {
		if d := p.dur; d > 0 && d-pl.eng.Position() < upNextLead+time.Second {
			finished = true
		}
	}
	p.endSession(finished)
	p.begin(next, next.ResumePosition(), st)
}

func (p *Player) cancelUpNext() {
	p.upNext.cancelled = true
	p.discard(p.upNext.stream)
	p.upNext.stream = nil
}

// titles returns the title line ("S1E3 · 集名" for an episode) and the
// line under it (the series).
func titles(it *emby.Item) (title, sub string) {
	if it == nil {
		return "", ""
	}
	if it.Type != emby.TypeEpisode {
		return it.Name, ""
	}
	label := ""
	if e, ok := it.Episode(); ok {
		if s, ok := it.Season(); ok {
			label = fmt.Sprintf(str.SeasonEpisodeFmt, s, e)
		} else {
			label = fmt.Sprintf(str.EpisodeFmt, e)
		}
	}
	switch {
	case label == "":
		title = it.Name
	case it.Name == "":
		title = label
	default:
		title = label + " · " + it.Name
	}
	return title, it.SeriesName
}

func labels(tracks []emby.Track) []string {
	out := make([]string, len(tracks))
	for i := range tracks {
		out[i] = tracks[i].Label()
	}
	return out
}

// loadPoster shows the item's backdrop (else its still or poster) while
// the stream loads.
func (p *Player) loadPoster(it *emby.Item) {
	p.poster, p.posterURL = nil, ""
	if it == nil || p.cfg.Client == nil {
		return
	}
	opts := emby.ImageOptions{MaxWidth: 1920, Quality: 85}
	for _, role := range []emby.ImageRole{emby.RoleBackdrop, emby.RoleLandscape, emby.RolePrimary} {
		if u := p.cfg.Client.ItemImageURL(it, role, opts); u != "" {
			p.posterURL = u
			break
		}
	}
	if p.posterURL == "" || p.cfg.Image != nil {
		return
	}
	gen, url, ctx, hc := p.gen, p.posterURL, p.ctx, p.http
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := hc.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return
		}
		bmp, err := ui.DecodeBitmap(data)
		if err != nil {
			return
		}
		p.post(func() {
			if gen == p.gen {
				p.poster = bmp
			}
		})
	}()
}
