package app

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
)

// The gallery shows every part of the kit with real artwork from
// Jellyfin's public demo server, fetched through the app's image loader,
// at pathGallery, with EMBY_DEV=1 only. The sample data lives here alone,
// and nothing else shows it.

const demoServer = "https://demo.jellyfin.org/stable"

// demoImage is the URL of a picture of an item of the demo server: kind is
// "Primary" (a poster, or an episode's still), "Backdrop" or "Thumb".
func demoImage(id, kind string, px int) string {
	return fmt.Sprintf("%s/Items/%s/Images/%s?maxWidth=%d", demoServer, id, kind, px)
}

// sampleItem is an item of the demo server, as the gallery shows it.
type sampleItem struct {
	ID, Name string
	Series   bool
	Year     int
	Rating   float64
	Official string
	Genres   []string
	Runtime  time.Duration
	Backdrop bool
}

var sampleItems = []sampleItem{
	{ID: "5e6e8380563c5211106652362c5c6843", Name: "Night of the Living Dead", Year: 1968, Rating: 7.6, Official: "NR", Genres: []string{"恐怖", "惊悚"}, Runtime: 95 * time.Minute, Backdrop: true},
	{ID: "c002f0f88ee970cce7f138bbc925de64", Name: "Dracula", Year: 1931, Rating: 7.2, Official: "NR", Genres: []string{"恐怖"}, Runtime: 74 * time.Minute, Backdrop: true},
	{ID: "9bd5926326d651153928e0d11c6ee7cb", Name: "Pioneer One", Series: true, Year: 2010, Rating: 6.2, Official: "NR", Genres: []string{"科幻", "剧情"}, Backdrop: true},
	{ID: "1056b7d34f0fa0b8ce635428e8b27f37", Name: "Caminandes: Gran Dillama", Year: 2013, Rating: 7.3, Genres: []string{"动画", "喜剧"}, Runtime: 2*time.Minute + 26*time.Second, Backdrop: true},
	{ID: "e16725e2c1c4367ec596cf93c38bbe4f", Name: "Jungle Book", Year: 1942, Rating: 6.3, Official: "NR", Genres: []string{"冒险", "奇幻"}, Runtime: 105 * time.Minute, Backdrop: true},
	{ID: "b07f6514a3e3aa7d8c273458c9cd4c82", Name: "King Lear", Year: 1953, Rating: 8, Genres: []string{"历史", "剧情"}, Runtime: 82 * time.Minute},
	{ID: "70853c7dde5e5759b151cd76876ab6c2", Name: "Sahara", Year: 1943, Rating: 7.2, Genres: []string{"冒险", "战争"}, Runtime: 98 * time.Minute, Backdrop: true},
	{ID: "c541e7545e58b28cd4d0da7aac89d7e8", Name: "Caminandes: Llama Drama", Year: 2013, Rating: 6.9, Genres: []string{"动画", "家庭"}, Runtime: 90 * time.Second, Backdrop: true},
	{ID: "b74b4d5de01b4e1a5ce43511c3684ad8", Name: "The Great St. Louis Bank Robbery", Year: 1959, Rating: 6.1, Official: "NR", Genres: []string{"犯罪", "惊悚"}, Runtime: 89 * time.Minute, Backdrop: true},
	{ID: "788ae1cdf3816e3441e29dd3a96ddfe1", Name: "The Great Train Robbery", Year: 1903, Rating: 7.0, Genres: []string{"西部", "犯罪"}, Runtime: 12 * time.Minute, Backdrop: true},
	{ID: "c7eb95d14156c803611cf4a78ac469f6", Name: "Caminandes: Llamigos", Year: 2016, Rating: 6.7, Genres: []string{"动画", "喜剧"}, Runtime: 150 * time.Second, Backdrop: true},
	{ID: "edb39341c5039551a5157e51fe4a3364", Name: "The Boy in the Plastic Bubble", Year: 1976, Rating: 5.7, Official: "PG", Genres: []string{"电视电影", "剧情"}, Runtime: 93 * time.Minute, Backdrop: true},
}

