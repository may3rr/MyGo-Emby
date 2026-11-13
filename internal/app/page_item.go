package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"mygo-emby/internal/emby"
)

// An item's page (DESIGN.md §4.4): its backdrop across the top, fading
// into the page; its poster beside its name, facts, genres and buttons to
// play it, from its start too, add it to the favorites and mark it
// watched; the version, audio track and subtitles to play it with; its
// overview; a series' seasons and their episodes; its cast; and what is
// like it.

const similarLimit = 16

// detailState is an item loaded for its page, with what goes with it.
type detailState struct {
	item     *emby.Item
	next     *emby.Item // a series' episode to play: the next one up
	similar  []emby.Item
	seasons  []emby.Item
	season   string // the season whose episodes show
	episodes map[string]*episodeList

	// The version, audio track and subtitles chosen; "" and nil for the
	// server's and the player's choice.
	source string
	audio  *int
	sub    *int

	started bool
	loading bool
	err     error
	reload  int
	gen     gen
}

// episodeList is a season's episodes.
type episodeList struct {
	items   []emby.Item
	loading bool
	loaded  bool
	err     error
}

// detailData is what loading an item's page brings.
type detailData struct {
	item    *emby.Item
	next    *emby.Item
	similar []emby.Item
	seasons []emby.Item
}

// fetchDetail asks for an item and, at once, what is like it and, for a
// series, its seasons and the episode up next. Only the item must come:
// the rest is left out where it fails.
func fetchDetail(ctx context.Context, cl *emby.Client, id string) (detailData, error) {
	it, err := cl.Item(ctx, id)
	if err != nil {
		return detailData{}, err
	}
	d := detailData{item: it}
	var wg sync.WaitGroup
	wg.Go(func() {
		if items, err := cl.Similar(ctx, id, similarLimit); err == nil {
			d.similar = items
		}
	})
	if it.Type == emby.TypeSeries {
		wg.Go(func() {
			if items, err := cl.Seasons(ctx, id); err == nil {
				d.seasons = items
			}
		})
		wg.Go(func() {
			if items, err := cl.NextUp(ctx, id, 1); err == nil && len(items) > 0 {
				d.next = &items[0]
			}
		})
	}
	wg.Wait()
	return d, nil
}

// detail returns the page state of the item with the ID id, loading it
// when it is not, or again after ⌘R.
func (a *app) detail(id string) *detailState {
	d := a.details[id]
	if d == nil {
		d = &detailState{episodes: map[string]*episodeList{}}
		a.details[id] = d
	}
	if a.client != nil && !d.loading && (!d.started || d.reload != a.reload) {
		a.loadDetail(d, id)
	}
	return d
}

func (a *app) loadDetail(d *detailState, id string) {
	d.started, d.loading, d.reload = true, true, a.reload
	n := d.gen.next()
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) (detailData, error) {
		return fetchDetail(ctx, cl, id)
	}, func(r detailData, err error) {
		if a.epoch != epoch || !d.gen.is(n) {
			return
		}
		d.loading = false
		if err != nil {
			if d.item != nil {
				a.toast = a.errText(err)
			}
			d.err = err
			return
		}
		d.item, d.next, d.similar, d.seasons, d.err = r.item, r.next, r.similar, r.seasons, nil
		d.episodes = map[string]*episodeList{}
		if !slices.ContainsFunc(d.seasons, func(s emby.Item) bool { return s.ID == d.season }) {
			d.season = defaultSeason(d.seasons, d.next)
		}
	})
}

// defaultSeason is the season a series' page opens on: that of the
// episode up next, else the first that is not the specials.
func defaultSeason(seasons []emby.Item, next *emby.Item) string {
	if next != nil && slices.ContainsFunc(seasons, func(s emby.Item) bool { return s.ID == next.SeasonID }) {
		return next.SeasonID
	}
	for _, s := range seasons {
		if n, ok := s.Season(); ok && n > 0 {
			return s.ID
		}
	}
	if len(seasons) > 0 {
		return seasons[0].ID
	}
	return ""
}

