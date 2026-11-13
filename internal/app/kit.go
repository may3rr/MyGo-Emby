package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// The kit: the parts every page is built of. Each is a method of the app
// (for its palette and pictures) taking the view's Context; those that the
// user acts on return their element, or take callbacks where the action may
// change what is being built.

// Cards.

// cardSpec is what a poster or landscape card shows.
type cardSpec struct {
	// Key tells the card apart among its siblings, as the item's ID:
	// required in lists of cards.
	Key      any
	Title    string
	Subtitle string
	// Image is the picture's URL, asked at the size the card shows it
	// (a.imgPx). "" shows Fallback.
	Image string
	// Fallback is the icon a card shows without a picture, or where it
	// failed: the item's kind (iconFilm, iconTV, …).
	Fallback *ui.SVG
	// Width is the card's width; 0 takes its parent's, as in a grid.
	Width float32
	// Progress, from 0 to 1, is how much has been watched: a bar along
	// the picture's foot. 0 shows none.
	Progress float64
	// Played marks the item watched; Unplayed counts the episodes not yet
	// watched, for a series, and takes precedence.
	Played   bool
	Unplayed int
	Favorite bool
	// OnOpen runs as the card is clicked, OnPlay as its play button is:
	// without OnPlay, the card shows no play button.
	OnOpen func()
	OnPlay func()
	// Menu is the card's context menu, as DESIGN.md §4.9 lists it.
	Menu func(m *ui.Menu)
}

const (
	cardTextGap = 8  // the picture to the title
	cardTitleH  = 18 // the title's line
	cardSubH    = 16 // the subtitle's line
	// cardTextH is the height under a card's picture: the cards of a row
	// line up whatever they say.
	cardTextH = cardTextGap + cardTitleH + 2 + cardSubH
)

// posterCard is a 2:3 card, as a movie's or a series' poster.
func (a *app) posterCard(c *ui.Context, s cardSpec) ui.Element {
	return a.card(c, s, 2.0/3, radiusPoster)
}

// landscapeCard is a 16:9 card, as an episode's still or a movie's backdrop.
func (a *app) landscapeCard(c *ui.Context, s cardSpec) ui.Element {
	return a.card(c, s, 16.0/9, radiusLandscape)
}

// card is a picture over a title and a line under it. Under the pointer the
// picture rises toward the viewer, its shadow deepening, and a play button
// shows on it; pressed, it gives a little.
func (a *app) card(c *ui.Context, s cardSpec, aspect, radius float32) ui.Element {
	p := a.pal
	card := ui.ButtonBase(c.Key(s.Key)).Column().AlignItems(ui.Stretch).Justify(ui.Start).
		FocusRing(false).Label(s.Title)
	if s.Width > 0 {
		card.Width(s.Width).Shrink(0)
	}
	hovered := card.Hovered() || (s.Key != nil && a.forceHover == s.Key)
	pressed := card.Pressed()
	focused := card.FocusVisible()
	card.Children(func() {
		art := ui.Box(c.Key("art")).AspectRatio(aspect).Radius(radius).Shrink(0)
		lift := ui.Pose{}
		switch {
		case pressed:
			lift.Scale = 1.01
		case hovered:
			lift.Scale = 1.03
		}
		art.AnimatePose(lift, spring)
		if hovered {
			art.Shadow(0, 14, 30, -4, p.shadowLift)
		} else {
			art.Shadow(0, 1, 4, 0, p.shadow)
		}
		art.Transition(ui.ElementTransition{Shadows: true, Duration: medium.Duration})
		if focused {
			art.DrawOver(func(pt *ui.Painter, r ui.Rect) {
				pt.FocusRing(r, [4]float32{radius, radius, radius, radius})
			})
		}
		art.Children(func() {
			a.artworkTitled(c, s.Image, s.Fallback, s.Title, radius)
			if s.Progress > 0 {
				a.cardProgress(c, s.Progress, radius)
			}
			switch {
			case s.Unplayed > 0:
				a.countBadge(c, s.Unplayed)
			case s.Played:
				a.playedBadge(c)
			}
			if s.OnPlay != nil && (hovered || card.FocusWithin()) {
				a.cardPlayButton(c, s.OnPlay)
			}
		})
		ui.Column(c.Key("text")).Height(cardTextH).Padding(cardTextGap, 2, 0, 2).Gap(2).Children(func() {
			ui.Row(c).Height(cardTitleH).Gap(5).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.Title).FontSize(fsCardTitle).FontWeight(wMedium).TextColor(p.text).
					SingleLine().Shrink(1).MinWidth(0)
				if s.Favorite {
					ui.Icon(c, iconHeartFill).Size(11, 11).TextColor(p.favorite).Shrink(0).Label(str.FavoriteMark)
				}
			})
			if s.Subtitle != "" {
				ui.Text(c, s.Subtitle).FontSize(fsCaption).TextColor(p.text2).Height(cardSubH).SingleLine()
			}
		})
	})
	if s.Menu != nil {
		card.ContextMenu(s.Menu)
	}
	if s.OnOpen != nil {
		card.OnClick(s.OnOpen)
	}
	return card
}