// sampleEpisodes are Pioneer One's first season.
var sampleEpisodes = []struct {
	ID, Name string
	Episode  int
	Progress float64
}{
	{"f717d85df782816c2ab75423e32f740b", "Earthfall", 1, 0.62},
	{"b80159226d16576d4c0613750e27c072", "The Man From Mars", 2, 0.18},
	{"173a94d516c79947da16ba9b1b84a4e0", "Alone in the Night", 3, 0.84},
	{"99b215aae1784e8d3ada93001ebe686f", "Triangular Diplomacy", 4, 0.35},
	{"0a11d3edf0c96febf345553f3cd699dc", "Sea Change", 5, 0},
	{"a8f85ca6627e583fdc47846d0376c36f", "War of the World", 6, 0},
}

// galleryState is what the gallery's controls change.
type galleryState struct {
	favorite map[string]bool
	played   map[string]bool
}

func (g *galleryState) init() {
	if g.favorite == nil {
		g.favorite = map[string]bool{sampleItems[1].ID: true, sampleItems[5].ID: true}
		g.played = map[string]bool{sampleItems[4].ID: true, sampleItems[9].ID: true}
	}
}

// sampleMenu is a sample card's context menu, as DESIGN.md §4.9 lists it.
func (a *app) sampleMenu(id string) func(m *ui.Menu) {
	g := &a.gallery
	return func(m *ui.Menu) {
		if m.Item(str.Play).Chosen() {
			a.toast = str.Play
		}
		m.Separator()
		played := str.MarkPlayed
		if g.played[id] {
			played = str.MarkUnplayed
		}
		if m.Item(played).Chosen() {
			g.played[id] = !g.played[id]
		}
		fav := str.AddFavorite
		if g.favorite[id] {
			fav = str.RemoveFavorite
		}
		if m.Item(fav).Chosen() {
			g.favorite[id] = !g.favorite[id]
		}
	}
}

// samplePoster is the poster card of sample item it, width wide.
func (a *app) samplePoster(c *ui.Context, it sampleItem, key any, width float32) {
	g := &a.gallery
	s := cardSpec{
		Key:      key,
		Title:    it.Name,
		Subtitle: fmt.Sprint(it.Year),
		Image:    demoImage(it.ID, "Primary", a.imgPx(max(width, posterWidth))),
		Fallback: iconFilm,
		Width:    width,
		Played:   g.played[it.ID],
		Favorite: g.favorite[it.ID],
		OnOpen:   func() { a.navigate(pathItem(it.ID)) },
		OnPlay:   func() { a.toast = str.Play + " " + it.Name },
		Menu:     a.sampleMenu(it.ID),
	}
	if it.Series {
		s.Fallback, s.Unplayed, s.Subtitle = iconTV, 4, fmt.Sprintf("%d · 1 季", it.Year)
	}
	a.posterCard(c, s)
}

