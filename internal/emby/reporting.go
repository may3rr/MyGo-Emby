package emby

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// PlaybackReport is the body of the /Sessions/Playing* reports.
type PlaybackReport struct {
	ItemID              string     `json:"ItemId"`
	MediaSourceID       string     `json:"MediaSourceId,omitempty"`
	PlaySessionID       string     `json:"PlaySessionId,omitempty"`
	PositionTicks       int64      `json:"PositionTicks"`
	IsPaused            bool       `json:"IsPaused"`
	IsMuted             bool       `json:"IsMuted"`
	VolumeLevel         int        `json:"VolumeLevel"` // 0-100
	PlayMethod          PlayMethod `json:"PlayMethod,omitempty"`
	AudioStreamIndex    *int       `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int       `json:"SubtitleStreamIndex,omitempty"`
	CanSeek             bool       `json:"CanSeek"`
	// PlaybackRate is the speed (1 = normal); 0 is omitted.
	PlaybackRate float64 `json:"PlaybackRate,omitempty"`
	// EventName is optional: "timeupdate", "pause", "unpause",
	// "volumechange", "audiotrackchange", "subtitletrackchange"…
	EventName string `json:"EventName,omitempty"`
	// Failed marks a stop caused by a playback error (stopped report only).
	Failed bool `json:"Failed,omitempty"`
}

// Report returns a PlaybackReport for this playback at position pos, with
// ids, play method and stream indexes filled in and volume at 100.
func (p *Playback) Report(pos time.Duration, paused bool) PlaybackReport {
	r := PlaybackReport{
		ItemID:        p.ItemID,
		MediaSourceID: p.MediaSourceID,
		PlaySessionID: p.PlaySessionID,
		PositionTicks: DurationToTicks(pos),
		IsPaused:      paused,
		VolumeLevel:   100,
		PlayMethod:    p.Method,
		CanSeek:       true,
	}
	if p.AudioStreamIndex >= 0 {
		r.AudioStreamIndex = Index(p.AudioStreamIndex)
	}
	r.SubtitleStreamIndex = Index(p.SubtitleStreamIndex)
	return r
}

// ReportPlaybackStart tells the server playback started (POST
// /Sessions/Playing). Note: Jellyfin counts a play on start.
func (c *Client) ReportPlaybackStart(ctx context.Context, r PlaybackReport) error {
	return c.report(ctx, "/Sessions/Playing", r)
}

// ReportPlaybackProgress sends the current position (POST
// /Sessions/Playing/Progress). Call it every ~10 s and on pause, seek and
// track changes; it also keeps a transcode alive.
func (c *Client) ReportPlaybackProgress(ctx context.Context, r PlaybackReport) error {
	return c.report(ctx, "/Sessions/Playing/Progress", r)
}

// ReportPlaybackStopped tells the server playback ended (POST
// /Sessions/Playing/Stopped). The server stores PositionTicks as the resume
// point, or marks the item played when it is near the end.
func (c *Client) ReportPlaybackStopped(ctx context.Context, r PlaybackReport) error {
	return c.report(ctx, "/Sessions/Playing/Stopped", r)
}

func (c *Client) report(ctx context.Context, path string, r PlaybackReport) error {
	if _, err := c.requireUser("POST " + path); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, nil, r, nil)
}

// StopTranscoding asks the server to kill the transcode of a play session
// (DELETE /Videos/ActiveEncodings?DeviceId=…&PlaySessionId=…). Call it after
// ReportPlaybackStopped for Transcode playbacks, and before re-requesting
// PlaybackInfo to switch audio track. Servers answer 204 even if no
// transcode was running.
func (c *Client) StopTranscoding(ctx context.Context, playSessionID string) error {
	if _, err := c.requireUser("DELETE /Videos/ActiveEncodings"); err != nil {
		return err
	}
	q := url.Values{}
	q.Set("DeviceId", c.device.DeviceID)
	if playSessionID != "" {
		q.Set("PlaySessionId", playSessionID)
	}
	return c.do(ctx, http.MethodDelete, "/Videos/ActiveEncodings", q, nil, nil)
}