// artwork fills its parent with the picture at url, its corners rounded:
// a placeholder of the skeleton's color while it loads, which the picture
// fades in over once it arrives. A picture that was ready as the card
// first showed shows at once. Without a picture, or where it failed, the
// placeholder shows fallback.
func (a *app) artwork(c *ui.Context, url string, fallback *ui.SVG, radius float32) {
	a.artworkTitled(c, url, fallback, "", radius)
}

// artworkTitled is artwork whose stand-in, for a picture there is not,
// shows title under the icon, as a card of an item without a poster: the
// card says what it is in its own place, not only below it.
func (a *app) artworkTitled(c *ui.Context, url string, fallback *ui.SVG, title string, radius float32) {
	p := a.pal
	bmp, _, failed := a.img.Lookup(url)
	box := ui.Box(c.Key("pic")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radius)
	in := fadeInOn(box, bmp != nil)
	missing := bmp == nil && (url == "" || failed) && fallback != nil
	switch {
	case missing:
		box.Background(p.fill)
	case in < 1:
		box.Background(p.skeleton)
	}
	box.Children(func() {
		if missing {
			ui.Column(c.Key("missing")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Gap(10).
				Padding(12).Children(func() {
				ui.Icon(c, fallback).Size(30, 30).TextColor(p.text3)
				if title != "" {
					ui.Text(c, title).FontSize(fsCaption).FontWeight(wMedium).TextColor(p.text2).
						TextAlign(ui.Center).MaxLines(3).Balance()
				}
			})
		}
		if bmp != nil {
			img := ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover).Radius(radius)
			if in < 1 {
				img.Opacity(in)
			}
		}
	})
}

// fadeInOn returns how far what el shows has faded in: 0 while it is not
// there, then up to 1 over a moment once it is, and 1 at once if it was
// there as el first showed.
func fadeInOn(el ui.Element, there bool) float32 {
	if !there {
		return el.Jump("in", 0)
	}
	return el.AnimateTo("in", 1, fadeIn)
}

// cardProgress is the bar of what has been watched along the foot of a
// card's picture, over a shade that keeps it legible on light pictures.
func (a *app) cardProgress(c *ui.Context, progress float64, radius float32) {
	p := a.pal
	ui.Box(c.Key("shade")).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radius).
		LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
			{Color: ui.Transparent, At: 0.6}, {Color: p.scrimSoft, At: 1},
		}})
	ui.Box(c.Key("progress")).Absolute().Left(10).Right(10).Bottom(9).Height(4).Radius(2).
		Background(p.progressBed).Label(fmt.Sprintf("%d%%", int(progress*100+0.5))).Children(func() {
		ui.Box(c).WidthPercent(float32(min(max(progress, 0.02), 1) * 100)).Height(4).Radius(2).Background(p.progress)
	})
}

// playedBadge is the check in the corner of a card watched to its end.
func (a *app) playedBadge(c *ui.Context) {
	p := a.pal
	ui.Box(c.Key("played")).Absolute().Top(8).Right(8).Size(22, 22).Radius(11).Center().
		Background(p.accent).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.25)).Label(str.Played).Children(func() {
		ui.Icon(c, iconCheck).Size(13, 13).TextColor(p.onAccent)
	})
}