// episodesOf returns a season's episodes, loading them when they are not.
func (a *app) episodesOf(d *detailState, seriesID, seasonID string) *episodeList {
	l := d.episodes[seasonID]
	if l == nil {
		l = &episodeList{}
		d.episodes[seasonID] = l
	}
	if a.client == nil || l.loading || l.loaded || l.err != nil {
		return l
	}
	l.loading = true
	epoch := a.epoch
	load(a, func(ctx context.Context, cl *emby.Client) ([]emby.Item, error) {
		return cl.Episodes(ctx, seriesID, seasonID)
	}, func(items []emby.Item, err error) {
		if a.epoch != epoch || d.episodes[seasonID] != l {
			return // reloaded meanwhile
		}
		l.loading = false
		l.items, l.err, l.loaded = items, err, err == nil
	})
	return l
}

const (
	detailBackdropH = 460
	detailPosterW   = 220
)

func (a *app) itemPage(c *ui.Context, r *ui.Route, id string) {
	d := a.detail(id)
	it := d.item
	title, backdrop := "", ""
	w, _ := c.Size()
	if it != nil {
		title = it.Name
		backdrop = a.itemImage(it, emby.RoleBackdrop, a.contentWidth(w))
	}
	// The page starts at the window's top, under the bar, with the backdrop
	// or, without one, room for the bar; the bar shows the name once the
	// header scrolls away.
	spec := pageSpec{Title: title, Hero: true, HeroHeight: detailBackdropH}
	if backdrop == "" {
		spec.HeroHeight = 300
	}
	if it != nil {
		spec.Actions = []barAction{{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh}}
	}
	a.scrollPage(c, r, spec, func() {
		switch {
		case it == nil && d.err != nil:
			ui.Box(c).Height(barH(c))
			a.errorState(c, a.errText(d.err), func() { a.loadDetail(d, id) })
		case it == nil:
			ui.Box(c).Height(barH(c))
			a.loadingState(c)
		default:
			a.itemHeader(c, d, backdrop)
			ui.Column(c.Key("sections")).Gap(shelfGap+8).Margin(36, 0, 0, 0).Children(func() {
				if it.Type == emby.TypeSeries && len(d.seasons) > 0 {
					a.seasonsSection(c, d)
				}
				a.castShelf(c, it)
				a.itemShelf(c, "similar", str.Similar, d.similar, "", false, false, nil)
			})
		}
	})
}

