//go:build darwin

package player

import "testing"

func TestNativePlaybackWaitsForPresentation(t *testing.T) {
	// No AppKit objects exist yet: commands before Video must only change
	// the desired state, without starting the native clock or calling it.
	e := &nativeEngine{rate: 1, playing: true}
	e.Play()
	e.SetRate(1.5)
	if e.started || !e.playing || e.rate != 1.5 {
		t.Fatal("unshown playback did not preserve the requested state")
	}
}
