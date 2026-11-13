//go:build darwin || linux

package remux

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"mygo-emby/internal/emby"
)

func TestTranscodedAudioMapsProbedTrackOrder(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := writeAudioScript(t, fmt.Sprintf("printf '%%s\\n' \"$@\" > %s\nexit 1\n", shellQuote(argsFile)))
	srv := &Server{ffmpeg: script}
	_, err := srv.transcodeAudio(context.Background(), "input", &track{order: 2}, 0, 0, time.Second)
	if !errors.Is(err, ErrAudioTranscodeFailed) {
		t.Fatalf("transcode error %v", err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	if !strings.Contains(got, "-map\n0:a:2\n") {
		t.Fatalf("FFmpeg args did not map the probed audio track order:\n%s", got)
	}
	if strings.Contains(got, "-map\n0:2\n") {
		t.Fatalf("FFmpeg mapped a server stream index as a container index:\n%s", got)
	}
}

func TestAudioTranscoderCancelKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := writeAudioScript(t, fmt.Sprintf("printf '%%s\\n' \"$$\" > %s\nexec sleep 30\n", shellQuote(pidFile)))
	srv := &Server{ffmpeg: script}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := srv.transcodeAudio(ctx, "input", &track{order: 0}, 0, 0, time.Second)
		done <- err
	}()
	pid := waitAudioPID(t, pidFile)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("audio transcoder did not stop after cancellation")
	}
	waitAudioProcessGone(t, pid)
}

func TestSessionCloseStopsAudioTranscoder(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := writeAudioScript(t, fmt.Sprintf("printf '%%s\\n' \"$$\" > %s\nexec sleep 30\n", shellQuote(pidFile)))
	files := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer files.Close()
	srv, err := NewServer(WithFFmpeg(script))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	session, err := srv.Open(context.Background(), Source{
		URL:            files.URL + "/mkv_hevc_dts.mkv",
		AudioIndex:     -1,
		AudioTranscode: true,
		Duration:       10 * time.Second,
		MediaSource: &emby.MediaSource{
			Container: "mkv",
			MediaStreams: []emby.MediaStream{
				{Index: 0, Type: emby.StreamVideo, Codec: "hevc"},
				{Index: 1, Type: emby.StreamAudio, Codec: "dts", IsDefault: true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	pid := waitAudioPID(t, pidFile)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	waitAudioProcessGone(t, pid)
}

func TestSupportedAudioKeepsPureGoPath(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ffmpeg-started")
	script := writeAudioScript(t, fmt.Sprintf("touch %s\nexit 1\n", shellQuote(marker)))
	files := httptest.NewServer(http.FileServer(http.Dir("testdata")))
	defer files.Close()
	srv, err := NewServer(WithFFmpeg(script))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	session, err := srv.Open(context.Background(), Source{
		URL:        files.URL + "/mkv_hevc_eac3_srt.mkv",
		AudioIndex: -1,
		Duration:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_ = get(t, strings.TrimSuffix(session.PlaylistURL, "master.m3u8")+"0.m4s")
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("supported E-AC-3 unexpectedly started FFmpeg (stat error %v)", err)
	}
}

func writeAudioScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func waitAudioPID(t *testing.T, file string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(file)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audio transcoder did not start")
	return 0
}

func waitAudioProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("audio transcoder process %d is still running", pid)
}
