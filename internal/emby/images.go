package emby

import (
	"net/url"
	"strconv"
)

// ImageType is a server image type.
type ImageType string

// Image types.
const (
	ImagePrimary  ImageType = "Primary"
	ImageBackdrop ImageType = "Backdrop"
	ImageThumb    ImageType = "Thumb"
	ImageLogo     ImageType = "Logo"
	ImageBanner   ImageType = "Banner"
	ImageArt      ImageType = "Art"
	ImageDisc     ImageType = "Disc"
	ImageChapter  ImageType = "Chapter"
)

// ImageOptions controls the size and caching of a requested image.
// Zero fields are omitted. Request MaxWidth/MaxHeight at display size times
// screen scale; the server scales down preserving aspect ratio.
type ImageOptions struct {
	MaxWidth  int
	MaxHeight int
	Width     int // exact width (may distort on some servers; prefer MaxWidth)
	Height    int
	// FillWidth/FillHeight crop to fill the box (Jellyfin; Emby ignores them).
	FillWidth  int
	FillHeight int
	Quality    int    // 1-100, typically 90
	Tag        string // image tag: changes when the image changes (cache busting)
	Index      int    // backdrop/chapter index (used for Backdrop and Chapter)
}

func (o ImageOptions) query() url.Values {
	q := url.Values{}
	setInt := func(k string, v int) {
		if v > 0 {
			q.Set(k, strconv.Itoa(v))
		}
	}
	setInt("maxWidth", o.MaxWidth)
	setInt("maxHeight", o.MaxHeight)
	setInt("width", o.Width)
	setInt("height", o.Height)
	setInt("fillWidth", o.FillWidth)
	setInt("fillHeight", o.FillHeight)
	setInt("quality", o.Quality)
	if o.Tag != "" {
		q.Set("tag", o.Tag)
	}
	return q
}

// ImageURL returns the URL of an item image:
// /Items/{id}/Images/{type}[/{index}]?maxWidth=…&quality=…&tag=…
// No token is added: Emby and Jellyfin serve images anonymously, and keeping
// the token out keeps the URL usable as a stable cache key.
func (c *Client) ImageURL(itemID string, typ ImageType, opts ImageOptions) string {
	if itemID == "" {
		return ""
	}
	p := "/Items/" + url.PathEscape(itemID) + "/Images/" + string(typ)
	if typ == ImageBackdrop || typ == ImageChapter || opts.Index > 0 {
		p += "/" + strconv.Itoa(opts.Index)
	}
	return c.apiURL(p, opts.query())
}

// UserImageURL returns the avatar URL of a user (/Users/{id}/Images/Primary).
// Returns "" when tag is empty, i.e. the user has no avatar.
func (c *Client) UserImageURL(userID, tag string, opts ImageOptions) string {
	if userID == "" || tag == "" {
		return ""
	}
	opts.Tag = tag
	return c.apiURL("/Users/"+url.PathEscape(userID)+"/Images/Primary", opts.query())
}

// PersonImageURL returns a person's portrait URL, or "" if the person has
// no image.
func (c *Client) PersonImageURL(p Person, opts ImageOptions) string {
	if p.ID == "" || p.PrimaryImageTag == "" {
		return ""
	}
	opts.Tag = p.PrimaryImageTag
	return c.ImageURL(p.ID, ImagePrimary, opts)
}

// ImageRef points at one concrete image: which item owns it, its type,
// index and tag. It is what Item.Image resolves a role to.
type ImageRef struct {
	ItemID string
	Type   ImageType
	Index  int
	Tag    string
}

// IsZero reports whether the reference points at nothing.
func (r ImageRef) IsZero() bool { return r.ItemID == "" || r.Tag == "" }

// URL builds the image URL for r with opts (Tag and Index come from r).
// Returns "" when r is zero.
func (c *Client) ImageRefURL(r ImageRef, opts ImageOptions) string {
	if r.IsZero() {
		return ""
	}
	opts.Tag = r.Tag
	opts.Index = r.Index
	return c.ImageURL(r.ItemID, r.Type, opts)
}

// ImageRole is what an image is used for in the UI.
type ImageRole int

