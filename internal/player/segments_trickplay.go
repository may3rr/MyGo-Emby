package player

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

const maxTrickSheetsCached = 8

type trickSheetKey struct {
	sourceID string
	width    int
	sheet    int
}

func (p *Player) resetSegmentsAndTrickplay() {
	p.segments = nil
	p.segmentsFromServer = false
	p.dismissedSeg = -1
	p.trickSheets = nil
	p.trickOrder = nil
	p.trickPending = nil
	p.trickFailed = nil
	p.chapThumbs = nil
	p.chapPending = nil
	p.seekThumbShown = false
}

func (p *Player) loadSegments(itemID string) {
	fetch := p.fetchSegments
	if fetch == nil || itemID == "" {
		return
	}
	gen, ctx := p.gen, p.ctx
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		segs, err := fetch(ctx, itemID)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("player: media segments: %v", err)
			}
			return
		}
		if len(segs) == 0 {
			return
		}
		p.post(func() {
			if gen == p.gen && p.active {
				p.segments = segs
				p.segmentsFromServer = true
				p.dismissedSeg = -1
			}
		})
	}()
}

// activeSegment returns the index and segment containing pos, or (-1, nil)
// when none applies or the user dismissed the current segment pass.
func (p *Player) activeSegment(pos time.Duration) (int, *emby.MediaSegment) {
	if p.dismissedSeg >= 0 {
		if p.dismissedSeg >= len(p.segments) || !p.segments[p.dismissedSeg].Contains(pos) {
			p.dismissedSeg = -1
		}
	}
	for i := range p.segments {
		if p.segments[i].Contains(pos) {
			if i == p.dismissedSeg {
				return -1, nil
			}
			return i, &p.segments[i]
		}
	}
	return -1, nil
}

func segmentSkipLabel(typ emby.SegmentType) string {
	switch typ {
	case emby.SegmentOutro:
		return str.SkipOutro
	case emby.SegmentRecap:
		return str.SkipRecap
	case emby.SegmentPreview:
		return str.SkipPreview
	case emby.SegmentCommercial:
		return str.SkipAd
	default:
		return str.SkipIntro
	}
}

func (p *Player) skipActiveSegment(now time.Time) bool {
	_, seg := p.activeSegment(p.framePos)
	if seg == nil {
		return false
	}
	p.dismissedSeg = -1
	p.seekTo(now, seg.End())
	return true
}

func (p *Player) externalLabel() string {
	if p.cfg.ExternalPlayerLabel != "" {
		return p.cfg.ExternalPlayerLabel
	}
	return "外部播放器"
}

func (p *Player) openInExternal() {
	if p.cfg.OnOpenExternal == nil || p.item == nil {
		return
	}
	it, msID, pos, openExt := p.item, p.mediaSourceID, p.lastPos, p.cfg.OnOpenExternal
	p.exit(false)
	openExt(it, msID, pos)
}

func (p *Player) hasUnburnableBitmapSubs() bool {
	if p.canBurnIn {
		return false
	}
	for i := range p.subTracks {
		if !p.subTracks[i].IsText {
			return true
		}
	}
	return false
}

