//go:build !darwin

package player

func openVideo(url string) (engine, error) { return openFrameVideo(url) }