// countBadge is the number of episodes not watched, in a card's corner.
func (a *app) countBadge(c *ui.Context, n int) {
	p := a.pal
	label := fmt.Sprint(n)
	if n > 99 {
		label = "99+"
	}
	ui.Row(c.Key("count")).Absolute().Top(8).Right(8).Height(22).MinWidth(22).PaddingX(7).Radius(11).
		Center().Background(p.accent).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.25)).
		Label(fmt.Sprintf(str.UnplayedFmt, n)).Children(func() {
		ui.Text(c, label).FontSize(fsBadge).FontWeight(wBold).TextColor(p.onAccent).
			FontFeatures("tnum").SingleLine()
	})
}

// cardPlayButton is the round button of glass that plays a card's item
// without opening it, in the middle of its picture.
func (a *app) cardPlayButton(c *ui.Context, play func()) {
	p := a.pal
	b := ui.ButtonBase(c.Key("play")).Absolute().Attach(ui.AnchorCenter, ui.AnchorCenter).
		Size(46, 46).Radius(23).Center().Label(str.Play).Tooltip(str.Play).FocusRingOffset(2).
		Material(glass.Glass{Style: glass.Clear, Tint: ui.RGBA(0, 0, 0, 0.28)})
	b.Transition(ui.ElementTransition{
		Animation: &spring,
		Enter:     &ui.Motion{Scale: 0.6},
		Exit:      &ui.Motion{Scale: 0.6},
	})
	pose := ui.Pose{}
	switch {
	case b.Pressed():
		pose.Scale = 0.92
	case b.Hovered():
		pose.Scale = 1.08
	}
	b.AnimatePose(pose, spring)
	b.Children(func() {
		ui.Icon(c, iconPlay).Size(20, 20).TextColor(p.onImage).Margin(0, 0, 0, 2)
	})
	b.OnClick(play)
}

// Shelves.

// shelfSpec is a row of cards under a title.
type shelfSpec struct {
	Key   any
	Title string
	// OnSeeAll, when set, makes the title a button opening everything the
	// shelf is a part of.
	OnSeeAll func()
	Count    int
	// ItemWidth is each card's width, and ArtHeight the height of its
	// picture, which the paging buttons center on.
	ItemWidth float32
	ArtHeight float32
	// Item builds card i, ItemWidth wide, keyed by its item.
	Item func(i int)
}

const (
	shelfPadTop    = 10 // room in the strip for a lifted card and its shadow
	shelfPadBottom = 18
)

// shelf is a title over a row of cards that scrolls sideways, reaching the
// edges of the content. Under the pointer, buttons at the row's ends page
// it, as the TV app's do; a touchpad scrolls it as it is.
func (a *app) shelf(c *ui.Context, s shelfSpec) {
	ui.Column(c.Key(s.Key)).Shrink(0).Children(func() {
		a.sectionTitle(c, s.Title, s.OnSeeAll).Margin(0, 0, titleGap-shelfPadTop, 0)
		row := ui.Box(c.Key("row")).Margin(0, -gutter, -shelfPadBottom, -gutter)
		// The page keeps where its shelves are scrolled (pagemem.go); the
		// gallery's keep it in the row.
		st := a.shelfScroll(s.Key)
		if st == nil {
			st = ui.Local(row, "scroll", func() ui.ScrollState { return ui.ScrollState{} })
		}
		hovered := row.Hovered()
		width := row.Bounds().W
		row.Children(func() {
			ui.ScrollHorizontal(c.Key("strip")).TrackScroll(st).Scrollbars(false).Children(func() {
				ui.Row(c).Padding(shelfPadTop, gutter, shelfPadBottom, gutter).Gap(cardGap).
					AlignItems(ui.Start).Children(func() {
					for i := range s.Count {
						s.Item(i)
					}
				})
			})
			step := s.ItemWidth + cardGap
			page := float32(math.Max(1, math.Floor(float64((width-2*gutter+cardGap)/step)))) * step
			y := shelfPadTop + s.ArtHeight/2 - 17
			a.shelfPager(c, "prev", iconBack, str.ScrollLeft, hovered && st.X > 1, y, true, func() {
				st.ScrollTo(max(0, snapDown(st.X-page, step)), 0, ui.Smooth)
			})
			a.shelfPager(c, "next", iconForward, str.ScrollRight, hovered && st.X < st.MaxX-1, y, false, func() {
				st.ScrollTo(min(st.MaxX, snapDown(st.X+page, step)), 0, ui.Smooth)
			})
		})
	})
}