func (a *app) galleryPage(c *ui.Context, r *ui.Route) {
	a.gallery.init()
	p := a.pal
	mode := func(m *ui.Menu) {
		for _, o := range []struct{ mode, label string }{
			{themeSystem, "跟随系统"}, {themeLight, "浅色"}, {themeDark, "深色"},
		} {
			if m.Item(o.label).Radio(a.settings.ThemeMode == o.mode).Chosen() {
				a.setThemeMode(o.mode)
			}
		}
	}
	spec := pageSpec{
		Title:   str.Gallery,
		Eyebrow: str.GalleryEyebrow,
		Actions: []barAction{
			{Icon: iconRefresh, Label: str.MenuRefresh, Do: a.refresh},
			{Icon: iconMore, Label: "外观", Menu: mode},
		},
	}
	a.scrollPage(c, r, spec, func() {
		ui.Column(c).Gap(shelfGap).Children(func() {
			a.galleryHero(c)

			// Continue watching: 16:9 cards with progress.
			a.shelf(c, shelfSpec{
				Key: "continue", Title: "继续观看", OnSeeAll: func() { a.toast = str.SeeAll },
				Count: len(sampleEpisodes) + 3, ItemWidth: landscapeWidth, ArtHeight: landscapeWidth * 9 / 16,
				Item: func(i int) {
					if i < len(sampleEpisodes) {
						ep := sampleEpisodes[i]
						a.landscapeCard(c, cardSpec{
							Key: ep.ID, Title: ep.Name, Subtitle: "Pioneer One · " + fmt.Sprintf("第 1 季 第 %d 集", ep.Episode),
							Image: demoImage(ep.ID, "Primary", a.imgPx(landscapeWidth)), Fallback: iconTV,
							Width: landscapeWidth, Progress: ep.Progress,
							OnOpen: func() { a.navigate(pathItem(ep.ID)) }, OnPlay: func() { a.toast = str.Resume },
							Menu: a.sampleMenu(ep.ID),
						})
						return
					}
					it := sampleItems[[]int{0, 3, 6}[i-len(sampleEpisodes)]]
					a.landscapeCard(c, cardSpec{
						Key: "bd" + it.ID, Title: it.Name, Subtitle: runtimeText(it.Runtime),
						Image: demoImage(it.ID, "Backdrop", a.imgPx(landscapeWidth)), Fallback: iconFilm,
						Width: landscapeWidth, Progress: 0.4,
						OnOpen: func() { a.navigate(pathItem(it.ID)) }, OnPlay: func() { a.toast = str.Resume },
						Menu: a.sampleMenu(it.ID),
					})
				},
			})

			// Recently added: posters with their badges.
			a.shelf(c, shelfSpec{
				Key: "recent", Title: "最近添加", OnSeeAll: func() { a.toast = str.SeeAll },
				Count: len(sampleItems), ItemWidth: posterWidth, ArtHeight: posterWidth * 3 / 2,
				Item: func(i int) { a.samplePoster(c, sampleItems[i], sampleItems[i].ID, posterWidth) },
			})

			a.gallerySection(c, "按钮与标签", func() {
				ui.Row(c).Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
					a.pillButton(c, iconPlay, str.Play, pillPrimary).OnClick(func() { a.toast = str.Play })
					a.pillButton(c, iconRefresh, str.PlayFromStart, pillSecondary)
					fav := g(a).favorite[sampleItems[0].ID]
					icon, label := iconHeart, str.AddFavorite
					if fav {
						icon, label = iconHeartFill, str.RemoveFavorite
					}
					a.pillButton(c, icon, label, pillPlain).OnClick(func() { g(a).favorite[sampleItems[0].ID] = !fav })
					a.pillButton(c, nil, "无图标", pillPlain)
					ui.Box(c).Width(8)
					a.iconButton(c, iconCheckCircle, str.MarkPlayed, 32)
					a.iconButton(c, iconInfo, "信息", 32)
					a.iconButton(c, iconMore, str.More, 32).Menu(a.sampleMenu(sampleItems[0].ID))
					a.iconButton(c, iconX, "关闭", 28)
				})
				ui.Row(c).Gap(8).Wrap().Margin(16, 0, 0, 0).Children(func() {
					for _, genre := range []string{"恐怖", "惊悚", "剧情", "动画", "科幻", "冒险", "家庭"} {
						a.chip(c, genre, true).OnClick(func() { a.toast = genre })
					}
				})
			})

			a.gallerySection(c, "元信息", func() {
				ui.Column(c).Gap(10).Children(func() {
					it := sampleItems[0]
					a.metaRow(c, metaSpec{Parts: []string{fmt.Sprint(it.Year), runtimeText(it.Runtime), joinNonEmpty(" / ", it.Genres...)}, Rating: it.Rating, Badge: it.Official})
					a.metaRow(c, metaSpec{Parts: []string{"2010", "1 季", "6 集"}, Rating: 6.2})
					a.metaRow(c, metaSpec{Parts: []string{fmt.Sprintf(str.EpisodeFmt, 1, 3), runtimeText(42 * time.Minute)}})
				})
			})

			a.gallerySection(c, "状态", func() {
				ui.Row(c).Gap(cardGap).AlignItems(ui.Stretch).Wrap().Children(func() {
					panel := func(key string, fn func()) {
						ui.Column(c.Key(key)).Grow(1).Basis(240).MinWidth(240).Height(280).Radius(radiusPanel).
							Border(1, p.hairline).Justify(ui.Center).Children(fn)
					}
					panel("empty", func() {
						a.emptyState(c, emptySpec{Icon: iconHeart, Title: "还没有收藏", Message: "在卡片上右键选择“收藏”，喜欢的影片会出现在这里。", Action: "浏览媒体库", OnAction: func() { a.navigate(pathHome) }})
					})
					panel("error", func() {
						a.errorState(c, "无法连接到服务器，请检查网络后重试。", func() { a.toast = str.Retry })
					})
					panel("loading", func() { a.loadingState(c) })
				})
			})

			a.skeletonShelf(c, "skeleton-landscape", true)
			a.skeletonShelf(c, "skeleton-poster", false)

			a.gallerySection(c, "图标", func() {
				ui.Row(c).Wrap().GapX(4).GapY(4).Children(func() {
					for _, ic := range allIcons {
						ui.Column(c.Key(ic.name)).Width(96).Height(76).Radius(10).AlignItems(ui.Center).
							Justify(ui.Center).Gap(8).Background(p.fill.Alpha(0.5)).Children(func() {
							ui.Icon(c, ic.svg).Size(22, 22).TextColor(p.text)
							ui.Text(c, ic.name).FontSize(10.5).TextColor(p.text2).SingleLine()
						})
					}
				})
			})
		})
	})
}