// itemHeader is the top of an item's page.
func (a *app) itemHeader(c *ui.Context, d *detailState, backdrop string) {
	p := a.pal
	it := d.item
	if backdrop == "" {
		ui.Box(c.Key("top")).Height(barH(c) + 24)
	} else {
		ui.Box(c.Key("backdrop")).Height(detailBackdropH).Margin(0, -gutter, 0, -gutter).Children(func() {
			a.artwork(c, backdrop, nil, 0)
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				LinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
					{Color: p.bg.Alpha(0), At: 0.3}, {Color: p.bg.Alpha(0.78), At: 0.78}, {Color: p.bg, At: 1},
				}})
		})
	}
	episode := it.Type == emby.TypeEpisode
	posterW, aspect, radius := float32(detailPosterW), float32(2.0/3), float32(radiusPoster)
	role := emby.RolePoster
	if episode {
		posterW, aspect, radius, role = 340, 16.0/9, radiusLandscape, emby.RolePrimary
	}
	row := ui.Row(c.Key("header")).Gap(32).AlignItems(ui.End)
	if backdrop != "" {
		row.Margin(-posterW/aspect*0.55, 0, 0, 0)
	}
	row.Children(func() {
		ui.Box(c.Key("poster")).Width(posterW).AspectRatio(aspect).Radius(radius).Shrink(0).
			Shadow(0, 10, 30, -6, p.shadowLift).Children(func() {
			a.artworkTitled(c, a.itemImage(it, role, posterW), itemIcon(it), it.Name, radius)
		})
		ui.Column(c.Key("facts")).Grow(1).Shrink(1).MinWidth(0).Gap(10).Padding(0, 0, 4, 0).Children(func() {
			if episode && it.SeriesName != "" {
				a.seriesLink(c, it)
			}
			ui.Text(c, it.Name).FontSize(30).FontWeight(wBold).TextColor(p.text).LetterSpacing(-0.5).
				MaxLines(2).Balance()
			parts := []string{it.YearText()}
			if it.RunTimeTicks > 0 && it.Type != emby.TypeSeries {
				parts = append(parts, runtimeText(it.Runtime()))
			}
			if n := len(d.seasons); it.Type == emby.TypeSeries && n > 0 {
				parts = append(parts, fmt.Sprintf("%d 季", n))
			}
			a.metaRow(c, metaSpec{Parts: parts, Rating: it.CommunityRating, Badge: it.OfficialRating})
			if len(it.Genres) > 0 {
				ui.Row(c.Key("genres")).Gap(6).Wrap().Children(func() {
					for _, g := range it.Genres[:min(len(it.Genres), 5)] {
						a.chip(c, g, false)
					}
				})
			}
			a.itemButtons(c, d)
			a.trackChoices(c, d)
		})
	})
	ui.Column(c.Key("about")).Gap(10).MaxWidth(780).Margin(28, 0, 0, 0).Children(func() {
		if tl := it.Tagline(); tl != "" {
			ui.Text(c, tl).FontSize(15).FontWeight(wSemibold).TextColor(p.text)
		}
		if it.Overview != "" {
			ui.Text(c.Key("overview"), it.Overview).FontSize(14).TextColor(p.text).LineHeight(1.6)
		}
		if ds := it.Directors(); len(ds) > 0 {
			a.factLine(c, str.Directors, strings.Join(ds, "、"))
		}
		if ss := it.StudioNames(); len(ss) > 0 {
			a.factLine(c, str.Studios, strings.Join(ss[:min(len(ss), 3)], "、"))
		}
	})
}

// seriesLink is an episode's series and number, opening the series.
func (a *app) seriesLink(c *ui.Context, it *emby.Item) {
	p := a.pal
	b := ui.ButtonBase(c.Key("series")).AlignSelf(ui.Start).Gap(4).AlignItems(ui.Center).Radius(6).
		Label(str.SeriesLink + " " + it.SeriesName).Tooltip(str.SeriesLink)
	color := p.text2
	if b.Hovered() {
		color = p.text
	}
	b.Children(func() {
		ui.Text(c, joinNonEmpty(" · ", it.SeriesName, episodeText(it))).FontSize(fsBody).FontWeight(wSemibold).
			TextColor(color).SingleLine()
		ui.Icon(c, iconChevronRight).Size(14, 14).TextColor(color)
	})
	if it.SeriesID != "" {
		id := it.SeriesID
		b.OnClick(func() { a.navigate(pathItem(id)) })
	}
}

// factLine is a labeled fact under the overview: "导演  张艺谋".
func (a *app) factLine(c *ui.Context, label, value string) {
	p := a.pal
	ui.Row(c.Key(label)).Gap(10).Children(func() {
		ui.Text(c, label).FontSize(fsBody).TextColor(p.text2).Shrink(0)
		ui.Text(c, value).FontSize(fsBody).TextColor(p.text).Shrink(1).MinWidth(0)
	})
}

// playTarget is what an item's page plays: the item, or a series' episode
// up next, and how the page's main button names it.
func playTarget(d *detailState) (*emby.Item, string) {
	it := d.item
	if it.Type == emby.TypeSeries {
		if n := d.next; n != nil {
			if n.CanResume() {
				return n, fmt.Sprintf(str.ResumeEpisodeFmt, episodeText(n))
			}
			return n, fmt.Sprintf(str.PlayEpisodeFmt, episodeText(n))
		}
		return it, str.Play
	}
	if it.CanResume() {
		if left := it.Remaining(); left > 0 {
			return it, fmt.Sprintf(str.ResumeLeftFmt, runtimeText(left))
		}
		return it, str.Resume
	}
	return it, str.Play
}

