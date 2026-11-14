package player

import (
	"context"
	"sync"

	"mygo-emby/internal/emby"
)

// transcodeSessions owns server work across current, replacing, prefetched
// and discarded streams. A server may return the same PlaySessionID for
// several opens, so closing one Stream does not necessarily end its work.
type transcodeSessions struct {
	mu       sync.Mutex
	refs     map[string]int
	opening  int
	stopping chan struct{}
	client   reportClient
	wg       *sync.WaitGroup
}

func (p *Player) streamOpener() func(context.Context, Request) (*Stream, error) {
	if p.transcodes == nil {
		p.transcodes = &transcodeSessions{refs: map[string]int{}, client: p.reportTo, wg: &p.reports}
	}
	sessions, source := p.transcodes, p.src
	return func(ctx context.Context, req Request) (*Stream, error) {
		return sessions.open(ctx, source, req)
	}
}

func (s *transcodeSessions) open(ctx context.Context, source Source, req Request) (*Stream, error) {
	// A stop already sent must finish before another open can reuse its
	// session ID. Waiting happens only on the source's worker goroutine.
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		stopping := s.stopping
		if stopping == nil {
			s.opening++
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-stopping:
		}
	}
	st, err := source.Open(ctx, req)
	s.mu.Lock()
	if st != nil && st.Method == emby.Transcode {
		if st.Playback != nil && st.Playback.PlaySessionID != "" {
			id := st.Playback.PlaySessionID
			s.refs[id]++
			st.releaseServer = sync.OnceFunc(func() { s.release(id) })
		}
	}
	s.opening--
	s.stopIdleLocked()
	s.mu.Unlock()
	if err != nil {
		st.close()
		return nil, err
	}
	return st, nil
}

func (s *transcodeSessions) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[id]--
	s.stopIdleLocked()
}

// No session is stopped while an open is in flight: its answer may share
// that session ID. New opens wait for the resulting stops before proceeding.
func (s *transcodeSessions) stopIdleLocked() {
	if s.opening != 0 || s.stopping != nil {
		return
	}
	var ids []string
	for id, count := range s.refs {
		if count == 0 {
			ids = append(ids, id)
			delete(s.refs, id)
		}
	}
	if len(ids) == 0 {
		return
	}
	done := make(chan struct{})
	s.stopping = done
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for _, id := range ids {
			if s.client != nil {
				ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
				_ = s.client.StopTranscoding(ctx, id)
				cancel()
			}
		}
		s.mu.Lock()
		s.stopping = nil
		close(done)
		s.stopIdleLocked()
		s.mu.Unlock()
	}()
}
