package emby

import (
	"encoding/json"
	"testing"
)

func TestImageURLs(t *testing.T) {
	c, _ := NewClient("https://host/emby/", testDevice)
	got := c.ImageURL("abc", ImagePrimary, ImageOptions{MaxWidth: 300, Quality: 90, Tag: "t1"})
	if want := "https://host/emby/Items/abc/Images/Primary?maxWidth=300&quality=90&tag=t1"; got != want {
		t.Errorf("primary = %q\nwant %q", got, want)
	}
	got = c.ImageURL("abc", ImageBackdrop, ImageOptions{MaxWidth: 1920, Index: 2, Tag: "b2"})
	if want := "https://host/emby/Items/abc/Images/Backdrop/2?maxWidth=1920&tag=b2"; got != want {
		t.Errorf("backdrop = %q", got)
	}
	got = c.ImageURL("abc", ImageBackdrop, ImageOptions{})
	if want := "https://host/emby/Items/abc/Images/Backdrop/0"; got != want {
		t.Errorf("backdrop 0 = %q", got)
	}
	for _, typ := range []ImageType{ImageThumb, ImageLogo, ImageBanner} {
		got = c.ImageURL("i", typ, ImageOptions{MaxHeight: 100, FillWidth: 50})
		if want := "https://host/emby/Items/i/Images/" + string(typ) + "?fillWidth=50&maxHeight=100"; got != want {
			t.Errorf("%s = %q", typ, got)
		}
	}
	if got := c.UserImageURL("u1", "ut", ImageOptions{MaxWidth: 64}); got != "https://host/emby/Users/u1/Images/Primary?maxWidth=64&tag=ut" {
		t.Errorf("user = %q", got)
	}
	if c.UserImageURL("u1", "", ImageOptions{}) != "" || c.ImageURL("", ImagePrimary, ImageOptions{}) != "" {
		t.Error("missing tag/id should give empty url")
	}
	p := Person{ID: "p1", Name: "A", PrimaryImageTag: "pt"}
	if got := c.PersonImageURL(p, ImageOptions{MaxWidth: 200}); got != "https://host/emby/Items/p1/Images/Primary?maxWidth=200&tag=pt" {
		t.Errorf("person = %q", got)
	}
	if c.PersonImageURL(Person{ID: "p2"}, ImageOptions{}) != "" {
		t.Error("person without tag should give empty url")
	}
}

func TestImageRoles(t *testing.T) {
	var ep Item
	if err := json.Unmarshal([]byte(episodeJSON), &ep); err != nil {
		t.Fatal(err)
	}
	check := func(name string, it *Item, role ImageRole, want ImageRef, wantOK bool) {
		t.Helper()
		got, ok := it.Image(role)
		if got != want || ok != wantOK {
			t.Errorf("%s: Image = %+v %v, want %+v %v", name, got, ok, want, wantOK)
		}
	}
	// Episode: landscape = own primary, poster = series primary,
	// backdrop = parent backdrop, logo = parent logo.
	check("ep landscape", &ep, RoleLandscape, ImageRef{"ep1", ImagePrimary, 0, "epp"}, true)
	check("ep poster", &ep, RolePoster, ImageRef{"s1", ImagePrimary, 0, "sp"}, true)
	check("ep backdrop", &ep, RoleBackdrop, ImageRef{"s1", ImageBackdrop, 0, "sb0"}, true)
	check("ep logo", &ep, RoleLogo, ImageRef{"s1", ImageLogo, 0, "sl"}, true)
	check("ep primary", &ep, RolePrimary, ImageRef{"ep1", ImagePrimary, 0, "epp"}, true)
	check("ep banner", &ep, RoleBanner, ImageRef{}, false)

	// Episode without its own still: ParentThumb, then ParentBackdrop, then series poster.
	ep2 := ep
	ep2.ImageTags = nil
	check("ep2 landscape", &ep2, RoleLandscape, ImageRef{"s1", ImageThumb, 0, "st"}, true)
	ep2.ParentThumbImageTag = ""
	check("ep2 landscape no thumb", &ep2, RoleLandscape, ImageRef{"s1", ImageBackdrop, 0, "sb0"}, true)
	ep2.ParentBackdropImageTags = nil
	check("ep2 landscape series", &ep2, RoleLandscape, ImageRef{"s1", ImagePrimary, 0, "sp"}, true)
	ep2.SeriesPrimaryImageTag = ""
	check("ep2 nothing", &ep2, RoleLandscape, ImageRef{}, false)

	movie := Item{ID: "m1", Type: TypeMovie,
		ImageTags:         map[string]string{"Primary": "mp", "Logo": "ml"},
		BackdropImageTags: []string{"mb0"}}
	check("movie poster", &movie, RolePoster, ImageRef{"m1", ImagePrimary, 0, "mp"}, true)
	check("movie landscape", &movie, RoleLandscape, ImageRef{"m1", ImageBackdrop, 0, "mb0"}, true)
	movie.ImageTags["Thumb"] = "mt"
	check("movie landscape thumb", &movie, RoleLandscape, ImageRef{"m1", ImageThumb, 0, "mt"}, true)
	check("movie backdrop", &movie, RoleBackdrop, ImageRef{"m1", ImageBackdrop, 0, "mb0"}, true)

	season := Item{ID: "se1", Type: TypeSeason, SeriesID: "s1", SeriesPrimaryImageTag: "sp"}
	check("season poster fallback", &season, RolePoster, ImageRef{"s1", ImagePrimary, 0, "sp"}, true)

	c, _ := NewClient("http://h:8096", testDevice)
	if got := c.ItemImageURL(&ep, RoleBackdrop, ImageOptions{MaxWidth: 1280}); got != "http://h:8096/Items/s1/Images/Backdrop/0?maxWidth=1280&tag=sb0" {
		t.Errorf("ItemImageURL = %q", got)
	}
	if c.ItemImageURL(&ep2, RoleLandscape, ImageOptions{}) != "" {
		t.Error("expected empty url")
	}
}