// itemButtons are the page's actions: play (or resume), play from the
// start, favorite, watched.
func (a *app) itemButtons(c *ui.Context, d *detailState) {
	it := d.item
	target, label := playTarget(d)
	opts := playOptions{MediaSourceID: d.source, AudioIndex: d.audio, SubtitleIndex: d.sub}
	ui.Row(c.Key("buttons")).Gap(10).Margin(6, 0, 0, 0).Wrap().Children(func() {
		if target.IsPlayable() || target.Type == emby.TypeSeries {
			t := *target
			a.pillButton(c, iconPlay, label, pillPrimary).OnClick(func() { a.play(&t, opts) })
			if t.CanResume() {
				from := opts
				from.FromStart = true
				a.pillButton(c, iconRefresh, str.PlayFromStart, pillSecondary).OnClick(func() { a.play(&t, from) })
			}
		}
		fav, favIcon, favKind := str.AddFavorite, iconHeart, pillPlain
		if it.IsFavorite() {
			fav, favIcon, favKind = str.Favorited, iconHeartFill, pillSecondary
		}
		a.pillButton(c, favIcon, fav, favKind).OnClick(func() { a.setFavorite(it, !it.IsFavorite()) })
		played, playedKind := str.MarkPlayed, pillPlain
		if it.IsPlayed() {
			played, playedKind = str.PlayedDone, pillSecondary
		}
		a.pillButton(c, iconCheckCircle, played, playedKind).OnClick(func() { a.setPlayed(it, !it.IsPlayed()) })
	})
}

// source returns the version the page plays: the one chosen, else the
// first.
func (d *detailState) chosenSource() *emby.MediaSource {
	srcs := d.item.MediaSources
	for i := range srcs {
		if srcs[i].ID == d.source {
			return &srcs[i]
		}
	}
	if len(srcs) > 0 {
		return &srcs[0]
	}
	return nil
}

// streamLabel names a stream for the menus.
func streamLabel(s emby.MediaStream) string {
	switch {
	case s.DisplayTitle != "":
		return s.DisplayTitle
	case s.Title != "":
		return s.Title
	}
	return joinNonEmpty(" ", s.Language, strings.ToUpper(s.Codec))
}

// trackChoices are the menus of the version, audio track and subtitles to
// play the item with, for what plays and has more than one of them.
func (a *app) trackChoices(c *ui.Context, d *detailState) {
	it := d.item
	if !it.IsPlayable() || len(it.MediaSources) == 0 {
		return
	}
	src := d.chosenSource()
	audios := src.Streams(emby.StreamAudio)
	subs := src.Streams(emby.StreamSubtitle)
	if len(it.MediaSources) < 2 && len(audios) < 2 && len(subs) == 0 {
		return
	}
	ui.Row(c.Key("tracks")).Gap(8).Wrap().Margin(2, 0, 0, 0).Children(func() {
		if len(it.MediaSources) > 1 {
			a.dropdown(c, "version", str.Version, src.DisplayName(), func(m *ui.Menu) {
				for _, s := range it.MediaSources {
					if m.Item(s.DisplayName()).Radio(s.ID == src.ID).Chosen() {
						d.source, d.audio, d.sub = s.ID, nil, nil
					}
				}
			})
		}
		if len(audios) > 1 {
			cur := -1
			if d.audio != nil {
				cur = *d.audio
			} else if src.DefaultAudioStreamIndex != nil {
				cur = *src.DefaultAudioStreamIndex
			}
			value := streamLabel(audios[0])
			for _, s := range audios {
				if s.Index == cur {
					value = streamLabel(s)
				}
			}
			a.dropdown(c, "audio", str.Audio, value, func(m *ui.Menu) {
				for _, s := range audios {
					if m.Item(streamLabel(s)).Radio(s.Index == cur).Chosen() {
						d.audio = emby.Index(s.Index)
					}
				}
			})
		}
		if len(subs) > 0 {
			cur := -1
			if d.sub != nil {
				cur = *d.sub
			} else if src.DefaultSubtitleStreamIndex != nil {
				cur = *src.DefaultSubtitleStreamIndex
			}
			value := str.SubtitlesOff
			for _, s := range subs {
				if s.Index == cur {
					value = streamLabel(s)
				}
			}
			a.dropdown(c, "subtitles", str.Subtitles, value, func(m *ui.Menu) {
				if m.Item(str.SubtitlesOff).Radio(cur < 0).Chosen() {
					d.sub = emby.Index(-1)
				}
				for _, s := range subs {
					if m.Item(streamLabel(s)).Radio(s.Index == cur).Chosen() {
						d.sub = emby.Index(s.Index)
					}
				}
			})
		}
	})
}

