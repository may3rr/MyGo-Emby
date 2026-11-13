package player

import "time"

// Seeking costs: AVPlayer seeks exactly (to the frame, decoding from the
// keyframe before it), a direct stream asks the server for another range,
// and a transcoded stream makes the server restart its transcode. The
// seeker therefore merges what comes close together into one seek:
//
//   - Steps (arrow keys, the skip buttons) add up while they come within
//     stepWindow of each other: three presses of → within 400 ms are one
//     seek of +30 s, made 400 ms after the last.
//   - A target (the progress bar let go) is one seek at once.
//   - Any seek waits until minGap after the one before, which is 0 for
//     files and stream copies and longer for server transcodes.
//
// The view shows the target while a seek waits (Preview), so the time and
// the bar move at once.
type seeker struct {
	stepWindow time.Duration
	minGap     time.Duration

	pending bool
	// relative: delta is added to the position at the time the seek is
	// made; else target is where it goes.
	relative bool
	delta    time.Duration
	target   time.Duration
	// base is the position the delta was added to, for the preview.
	base time.Duration
	due  time.Time
	last time.Time
}

const defaultStepWindow = 400 * time.Millisecond

// step adds d to a pending relative seek, or starts one from pos.
func (s *seeker) step(now time.Time, pos, d time.Duration) {
	if !s.pending || !s.relative {
		s.pending, s.relative, s.delta, s.base = true, true, 0, pos
	}
	s.delta += d
	s.due = now.Add(s.window())
	s.respectGap()
}

// seekTo asks for a seek to target, made as soon as the gap allows.
func (s *seeker) seekTo(now time.Time, target time.Duration) {
	s.pending, s.relative, s.delta, s.target = true, false, 0, target
	s.due = now
	s.respectGap()
}

func (s *seeker) window() time.Duration {
	if s.stepWindow > 0 {
		return s.stepWindow
	}
	return defaultStepWindow
}

func (s *seeker) respectGap() {
	if s.minGap > 0 && !s.last.IsZero() {
		if earliest := s.last.Add(s.minGap); s.due.Before(earliest) {
			s.due = earliest
		}
	}
}

// preview returns where the pending seek goes, for the view, given the
// position now; ok is false when none waits.
func (s *seeker) preview(pos time.Duration) (time.Duration, bool) {
	if !s.pending {
		return 0, false
	}
	if s.relative {
		return s.base + s.delta, true
	}
	return s.target, true
}

// take reports whether the pending seek is due at now, and its target
// given the position pos then; it clears the seek when it is. wait is how
// long until it is due otherwise (0 when none waits).
func (s *seeker) take(now time.Time, pos time.Duration) (target time.Duration, ok bool, wait time.Duration) {
	if !s.pending {
		return 0, false, 0
	}
	if now.Before(s.due) {
		return 0, false, s.due.Sub(now)
	}
	target = s.target
	if s.relative {
		target = pos + s.delta
	}
	s.pending, s.delta = false, 0
	s.last = now
	return target, true, 0
}

// reset drops a pending seek, as when the stream changes.
func (s *seeker) reset() {
	s.pending, s.delta = false, 0
	s.last = time.Time{}
}

// clampSeek keeps a target within the video, short of its very end, so
// that a seek forward never lands on the end and finishes the video.
func clampSeek(t, dur time.Duration) time.Duration {
	if t < 0 {
		return 0
	}
	if dur > 0 && t > dur-time.Second {
		return max(dur-time.Second, 0)
	}
	return t
}
