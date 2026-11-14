package player

import (
	"context"
	"log"
	"sync"
	"time"

	"mygo-emby/internal/emby"
)

// Reports go to the server from a goroutine of their own, one per played
// item, in the order the player made them: start, progress, stopped, then
// the end of the server's transcode. The main thread only queues them and
// never waits; each request has its own timeout, so a slow server delays
// the reports, not the picture. Progress reports that pile up behind a slow
// one are merged: only the latest goes.

const (
	reportTimeout    = 10 * time.Second
	progressInterval = 10 * time.Second
)

type reportKind uint8

const (
	reportStart reportKind = iota
	reportProgress
	reportStopped
	reportStopTranscode
)

type reportJob struct {
	kind    reportKind
	report  emby.PlaybackReport
	session string // play session of a transcode to stop
	release func() // release shared server work after the stopped report
}

// reportClient is what the reporter needs of *emby.Client.
type reportClient interface {
	ReportPlaybackStart(ctx context.Context, r emby.PlaybackReport) error
	ReportPlaybackProgress(ctx context.Context, r emby.PlaybackReport) error
	ReportPlaybackStopped(ctx context.Context, r emby.PlaybackReport) error
	StopTranscoding(ctx context.Context, playSessionID string) error
}

type reporter struct {
	client reportClient
	mu     sync.Mutex
	queue  []reportJob
	wake   chan struct{}
	done   bool // a stopped report was queued: the goroutine ends after it
}

// newReporter starts a reporter, counted in wg until its last report went.
func newReporter(client reportClient, wg *sync.WaitGroup) *reporter {
	r := &reporter{client: client, wake: make(chan struct{}, 1)}
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.run()
	}()
	return r
}

// send queues a report. After a stopped report, only transcode stops are
// taken.
func (r *reporter) send(j reportJob) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.done && j.kind != reportStopTranscode {
		r.mu.Unlock()
		return
	}
	if j.kind == reportProgress && len(r.queue) > 0 && r.queue[len(r.queue)-1].kind == reportProgress {
		r.queue[len(r.queue)-1] = j
	} else {
		r.queue = append(r.queue, j)
	}
	if j.kind == reportStopped {
		r.done = true
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// finish queues the stopped report and, for a transcode, its stop; the
// goroutine ends once they went.
func (r *reporter) finish(stopped emby.PlaybackReport, transcodeSession string) {
	r.finishJobs(stopped, reportJob{kind: reportStopTranscode, session: transcodeSession})
}

func (r *reporter) finishWithRelease(stopped emby.PlaybackReport, release func()) {
	if r == nil {
		if release != nil {
			release()
		}
		return
	}
	r.finishJobs(stopped, reportJob{kind: reportStopTranscode, release: release})
}

func (r *reporter) finishJobs(stopped emby.PlaybackReport, cleanup reportJob) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.done = true
	r.queue = append(r.queue, reportJob{kind: reportStopped, report: stopped})
	if cleanup.session != "" || cleanup.release != nil {
		r.queue = append(r.queue, cleanup)
	}
	r.queue = append(r.queue, reportJob{kind: 255}) // the end
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// end ends the goroutine without a stopped report, for a session that
// never reported its start.
func (r *reporter) end() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.done = true
	r.queue = append(r.queue, reportJob{kind: 255})
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *reporter) run() {
	for range r.wake {
		for {
			r.mu.Lock()
			if len(r.queue) == 0 {
				r.mu.Unlock()
				break
			}
			j := r.queue[0]
			r.queue = r.queue[1:]
			r.mu.Unlock()
			if j.kind == 255 {
				return
			}
			r.do(j)
		}
	}
}

func (r *reporter) do(j reportJob) {
	if j.release != nil {
		j.release()
		return
	}
	if r.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
	defer cancel()
	var err error
	what := "progress"
	switch j.kind {
	case reportStart:
		what = "start"
		err = r.client.ReportPlaybackStart(ctx, j.report)
	case reportProgress:
		err = r.client.ReportPlaybackProgress(ctx, j.report)
	case reportStopped:
		what = "stopped"
		err = r.client.ReportPlaybackStopped(ctx, j.report)
	case reportStopTranscode:
		what = "stop transcoding"
		err = r.client.StopTranscoding(ctx, j.session)
	}
	if err != nil {
		log.Printf("player: reporting %s: %v", what, err)
	}
}

// waitGroup waits for wg up to timeout, reporting whether it finished.
func waitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