// dropdown is a quiet capsule naming a choice and what is chosen, which
// opens the choices: "音轨  中文 AAC 5.1 ⌄".
func (a *app) dropdown(c *ui.Context, key, label, value string, menu func(m *ui.Menu)) ui.Element {
	p := a.pal
	b := ui.ButtonBase(c.Key(key)).Height(28).Padding(0, 10, 0, 12).Gap(6).Radius(14).AlignItems(ui.Center).
		Label(label + "：" + value).MaxWidth(320)
	bg := p.fill
	switch {
	case b.Pressed():
		bg = p.fillHover.MixOklab(black, 0.06)
	case b.Hovered():
		bg = p.fillHover
	}
	b.Background(b.AnimateColor("bg", bg, quick))
	b.Children(func() {
		ui.Text(c, label).FontSize(fsCaption).TextColor(p.text2).SingleLine().Shrink(0)
		ui.Text(c, value).FontSize(fsBody).FontWeight(wMedium).TextColor(p.text).SingleLine().Shrink(1).MinWidth(0)
		ui.Icon(c, iconChevronDown).Size(12, 12).TextColor(p.text3).Shrink(0)
	})
	b.PopoverMenuButton(menu)
	return b
}

// seasonsSection is a series' seasons, to choose among, over the episodes
// of the one chosen.
func (a *app) seasonsSection(c *ui.Context, d *detailState) {
	it := d.item
	ui.Column(c.Key("seasons")).Gap(titleGap).Children(func() {
		if len(d.seasons) > 1 {
			ui.ScrollHorizontal(c.Key("season-tabs")).Scrollbars(false).Margin(0, -gutter).Children(func() {
				ui.Row(c).Gap(8).PaddingX(gutter).Padding(2, gutter).Children(func() {
					for _, s := range d.seasons {
						id := s.ID
						a.filterChip(c, s.Name, s.ID == d.season).OnClick(func() { d.season = id })
					}
				})
			})
		} else {
			a.sectionTitle(c, d.seasons[0].Name, nil)
		}
		eps := a.episodesOf(d, it.ID, d.season)
		switch {
		case eps.err != nil:
			a.errorState(c, a.errText(eps.err), func() { eps.err = nil; a.episodesOf(d, it.ID, d.season) })
		case !eps.loaded:
			a.loadingState(c)
		default:
			ui.Column(c.Key("episodes-"+d.season)).Gap(4).Margin(0, -12).Children(func() {
				for i := range eps.items {
					a.episodeRow(c, &eps.items[i])
				}
			})
		}
	})
}

