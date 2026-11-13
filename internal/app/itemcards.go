package app

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// Items of the server as the kit shows them: their pictures, their cards,
// and where opening one goes.

// itemImage is the URL of the picture of it for role, asked of the server
// at the size it shows, dips wide, or "" where it has none.
func (a *app) itemImage(it *emby.Item, role emby.ImageRole, dips float32) string {
	if a.client == nil || it == nil {
		return ""
	}
	ref, ok := it.Image(role)
	if !ok {
		return ""
	}
	return a.client.ImageURL(ref.ItemID, ref.Type, emby.ImageOptions{
		MaxWidth: a.imgPx(dips), Quality: 90, Tag: ref.Tag, Index: ref.Index,
	})
}

// itemIcon is what a card shows of an item without a picture.
func itemIcon(it *emby.Item) *ui.SVG {
	switch it.Type {
	case emby.TypeSeries, emby.TypeSeason, emby.TypeEpisode, emby.TypeTvChannel:
		return iconTV
	case emby.TypeBoxSet:
		return iconCollection
	case emby.TypeMusicAlbum, emby.TypeMusicArtist, emby.TypeAudio, emby.TypeMusicVideo:
		return iconMusic
	case emby.TypePlaylist:
		return iconList
	case emby.TypePerson:
		return iconUser
	case emby.TypeMovie, emby.TypeVideo, emby.TypeTrailer:
		return iconFilm
	}
	if it.IsFolderLike() {
		return iconFolder
	}
	return iconFilm
}

// episodeText says which episode it is: "第 1 季第 2 集", or "" for what is
// not an episode.
func episodeText(it *emby.Item) string {
	if it.Type != emby.TypeEpisode {
		return ""
	}
	ep, ok := it.Episode()
	if !ok {
		return ""
	}
	if s, ok := it.Season(); ok {
		return fmt.Sprintf(str.EpisodeFmt, s, ep)
	}
	return fmt.Sprintf("第 %d 集", ep)
}

// cardTitle is the name a card shows: an episode's series, under which its
// own name goes (cardSubtitle).
func cardTitle(it *emby.Item) string {
	if it.Type == emby.TypeEpisode && it.SeriesName != "" {
		return it.SeriesName
	}
	return it.Name
}

// cardSubtitle is the line under a card's title.
func cardSubtitle(it *emby.Item, resume bool) string {
	switch it.Type {
	case emby.TypeEpisode:
		if it.SeriesName == "" {
			return episodeText(it)
		}
		return joinNonEmpty(" · ", episodeText(it), it.Name)
	case emby.TypeBoxSet:
		return str.BoxSet
	case emby.TypeMusicAlbum:
		return joinNonEmpty(" · ", it.AlbumArtist, yearText(it))
	}
	if resume && it.CanResume() {
		if left := it.Remaining(); left > 0 {
			return fmt.Sprintf(str.RemainingFmt, runtimeText(left))
		}
	}
	return yearText(it)
}

func yearText(it *emby.Item) string { return it.YearText() }

// cardOpts says how an item's card shows.
type cardOpts struct {
	libID     string  // the library it was found in, for opening a folder in it
	width     float32 // the card's width, 0 to fill a grid's cell
	shown     float32 // how wide its picture shows, to ask the server for: width when 0
	landscape bool    // a 16:9 card rather than a poster
	resume    bool    // its line says the time left
}

// itemCard is the card of it.
func (a *app) itemCard(c *ui.Context, it *emby.Item, o cardOpts) {
	item := *it // the card's callbacks outlive the slice it is in
	shown := o.shown
	if shown <= 0 {
		shown = o.width
	}
	if shown <= 0 {
		shown = posterWidth
	}
	s := cardSpec{
		Key:      item.ID,
		Title:    cardTitle(&item),
		Subtitle: cardSubtitle(&item, o.resume),
		Fallback: itemIcon(&item),
		Width:    o.width,
		Played:   item.IsPlayed(),
		Favorite: item.IsFavorite(),
		OnOpen:   func() { a.openItem(&item, o.libID) },
		Menu:     a.itemMenu(&item),
	}
	if item.IsFolderLike() {
		s.Unplayed = item.UnplayedCount()
	}
	if item.CanResume() {
		s.Progress = item.Progress()
	}
	if item.IsPlayable() || item.Type == emby.TypeSeries {
		s.OnPlay = func() { a.play(&item, playOptions{}) }
	}
	if o.landscape {
		s.Image = a.itemImage(&item, emby.RoleLandscape, shown)
		a.landscapeCard(c, s)
		return
	}
	s.Image = a.itemImage(&item, emby.RolePoster, shown)
	a.posterCard(c, s)
}

// openItem opens it: a series or what plays on its page; a folder, a box
// set or an album in the library page, a level down.
func (a *app) openItem(it *emby.Item, libID string) {
	switch {
	case it.Type == emby.TypeSeries || it.IsPlayable() || !it.IsFolderLike():
		a.navigate(pathItem(it.ID))
	default:
		if libID == "" {
			libID = it.ParentID
		}
		a.navigate(pathFolder(libID, it.ID, it.Name))
	}
}

// pathFolder is the page of a folder of a library: its items, a level
// down, named name.
func pathFolder(libID, folderID, name string) string {
	return pathLibrary(libID) + "/" + url.PathEscape(folderID) + "?" + url.Values{"name": {name}}.Encode()
}

// itemCountText says how many items there are: "2160 项".
func itemCountText(n int) string { return fmt.Sprintf(str.ItemsFmt, n) }

// itoa is strconv.Itoa, for the keys of rows.
func itoa(i int) string { return strconv.Itoa(i) }