// snapDown rounds x down to a multiple of step, so that a page starts with
// a whole card.
func snapDown(x, step float32) float32 {
	if step <= 0 {
		return x
	}
	return float32(math.Floor(float64(x/step+0.01))) * step
}

// shelfPager is a round button of glass at one end of a shelf, shown while
// there is more that way and the pointer is over the shelf.
func (a *app) shelfPager(c *ui.Context, key, icon any, label string, shown bool, y float32, start bool, do func()) {
	p := a.pal
	b := ui.ButtonBase(c.Key(key)).Absolute().Top(y).Size(34, 34).Radius(17).Center().
		Label(label).Tooltip(label).TabStop(false).Material(glass.Glass{})
	if start {
		b.Left(10)
	} else {
		b.Right(10)
	}
	vis := b.AnimateTo("shown", b2f(shown), quick)
	b.Opacity(vis).IgnoreInput(!shown)
	pose := ui.Pose{Scale: 0.9 + 0.1*vis}
	if b.Pressed() {
		pose.Scale = 0.9
	}
	b.AnimatePose(pose, spring)
	b.Children(func() { ui.Icon(c, icon.(*ui.SVG)).Size(17, 17).TextColor(p.text) })
	b.OnClick(do)
}

// sectionTitle is the 20-point title of a part of a page. With onMore it is
// a button, its chevron saying it opens all of it.
func (a *app) sectionTitle(c *ui.Context, title string, onMore func()) ui.Element {
	p := a.pal
	if onMore == nil {
		return ui.Text(c.Key("title"), title).FontSize(fsTitle).FontWeight(wSemibold).TextColor(p.text).
			LetterSpacing(-0.2).Height(26).SingleLine()
	}
	b := ui.ButtonBase(c.Key("title")).AlignSelf(ui.Start).Height(26).Gap(3).AlignItems(ui.Center).
		Label(title + "，" + str.SeeAll).Tooltip(str.SeeAll).Radius(6).FocusRingOffset(2)
	color := p.text2
	if b.Hovered() {
		color = p.text
	}
	b.Children(func() {
		ui.Text(c, title).FontSize(fsTitle).FontWeight(wSemibold).TextColor(p.text).LetterSpacing(-0.2).SingleLine()
		ui.Icon(c, iconChevronRight).Size(18, 18).TextColor(b.AnimateColor("chev", color, quick)).Margin(1, 0, 0, 0)
	})
	b.OnClick(onMore)
	return b
}

// Buttons.

// pillKind is the look of a pill button.
type pillKind int

const (
	pillPrimary   pillKind = iota // filled with the accent: the page's main action
	pillSecondary                 // tinted with the accent
	pillPlain                     // a quiet gray fill
	// Over pictures, as a hero's: white, the main action, and glass.
	pillOnImage
	pillGlass
)

// pillButton is a capsule with an optional leading icon and a label.
func (a *app) pillButton(c *ui.Context, icon *ui.SVG, label string, kind pillKind) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(label)).Height(32).Gap(6).Radius(16).AlignItems(ui.Center).
		Label(label).Shrink(0)
	if icon != nil {
		b.Padding(0, 16, 0, 13)
	} else {
		b.PaddingX(18)
	}
	var bg, fg ui.Color
	switch kind {
	case pillPrimary:
		bg, fg = p.accent, p.onAccent
		if !c.WindowActive() {
			bg, fg = p.fillHover, p.text
		}
		switch {
		case b.Pressed():
			bg = bg.MixOklab(black, 0.14)
		case b.Hovered():
			bg = bg.MixOklab(white, 0.1)
		}
	case pillSecondary:
		bg, fg = p.accent.Alpha(0.13), p.accent
		switch {
		case b.Pressed():
			bg = p.accent.Alpha(0.24)
		case b.Hovered():
			bg = p.accent.Alpha(0.18)
		}
	case pillOnImage:
		bg, fg = ui.RGBA(255, 255, 255, 0.94), ui.Hex("#111113")
		switch {
		case b.Pressed():
			bg = ui.RGBA(225, 225, 230, 0.94)
		case b.Hovered():
			bg = white
		}
	case pillGlass:
		bg, fg = ui.Transparent, p.onImage
		b.Material(glass.Glass{Style: glass.Clear, Tint: ui.RGBA(0, 0, 0, 0.22)})
		switch {
		case b.Pressed():
			bg = ui.RGBA(255, 255, 255, 0.12)
		case b.Hovered():
			bg = ui.RGBA(255, 255, 255, 0.08)
		}
	default:
		bg, fg = p.fill, p.text
		switch {
		case b.Pressed():
			bg = p.fillHover.MixOklab(black, 0.06)
		case b.Hovered():
			bg = p.fillHover
		}
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.97
	}
	b.AnimatePose(pose, spring)
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).Size(15, 15).TextColor(fg)
		}
		ui.Text(c, label).FontSize(fsBody).FontWeight(wSemibold).TextColor(fg).SingleLine()
	})
	return b
}