// episodeRow is an episode in a season's list: its still, with its
// progress, its number and name, its overview and its length.
func (a *app) episodeRow(c *ui.Context, ep *emby.Item) {
	p := a.pal
	item := *ep
	row := ui.ButtonBase(c.Key(ep.ID)).Gap(18).Padding(10, 12).Radius(radiusLandscape).
		AlignItems(ui.Start).Justify(ui.Start).Label(item.Name).FocusRingOffset(-1)
	a.rowBackground(row)
	hovered := row.Hovered() || row.FocusWithin()
	row.Children(func() {
		const thumbW = 224
		ui.Box(c.Key("thumb")).Width(thumbW).AspectRatio(16.0/9).Radius(10).Shrink(0).
			Shadow(0, 1, 4, 0, p.shadow).Children(func() {
			a.artwork(c, a.itemImage(&item, emby.RoleLandscape, thumbW), iconTV, 10)
			if item.CanResume() {
				a.cardProgress(c, item.Progress(), 10)
			}
			if item.IsPlayed() {
				a.playedBadge(c)
			}
			if hovered {
				a.cardPlayButton(c, func() { a.play(&item, playOptions{}) })
			}
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(5).Padding(4, 0, 0, 0).Children(func() {
			name := item.Name
			if n, ok := item.Episode(); ok {
				name = fmt.Sprintf(str.EpisodeNumFmt, n, item.Name)
			}
			ui.Text(c, name).FontSize(14).FontWeight(wSemibold).TextColor(p.text).MaxLines(2)
			var parts []string
			if item.RunTimeTicks > 0 {
				parts = append(parts, runtimeText(item.Runtime()))
			}
			if !item.PremiereDate.IsZero() {
				parts = append(parts, item.PremiereDate.Format("2006-01-02"))
			}
			if len(parts) > 0 {
				ui.Text(c, strings.Join(parts, " · ")).FontSize(fsCaption).TextColor(p.text2).FontFeatures("tnum")
			}
			if item.Overview != "" {
				ui.Text(c, item.Overview).FontSize(fsBody).TextColor(p.text2).LineHeight(1.45).MaxLines(3)
			}
		})
	})
	row.ContextMenu(a.itemMenu(&item))
	row.OnClick(func() { a.navigate(pathItem(item.ID)) })
}

// castShelf is the item's people in a row of portraits, each opening the
// person's page.
func (a *app) castShelf(c *ui.Context, it *emby.Item) {
	people := it.Cast()
	people = append(people, it.Crew()...)
	people = slices.DeleteFunc(people, func(p emby.Person) bool { return p.ID == "" })
	if len(people) > 30 {
		people = people[:30]
	}
	if len(people) == 0 {
		return
	}
	const w, photo = 116, 96
	a.shelf(c, shelfSpec{
		Key: "cast", Title: str.Cast, Count: len(people), ItemWidth: w, ArtHeight: photo,
		Item: func(i int) { a.personCard(c, people[i], w, photo) },
	})
}

// personCard is a person's round portrait, name and role.
func (a *app) personCard(c *ui.Context, person emby.Person, w, photo float32) {
	p := a.pal
	b := ui.ButtonBase(c.Key(person.ID + "\x00" + person.Role)).Width(w).Shrink(0).Column().AlignItems(ui.Center).
		Gap(4).Label(person.Name).FocusRing(false)
	hovered := b.Hovered()
	b.Children(func() {
		pic := ui.Box(c.Key("photo")).Size(photo, photo).Radius(photo/2).Margin(0, 0, 6, 0)
		pose := ui.Pose{}
		if hovered {
			pose.Scale = 1.04
		}
		pic.AnimatePose(pose, spring).Shadow(0, 1, 4, 0, p.shadow)
		if b.FocusVisible() {
			pic.DrawOver(func(pt *ui.Painter, r ui.Rect) {
				pt.FocusRing(r, [4]float32{photo / 2, photo / 2, photo / 2, photo / 2})
			})
		}
		url := ""
		if a.client != nil {
			url = a.client.PersonImageURL(person, emby.ImageOptions{MaxWidth: a.imgPx(photo), Quality: 90})
		}
		pic.Children(func() { a.artwork(c, url, iconUser, photo/2) })
		ui.Text(c, person.Name).FontSize(fsCardTitle).FontWeight(wMedium).TextColor(p.text).SingleLine().
			TextAlign(ui.Center).MaxWidth(w)
		role := person.Role
		if role == "" && person.Type != "Actor" {
			role = person.Type
		}
		ui.Text(c, role).FontSize(fsCaption).TextColor(p.text2).SingleLine().TextAlign(ui.Center).MaxWidth(w)
	})
	id := person.ID
	b.OnClick(func() { a.navigate(pathPerson(id)) })
}
