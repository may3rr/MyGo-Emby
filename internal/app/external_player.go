package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"time"

	"mygo-emby/internal/emby"
)

// externalPlayer describes a locally installed third-party media player used
// as an optional handoff or fallback for unsupported codecs/containers.
type externalPlayer struct {
	Name string
	Kind string // "iina", "mpv", "vlc"
	Path string
}

// detectExternalPlayersAsync scans common install locations and $PATH off
// the main thread and updates a.extPlayers once finished.
func (a *app) detectExternalPlayersAsync() {
	go func() {
		found := detectExternalPlayers()
		if len(found) == 0 {
			return
		}
		a.update(func() {
			// Preserve players injected earlier (e.g. during tests).
			if len(a.extPlayers) == 0 {
				a.extPlayers = found
			}
		})
	}()
}

func detectExternalPlayers() []externalPlayer {
	type probe struct {
		name, kind string
		paths      []string
		commands   []string
	}
	home, _ := os.UserHomeDir()
	var probes []probe
	if runtime.GOOS == "darwin" {
		iinaPaths := []string{"/Applications/IINA.app/Contents/MacOS/iina-cli"}
		vlcPaths := []string{"/Applications/VLC.app/Contents/MacOS/VLC"}
		if home != "" {
			iinaPaths = append(iinaPaths, filepath.Join(home, "Applications", "IINA.app", "Contents", "MacOS", "iina-cli"))
			vlcPaths = append(vlcPaths, filepath.Join(home, "Applications", "VLC.app", "Contents", "MacOS", "VLC"))
		}
		probes = []probe{
			{name: "IINA", kind: "iina", paths: iinaPaths, commands: []string{"iina-cli", "iina"}},
			{name: "mpv", kind: "mpv", paths: []string{"/opt/homebrew/bin/mpv", "/usr/local/bin/mpv"}, commands: []string{"mpv"}},
			{name: "VLC", kind: "vlc", paths: vlcPaths, commands: []string{"vlc"}},
		}
	} else {
		probes = []probe{
			{name: "IINA", kind: "iina", commands: []string{"iina-cli", "iina"}},
			{name: "mpv", kind: "mpv", commands: []string{"mpv"}},
			{name: "VLC", kind: "vlc", commands: []string{"vlc"}},
		}
	}

	var out []externalPlayer
	for _, p := range probes {
		if path := firstExecutable(p.paths, p.commands); path != "" {
			out = append(out, externalPlayer{Name: p.name, Kind: p.kind, Path: path})
		}
	}
	return out
}

func firstExecutable(paths, commands []string) string {
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	for _, cmd := range commands {
		if p, err := exec.LookPath(cmd); err == nil && p != "" {
			return p
		}
	}
	return ""
}

// openExternal fetches the item's MediaSources when needed, builds a tokenized
// direct-stream URL without logging credentials, and launches ep directly.
func (a *app) openExternal(ep externalPlayer, it *emby.Item, mediaSourceID string, start time.Duration) {
	if it == nil || a.client == nil {
		return
	}
	item := *it
	if len(item.MediaSources) > 0 {
		item.MediaSources = slices.Clone(item.MediaSources)
	}
	title := item.DisplayTitle()
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) (string, error) {
		if len(item.MediaSources) == 0 {
			fetched, err := cl.Item(ctx, item.ID)
			if err != nil {
				return "", err
			}
			item = *fetched
		}
		var src *emby.MediaSource
		if mediaSourceID != "" {
			for i := range item.MediaSources {
				if item.MediaSources[i].ID == mediaSourceID {
					src = &item.MediaSources[i]
					break
				}
			}
		}
		if src == nil && len(item.MediaSources) > 0 {
			src = &item.MediaSources[0]
		}
		streamURL := cl.DirectStreamURL(item.ID, src)
		if streamURL == "" {
			return "", errors.New("empty stream URL")
		}
		return streamURL, nil
	}, func(streamURL string, err error) {
		if a.epoch != epoch {
			return
		}
		if err != nil {
			a.toast = a.errText(err)
			return
		}
		a.runExternal(ep, streamURL, title, start)
	})
}

func (a *app) runExternal(ep externalPlayer, streamURL, title string, start time.Duration) {
	var err error
	if a.launchExternal != nil {
		err = a.launchExternal(ep, streamURL, title, start)
	} else {
		args := externalPlayerArgs(ep.Kind, streamURL, title, start)
		cmd := exec.Command(ep.Path, args...)
		if err = cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	if err != nil {
		log.Printf("external player (%s) %s: %v", ep.Kind, emby.RedactURL(streamURL), err)
		a.toast = fmt.Sprintf(str.ExternalFailedFmt, err)
		return
	}
	a.toast = fmt.Sprintf(str.OpenedExternalFmt, ep.Name)
}

func externalPlayerArgs(kind, streamURL, title string, start time.Duration) []string {
	secs := int(start / time.Second)
	switch kind {
	case "iina":
		args := []string{"--no-stdin"}
		if title != "" {
			args = append(args, "--mpv-force-media-title="+title)
		}
		if secs > 0 {
			args = append(args, "--mpv-start="+strconv.Itoa(secs))
		}
		return append(args, streamURL)
	case "mpv":
		var args []string
		if title != "" {
			args = append(args, "--force-media-title="+title)
		}
		if secs > 0 {
			args = append(args, "--start="+strconv.Itoa(secs))
		}
		return append(args, streamURL)
	case "vlc":
		var args []string
		if title != "" {
			args = append(args, "--meta-title="+title)
		}
		if secs > 0 {
			args = append(args, "--start-time="+strconv.Itoa(secs))
		}
		return append(args, streamURL)
	default:
		return []string{streamURL}
	}
}