// iconButton is a round button showing an icon, which a soft disc marks
// under the pointer. label is its tooltip and its name for screen readers.
// size is the button's; its icon is a little over half of it.
func (a *app) iconButton(c *ui.Context, icon *ui.SVG, label string, size float32) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(label)).Size(size, size).Radius(size / 2).Center().
		Label(label).Tooltip(label).Shrink(0)
	bg := ui.Transparent
	switch {
	case b.Pressed():
		bg = p.press
	case b.Hovered():
		bg = p.hover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.9
	}
	b.AnimatePose(pose, spring)
	s := float32(math.Round(float64(size * 0.56)))
	b.Children(func() { ui.Icon(c, icon).Size(s, s).TextColor(p.text) })
	return b
}

// chip is a small capsule of text, as a genre: a button when clickable.
func (a *app) chip(c *ui.Context, label string, clickable bool) ui.Element {
	p := a.pal
	var b ui.Element
	if clickable {
		b = ui.ButtonBase(c.Key(label)).Label(label)
	} else {
		b = ui.Row(c.Key(label))
	}
	b.Height(26).PaddingX(12).Radius(13).AlignItems(ui.Center).Shrink(0)
	bg := p.fill
	if clickable {
		switch {
		case b.Pressed():
			bg = p.fillHover.MixOklab(black, 0.06)
		case b.Hovered():
			bg = p.fillHover
		}
		b.Background(b.AnimateColor("bg", bg, quick))
	} else {
		b.Background(bg)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(fsCaption).FontWeight(wMedium).TextColor(p.text).SingleLine()
	})
	return b
}

// filterChip is a capsule choosing one of a few filters, as 全部, 未看 and
// 收藏 over a library: the one chosen is filled with the text's color.
func (a *app) filterChip(c *ui.Context, label string, selected bool) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(label)).Label(label).Height(28).PaddingX(14).Radius(14).
		AlignItems(ui.Center).Shrink(0).AccessSelected(selected)
	bg, fg := p.fill, p.text
	switch {
	case selected:
		bg, fg = p.text, p.bg
	case b.Pressed():
		bg = p.fillHover.MixOklab(black, 0.06)
	case b.Hovered():
		bg = p.fillHover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		ui.Text(c, label).FontSize(fsBody).FontWeight(wMedium).TextColor(b.AnimateColor("fg", fg, quick)).SingleLine()
	})
	return b
}

// Metadata.

// metaSpec is a line of facts about an item.
type metaSpec struct {
	// Parts are shown in order, a dot between each two: a year, a run
	// time, a season count.
	Parts []string
	// Rating is the community's rating out of 10, shown with a star; 0
	// shows none.
	Rating float64
	// Badge is the official rating, as "PG-13", in an outlined box.
	Badge string
	// OnImage draws it for a picture behind it, in white.
	OnImage bool
}

