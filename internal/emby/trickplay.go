package emby

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// TrickplayInfo describes one resolution of a trickplay thumbnail sprite grid.
type TrickplayInfo struct {
	Width          int `json:"Width"`
	Height         int `json:"Height"`
	TileWidth      int `json:"TileWidth"`
	TileHeight     int `json:"TileHeight"`
	ThumbnailCount int `json:"ThumbnailCount"`
	Interval       int `json:"Interval"` // milliseconds per thumbnail
	Bandwidth      int `json:"Bandwidth,omitempty"`
}

// Valid reports whether the trickplay metadata has usable dimensions and interval.
func (t TrickplayInfo) Valid() bool {
	return t.Width > 0 && t.Height > 0 && t.TileWidth > 0 && t.TileHeight > 0 &&
		t.ThumbnailCount > 0 && t.Interval > 0
}

// TileAt locates the sprite sheet index and pixel sub-rectangle for pos.
func (t TrickplayInfo) TileAt(pos time.Duration) (sheetIndex, x, y, w, h int, ok bool) {
	if !t.Valid() || pos < 0 {
		return 0, 0, 0, 0, 0, false
	}
	idx := int(pos.Milliseconds()) / t.Interval
	if idx >= t.ThumbnailCount {
		idx = t.ThumbnailCount - 1
	}
	if idx < 0 {
		return 0, 0, 0, 0, 0, false
	}
	perSheet := t.TileWidth * t.TileHeight
	sheetIndex = idx / perSheet
	within := idx % perSheet
	col := within % t.TileWidth
	row := within / t.TileWidth
	return sheetIndex, col * t.Width, row * t.Height, t.Width, t.Height, true
}

// BestTrickplay chooses the trickplay grid for mediaSourceID (or the first
// available source) whose tile width is closest to targetWidth.
func (it *Item) BestTrickplay(mediaSourceID string, targetWidth int) (sourceID string, widthKey int, info TrickplayInfo, ok bool) {
	if it == nil || len(it.Trickplay) == 0 {
		return "", 0, TrickplayInfo{}, false
	}
	if targetWidth <= 0 {
		targetWidth = 320
	}
	byWidth, exists := it.Trickplay[mediaSourceID]
	if exists {
		sourceID = mediaSourceID
	} else {
		keys := make([]string, 0, len(it.Trickplay))
		for k := range it.Trickplay {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sourceID = keys[0]
		byWidth = it.Trickplay[sourceID]
	}
	bestDist := int(^uint(0) >> 1)
	for wk, ti := range byWidth {
		if !ti.Valid() {
			continue
		}
		w, err := strconv.Atoi(wk)
		if err != nil || w <= 0 {
			w = ti.Width
		}
		dist := w - targetWidth
		if dist < 0 {
			dist = -dist
		}
		if !ok || dist < bestDist || (dist == bestDist && w > widthKey) {
			widthKey = w
			info = ti
			bestDist = dist
			ok = true
		}
	}
	return sourceID, widthKey, info, ok
}

const maxTrickplaySheetBytes = 16 << 20

// FetchTrickplaySheet downloads one JPEG/WebP trickplay sprite sheet using
// header-based same-origin authentication (never exposing tokens in URLs).
func (c *Client) FetchTrickplaySheet(ctx context.Context, itemID, mediaSourceID string, width, sheetIndex int) ([]byte, error) {
	if itemID == "" || width <= 0 || sheetIndex < 0 {
		return nil, &Error{Kind: KindInvalidURL, Message: "invalid trickplay parameters"}
	}
	q := url.Values{}
	if mediaSourceID != "" {
		q.Set("MediaSourceId", mediaSourceID)
	}
	path := fmt.Sprintf("/Videos/%s/Trickplay/%d/%d.jpg", url.PathEscape(itemID), width, sheetIndex)
	return c.fetchRaw(ctx, c.apiURL(path, q), maxTrickplaySheetBytes)
}