func g(a *app) *galleryState { return &a.gallery }

// gallerySection is a titled part of the gallery.
func (a *app) gallerySection(c *ui.Context, title string, body func()) {
	ui.Column(c.Key(title)).Gap(titleGap).Children(func() {
		a.sectionTitle(c, title, nil)
		body()
	})
}

// galleryHero is a banner of a backdrop with text over it: the scrim and
// the colors of text over pictures, as Home's and an item's headers use.
func (a *app) galleryHero(c *ui.Context) {
	p := a.pal
	it := sampleItems[0]
	w, _ := c.Size()
	bannerW := max(a.contentWidth(w)-2*gutter, 400)
	ui.Box(c.Key("hero")).Height(300).Radius(radiusPanel).Shadow(0, 2, 10, 0, p.shadow).Children(func() {
		a.artwork(c, demoImage(it.ID, "Backdrop", a.imgPx(bannerW)), iconFilm, radiusPanel)
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radiusPanel).
			LinearGradient(ui.LinearGradient{Angle: 90, Stops: []ui.GradientStop{
				{Color: p.scrim, At: 0}, {Color: p.scrim.Alpha(0.35), At: 0.55}, {Color: ui.Transparent, At: 0.85},
			}})
		ui.Column(c).Absolute().Left(32).Bottom(30).MaxWidth(460).Gap(8).Children(func() {
			ui.Text(c, "精选").FontSize(fsSmall).FontWeight(wBold).TextColor(p.onImage2).LetterSpacing(0.6)
			ui.Text(c, it.Name).FontSize(30).FontWeight(wBold).TextColor(p.onImage).LetterSpacing(-0.5).
				MaxLines(2).Balance().TextShadow(0, 1, 6, ui.RGBA(0, 0, 0, 0.3))
			a.metaRow(c, metaSpec{Parts: []string{fmt.Sprint(it.Year), runtimeText(it.Runtime), joinNonEmpty(" / ", it.Genres...)}, Rating: it.Rating, Badge: it.Official, OnImage: true})
			ui.Text(c, "一群陌生人被困在宾夕法尼亚乡间的一座农舍里，屋外是越聚越多的活死人。").FontSize(fsBody).
				TextColor(p.onImage2).LineHeight(1.45).MaxLines(2)
			ui.Row(c).Gap(10).Margin(8, 0, 0, 0).Children(func() {
				a.pillButton(c, iconPlay, str.Play, pillOnImage).OnClick(func() { a.toast = str.Play })
				a.pillButton(c, iconInfo, "详细信息", pillGlass).OnClick(func() { a.navigate(pathItem(it.ID)) })
			})
		})
	})
}

// The gallery's screenshots, with EMBY_DEV=1 only.
func init() {
	addShots(80,
		shotStep{Do: func(a *app) {
			if a.dev {
				a.navigate(pathGallery)
			}
		}, Wait: 4 * time.Second},
		shotStep{Name: "gallery-top", If: func(a *app) bool { return a.dev }},
		shotStep{Do: func(a *app) {
			a.scrollPageTo(300)
			a.forceHover = sampleItems[2].ID
		}, Wait: time.Second},
		shotStep{Name: "gallery-hover", If: func(a *app) bool { return a.dev }},
		shotStep{Do: func(a *app) {
			a.forceHover = nil
			a.scrollPageTo(1900)
		}},
		shotStep{Name: "gallery-states", If: func(a *app) bool { return a.dev }},
	)
}