// metaRow is a line of facts: "2013 · 1 小时 32 分钟 · ★ 7.3 · [PG]".
func (a *app) metaRow(c *ui.Context, m metaSpec) ui.Element {
	p := a.pal
	color, faint := p.text2, p.text3
	if m.OnImage {
		color, faint = p.onImage2, p.onImage2.Alpha(0.6)
	}
	return ui.Row(c).Gap(7).AlignItems(ui.Center).Wrap().Children(func() {
		n := 0
		dot := func() {
			if n > 0 {
				ui.Text(c, "·").FontSize(fsBody).FontWeight(wBold).TextColor(faint)
			}
			n++
		}
		for _, part := range m.Parts {
			if part == "" {
				continue
			}
			dot()
			ui.Text(c, part).FontSize(fsBody).TextColor(color).FontFeatures("tnum").SingleLine()
		}
		if m.Rating > 0 {
			dot()
			ui.Row(c).Gap(3).AlignItems(ui.Center).Label(fmt.Sprintf("评分 %.1f", m.Rating)).Children(func() {
				ui.Icon(c, iconStar).Size(13, 13).TextColor(p.star)
				ui.Text(c, fmt.Sprintf("%.1f", m.Rating)).FontSize(fsBody).FontWeight(wMedium).
					TextColor(color).FontFeatures("tnum").SingleLine()
			})
		}
		if m.Badge != "" {
			dot()
			ui.Row(c).Height(18).PaddingX(5).Radius(4).Border(1, faint).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Badge).FontSize(10.5).FontWeight(wSemibold).TextColor(color).SingleLine()
			})
		}
	})
}

// runtimeText says how long something runs: "1 小时 32 分钟", "45 分钟".
func runtimeText(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m >= 60 {
		return fmt.Sprintf(str.HoursMinutesFmt, m/60, m%60)
	}
	return fmt.Sprintf(str.MinutesFmt, max(m, 1))
}

// States.

// emptySpec is what a page or a part of one says while it has nothing to
// show.
type emptySpec struct {
	Icon     *ui.SVG
	Title    string
	Message  string
	Action   string // a button's label, "" for none
	OnAction func()
}

// emptyState is a quiet message in the middle of where content would be.
func (a *app) emptyState(c *ui.Context, e emptySpec) ui.Element {
	p := a.pal
	return ui.Column(c.Key("empty")).AlignSelf(ui.Center).AlignItems(ui.Center).MaxWidth(380).
		Padding(48, 16).Gap(8).Children(func() {
		if e.Icon != nil {
			ui.Box(c).Size(64, 64).Radius(32).Center().Background(p.fill).Margin(0, 0, 8, 0).Children(func() {
				ui.Icon(c, e.Icon).Size(28, 28).TextColor(p.text2)
			})
		}
		ui.Text(c, e.Title).FontSize(17).FontWeight(wSemibold).TextColor(p.text).TextAlign(ui.Center).Balance()
		if e.Message != "" {
			ui.Text(c, e.Message).FontSize(fsBody).TextColor(p.text2).TextAlign(ui.Center).LineHeight(1.45).Balance()
		}
		if e.Action != "" && e.OnAction != nil {
			a.pillButton(c, nil, e.Action, pillSecondary).Margin(10, 0, 0, 0).OnClick(e.OnAction)
		}
	})
}

// errorState says what failed, with a button to try again.
func (a *app) errorState(c *ui.Context, message string, retry func()) ui.Element {
	e := emptySpec{Icon: iconAlert, Title: str.ErrorTitle, Message: message}
	if retry != nil {
		e.Action, e.OnAction = str.Retry, retry
	}
	return a.emptyState(c, e)
}

// loadingState is a spinner in the middle of where content will be. It
// waits a moment before it shows, so that a quick load shows nothing.
func (a *app) loadingState(c *ui.Context) ui.Element {
	p := a.pal
	box := ui.Column(c.Key("loading")).AlignSelf(ui.Center).AlignItems(ui.Center).Padding(64, 16).Gap(10).
		Label(str.Loading).Busy(true)
	since := ui.Local(box, "since", func() time.Time { return c.Now() })
	const wait = 400 * time.Millisecond
	shown := c.Now().Sub(*since) >= wait
	if !shown {
		c.After(wait - c.Now().Sub(*since))
	}
	box.Opacity(box.AnimateTo("shown", b2f(shown), medium))
	box.Children(func() {
		ui.Spinner(c).Size(22, 22)
		ui.Text(c, str.Loading).FontSize(fsCaption).TextColor(p.text2)
	})
	return box
}

// Skeletons: gray blocks in the shape of what loads, which it takes the
// place of without moving anything, and over them a light band sweeping,
// across all the blocks of the window in step.