// previewThumbnail returns a bitmap, an optional pixel source region (non-empty
// for Trickplay sprite sheets, zero for full chapter images), and display size
// (width, height in DIPs) for the seek preview at target.
func (p *Player) previewThumbnail(target time.Duration) (bmp *ui.Bitmap, src ui.Rect, w, h float32) {
	if p.item == nil {
		return nil, ui.Rect{}, 0, 0
	}
	if msID, widthKey, info, ok := p.item.BestTrickplay(p.mediaSourceID, 320); ok {
		if sheetIdx, tx, ty, tw, th, ok := info.TileAt(target); ok {
			if sheetBmp := p.trickplayBitmap(p.item.ID, msID, widthKey, sheetIdx); sheetBmp != nil {
				const dispW = float32(168)
				dispH := dispW * float32(th) / max(float32(tw), 1)
				dispH = min(max(dispH, 72), 126)
				return sheetBmp, ui.Rect{X: float32(tx), Y: float32(ty), W: float32(tw), H: float32(th)}, dispW, dispH
			}
		}
	}
	if ci := p.chapterAt(target); ci >= 0 && p.chapters[ci].ImageTag != "" && p.cfg.Client != nil {
		u := p.cfg.Client.ImageURL(p.item.ID, emby.ImageChapter, emby.ImageOptions{
			MaxWidth: 336,
			Quality:  85,
			Tag:      p.chapters[ci].ImageTag,
			Index:    ci,
		})
		if chBmp := p.chapterBitmap(u); chBmp != nil {
			return chBmp, ui.Rect{}, 168, 94.5
		}
	}
	return nil, ui.Rect{}, 0, 0
}

func (p *Player) trickplayBitmap(itemID, sourceID string, width, sheet int) *ui.Bitmap {
	k := trickSheetKey{sourceID: sourceID, width: width, sheet: sheet}
	if p.trickSheets != nil {
		if bmp := p.trickSheets[k]; bmp != nil {
			return bmp
		}
	}
	if p.trickFailed[k] || p.trickPending[k] || p.fetchTrickSheet == nil {
		return nil
	}
	if p.trickPending == nil {
		p.trickPending = map[trickSheetKey]bool{}
	}
	p.trickPending[k] = true
	gen, ctx, fetch := p.gen, p.ctx, p.fetchTrickSheet
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		data, err := fetch(ctx, itemID, sourceID, width, sheet)
		var bmp *ui.Bitmap
		if err == nil && len(data) > 0 {
			bmp, err = ui.DecodeBitmap(data)
		}
		if ctx.Err() != nil {
			return
		}
		p.post(func() {
			if gen != p.gen || !p.active {
				return
			}
			delete(p.trickPending, k)
			if err != nil || bmp == nil {
				if p.trickFailed == nil {
					p.trickFailed = map[trickSheetKey]bool{}
				}
				p.trickFailed[k] = true
				return
			}
			if p.trickSheets == nil {
				p.trickSheets = map[trickSheetKey]*ui.Bitmap{}
			}
			if len(p.trickOrder) >= maxTrickSheetsCached {
				evict := p.trickOrder[0]
				p.trickOrder = p.trickOrder[1:]
				delete(p.trickSheets, evict)
			}
			p.trickSheets[k] = bmp
			p.trickOrder = append(p.trickOrder, k)
		})
	}()
	return nil
}

func (p *Player) chapterBitmap(url string) *ui.Bitmap {
	if url == "" {
		return nil
	}
	if p.cfg.Image != nil {
		return p.cfg.Image(url)
	}
	if p.chapThumbs != nil {
		if bmp := p.chapThumbs[url]; bmp != nil {
			return bmp
		}
	}
	if p.chapPending[url] {
		return nil
	}
	if p.chapPending == nil {
		p.chapPending = map[string]bool{}
	}
	p.chapPending[url] = true
	gen, ctx, hc := p.gen, p.ctx, p.http
	p.reports.Add(1)
	go func() {
		defer p.reports.Done()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := hc.Do(req)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		var bmp *ui.Bitmap
		if resp.StatusCode == http.StatusOK {
			if data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)); err == nil {
				bmp, _ = ui.DecodeBitmap(data)
			}
		}
		if ctx.Err() != nil {
			return
		}
		p.post(func() {
			if gen != p.gen || !p.active {
				return
			}
			delete(p.chapPending, url)
			if bmp != nil {
				if p.chapThumbs == nil {
					p.chapThumbs = map[string]*ui.Bitmap{}
				}
				if len(p.chapThumbs) >= maxTrickSheetsCached {
					for old := range p.chapThumbs {
						delete(p.chapThumbs, old)
						break
					}
				}
				p.chapThumbs[url] = bmp
			}
		})
	}()
	return nil
}