// Image roles.
const (
	// RolePoster is a 2:3 portrait (movie poster, series poster). For
	// episodes it is the series poster.
	RolePoster ImageRole = iota
	// RoleLandscape is a 16:9 card (continue watching, episode lists).
	RoleLandscape
	// RoleBackdrop is a full-width background.
	RoleBackdrop
	// RoleLogo is a transparent title logo.
	RoleLogo
	// RoleBanner is a wide banner.
	RoleBanner
	// RolePrimary is the item's own primary image (episode still, library
	// cover, person photo), falling back like RolePoster.
	RolePrimary
)

// Image picks the best available image for role, with fallbacks:
//
//   - RolePoster: series poster for episodes and seasons without their own;
//     the item's Primary otherwise; then the series poster.
//   - RoleLandscape: episodes use their own Primary, then ParentThumb, then
//     ParentBackdrop, then the series poster. Other items use Thumb, then
//     Backdrop, then ParentThumb, ParentBackdrop, then Primary.
//   - RoleBackdrop: own backdrop, then ParentBackdropImageTags with
//     ParentBackdropItemId, then own Thumb, then ParentThumb.
//   - RoleLogo: own Logo, then ParentLogoItemId/ParentLogoImageTag.
//   - RoleBanner: own Banner.
//   - RolePrimary: own Primary, then the RolePoster fallbacks.
//
// The second result is false when nothing suitable exists.
func (it *Item) Image(role ImageRole) (ImageRef, bool) {
	var cands []ImageRef
	own := func(t ImageType) ImageRef {
		return ImageRef{ItemID: it.ID, Type: t, Tag: it.ImageTags[string(t)]}
	}
	ownBackdrop := func() ImageRef {
		if len(it.BackdropImageTags) > 0 {
			return ImageRef{ItemID: it.ID, Type: ImageBackdrop, Tag: it.BackdropImageTags[0]}
		}
		return ImageRef{}
	}
	parentBackdrop := func() ImageRef {
		if len(it.ParentBackdropImageTags) > 0 {
			return ImageRef{ItemID: it.ParentBackdropItemID, Type: ImageBackdrop, Tag: it.ParentBackdropImageTags[0]}
		}
		return ImageRef{}
	}
	parentThumb := ImageRef{ItemID: it.ParentThumbItemID, Type: ImageThumb, Tag: it.ParentThumbImageTag}
	seriesPrimary := ImageRef{ItemID: it.SeriesID, Type: ImagePrimary, Tag: it.SeriesPrimaryImageTag}
	seriesThumb := ImageRef{ItemID: it.SeriesID, Type: ImageThumb, Tag: it.SeriesThumbImageTag}
	parentPrimary := ImageRef{ItemID: it.ParentPrimaryImageItemID, Type: ImagePrimary, Tag: it.ParentPrimaryImageTag}
	albumPrimary := ImageRef{ItemID: it.AlbumID, Type: ImagePrimary, Tag: it.AlbumPrimaryImageTag}

	switch role {
	case RolePoster:
		if it.Type == TypeEpisode {
			cands = append(cands, seriesPrimary, parentPrimary, own(ImagePrimary))
		} else {
			cands = append(cands, own(ImagePrimary), seriesPrimary, parentPrimary, albumPrimary)
		}
	case RolePrimary:
		cands = append(cands, own(ImagePrimary), seriesPrimary, parentPrimary, albumPrimary)
	case RoleLandscape:
		if it.Type == TypeEpisode {
			cands = append(cands, own(ImagePrimary), parentThumb, seriesThumb, parentBackdrop(), seriesPrimary)
		} else {
			cands = append(cands, own(ImageThumb), ownBackdrop(), parentThumb, seriesThumb, parentBackdrop(), own(ImagePrimary))
		}
	case RoleBackdrop:
		cands = append(cands, ownBackdrop(), parentBackdrop(), own(ImageThumb), parentThumb, seriesThumb)
	case RoleLogo:
		cands = append(cands, own(ImageLogo), ImageRef{ItemID: it.ParentLogoItemID, Type: ImageLogo, Tag: it.ParentLogoImageTag})
	case RoleBanner:
		cands = append(cands, own(ImageBanner))
	}
	for _, c := range cands {
		if !c.IsZero() {
			return c, true
		}
	}
	return ImageRef{}, false
}

// ItemImageURL is Image(role) followed by ImageRefURL. Returns "" when the
// item has no suitable image.
func (c *Client) ItemImageURL(it *Item, role ImageRole, opts ImageOptions) string {
	if it == nil {
		return ""
	}
	r, ok := it.Image(role)
	if !ok {
		return ""
	}
	return c.ImageRefURL(r, opts)
}