type shimmer struct {
	base, sheen ui.Color
	span        float32 // the width the band crosses: the window's
	still       bool    // less motion asked: no band
}

const (
	shimmerPeriod = 1700 * time.Millisecond
	shimmerSweep  = 1200 * time.Millisecond // the band crosses, then rests
	shimmerBand   = 280                     // DIPs
)

func (a *app) shimmer(c *ui.Context) shimmer {
	w, _ := c.Size()
	return shimmer{base: a.pal.skeleton, sheen: a.pal.sheen, span: w, still: c.Preferences().ReduceMotion}
}

// paint fills r as a block of the skeleton. It only paints: the band moves
// without building the view again.
func (s shimmer) paint(p *ui.Painter, r ui.Rect, radius float32) {
	if s.still || r.W <= 0 {
		p.Fill(r, s.base, radius)
		return
	}
	at := time.Duration(p.Now().UnixNano() % int64(shimmerPeriod))
	if at >= shimmerSweep {
		p.Fill(r, s.base, radius)
		p.After(shimmerPeriod - at)
		return
	}
	p.AnimationFrame()
	x := -shimmerBand/2 + float32(at)/float32(shimmerSweep)*(s.span+shimmerBand)
	if x+shimmerBand/2 < r.X || x-shimmerBand/2 > r.X+r.W {
		p.Fill(r, s.base, radius)
		return
	}
	along := func(x float32) float32 { return (x - r.X) / r.W }
	p.FillGradient(r, ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
		{Color: s.base, At: along(x - shimmerBand/2)},
		{Color: s.base.MixOklab(s.sheen, 1), At: along(x)},
		{Color: s.base, At: along(x + shimmerBand/2)},
	}}, radius)
}

// bone is a block of the skeleton, as large as it is laid out.
func (s shimmer) bone(c *ui.Context, radius float32) ui.Element {
	return ui.Box(c).Shrink(0).Draw(func(p *ui.Painter, r ui.Rect) { s.paint(p, r, radius) })
}

// cardBone is a card's skeleton: its picture and two lines.
func (a *app) cardBone(c *ui.Context, s shimmer, width, aspect, radius float32, i int) {
	ui.Column(c.Key(i)).Width(width).Shrink(0).Children(func() {
		s.bone(c, radius).Width(width).Height(width / aspect)
		ui.Column(c).Height(cardTextH).Padding(cardTextGap+4, 2, 0, 2).Gap(8).Children(func() {
			s.bone(c, 4).Height(10).WidthPercent([]float32{72, 54, 80, 63, 46, 70}[i%6])
			s.bone(c, 4).Height(9).WidthPercent([]float32{38, 46, 30, 42, 34, 40}[i%6])
		})
	})
}

// skeletonShelf stands for a shelf that loads.
func (a *app) skeletonShelf(c *ui.Context, key any, landscape bool) {
	s := a.shimmer(c)
	width, aspect, radius := float32(posterWidth), float32(2.0/3), float32(radiusPoster)
	if landscape {
		width, aspect, radius = landscapeWidth, 16.0/9, radiusLandscape
	}
	ui.Column(c.Key(key)).Shrink(0).Gap(titleGap + 4).Label(str.Loading).Busy(true).Children(func() {
		s.bone(c, 6).Width(150).Height(20).Margin(3, 0, 0, 0)
		ui.Row(c).Margin(0, -gutter).PaddingX(gutter).Gap(cardGap).ClipX().Children(func() {
			for i := range 8 {
				a.cardBone(c, s, width, aspect, radius, i)
			}
		})
	})
}

// skeletonGrid stands for a grid of posters that loads: as many columns as
// the grid would have, rows enough to fill the window.
func (a *app) skeletonGrid(c *ui.Context, minWidth float32) {
	s := a.shimmer(c)
	w, h := c.Size()
	inner := a.contentWidth(w) - 2*gutter
	cols, cardW := gridColumns(inner, minWidth, cardGap)
	rows := max(1, int(h/(cardW*1.5+cardTextH+24))+1)
	ui.Column(c.Key("skeleton-grid")).Gap(24).Label(str.Loading).Busy(true).Children(func() {
		for r := range rows {
			ui.Row(c.Key(r)).Gap(cardGap).Children(func() {
				for i := range cols {
					a.cardBone(c, s, cardW, 2.0/3, radiusPoster, r*cols+i)
				}
			})
		}
	})
}

// gridColumns returns how many cards at least minWidth wide fit across
// width, gap apart, and how wide each is then.
func gridColumns(width, minWidth, gap float32) (int, float32) {
	if minWidth <= 0 {
		minWidth = 1
	}
	cols := max(1, int(math.Floor(float64((width+gap)/(minWidth+gap)))))
	return cols, max(1, (width-gap*float32(cols-1))/float32(cols))
}

// The grid.

// gridSpec is a grid of cards, built only as they show.
type gridSpec struct {
	Count int
	// Landscape makes the cards 16:9 rather than 2:3.
	Landscape bool
	// MinWidth is a card's least width: the grid fits as many columns as
	// it can, sharing the width left among them.
	MinWidth float32
	// Key returns item i's identity, as its ID; Label its name, which
	// typing its first letters finds.
	Key   func(i int) any
	Label func(i int) string
	// Item builds the card of item i, width wide: a posterCard or a
	// landscapeCard with that Width, or 0.
	Item func(i int, width float32)
	// OnNearEnd runs while the last rows are about to show, every frame
	// they are: load the next page there, once (keep a loading flag).
	OnNearEnd func()
	// PadTop is room above the first row, which scrolls with it; the
	// scaffold sets it for its title and Header.
	PadTop float32
	// Header, HeaderHeight and Empty are gridPage's: what goes under the
	// title, as sort and filter controls, its height, and what shows when
	// Count is 0.
	Header       func()
	HeaderHeight float32
	Empty        func()
}

// gridView is a grid's state: its place, and how far it is scrolled.
type gridView struct {
	grid   ui.GridState
	scroll ui.ScrollState
}

const (
	gridCellBorder = 2  // each cell's clear border, which ui.GridView draws
	gridCellGap    = 8  // ui.GridView's gap between cells (the theme's Space(2))
	gridCardInset  = 2  // inside a cell, so that cards stand cardGap apart
	gridRowGap     = 14 // between rows, beyond the cells' own room
)

// posterGrid is a ui.GridView of cards in the content's gutters, the
// columns following the width, that tells OnNearEnd as its last rows come
// into view.
func (a *app) posterGrid(c *ui.Context, v *gridView, g gridSpec) ui.Element {
	minW := g.MinWidth
	if minW <= 0 {
		minW = 150
		if g.Landscape {
			minW = 240
		}
	}
	// The cells' width: as the grid lays them out, from its width in the
	// last frame, or the window's at first.
	edge := float32(gutter - gridCellBorder - gridCardInset)
	width := v.grid.Bounds(c).W
	if width <= 0 {
		w, _ := c.Size()
		width = a.contentWidth(w)
	}
	inner := width - 2*edge
	cols := max(1, int(math.Floor(float64((inner+gridCellGap)/(minW+2*gridCellBorder+gridCardInset*2+gridCellGap)))))
	cell := (inner - gridCellGap*float32(cols-1)) / float32(cols)
	cardW := cell - 2*gridCellBorder - 2*gridCardInset
	aspect := float32(2.0 / 3)
	if g.Landscape {
		aspect = 16.0 / 9
	}
	cellH := float32(math.Ceil(float64(cardW/aspect+cardTextH))) + 2*gridCellBorder
	v.grid.Key = g.Key
	v.grid.Label = g.Label
	last := -1
	grid := ui.GridView(c, &v.grid, g.Count, minW+2*gridCellBorder+2*gridCardInset, cellH, func(i int) {
		last = max(last, i)
		ui.Column(c).Grow(1).PaddingX(gridCardInset).Children(func() { g.Item(i, cardW) })
	})
	grid.TrackScroll(&v.scroll).Padding(g.PadTop, edge, pageBottom, edge).Gap(gridRowGap)
	// The rows built reach a little beyond those in view: within two rows
	// of the end, the next page is due.
	if g.OnNearEnd != nil && g.Count > 0 && last >= g.Count-1-2*cols {
		g.OnNearEnd()
	}
	return grid
}

// joinNonEmpty joins the parts that say something.
func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
