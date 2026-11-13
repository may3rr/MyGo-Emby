package app

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Type scale, in points, as DESIGN.md sets it. The system font throughout.
const (
	fsLargeTitle = 28   // a page's large title, Bold
	fsTitle      = 20   // a section's title, Semibold
	fsHeadline   = 15   // the bar's title, Semibold
	fsBody       = 13   // running text
	fsCardTitle  = 13   // a card's title, Medium
	fsCaption    = 12   // secondary text, in text2
	fsSmall      = 11   // badges, eyebrows
	fsBadge      = 11.5 // numbers on badges
)

// Font weights.
const (
	wRegular  = 400
	wMedium   = 500
	wSemibold = 600
	wBold     = 700
)

// Spacing and shape, in DIPs.
const (
	gutter          = 32  // the content's left and right padding
	shelfGap        = 28  // between shelves and sections
	cardGap         = 16  // between cards
	titleGap        = 12  // a section's title to its content
	radiusPoster    = 10  // 2:3 cards
	radiusLandscape = 12  // 16:9 cards
	radiusButton    = 8   // square-ish buttons, fields
	radiusPanel     = 16  // panels and sheets
	sidebarWidth    = 232 // DESIGN.md §3
	barHeight       = 52  // the floating bar over the content
	posterWidth     = 160 // a poster card in a shelf
	landscapeWidth  = 288 // a 16:9 card in a shelf
)

// Motion, DESIGN.md §3: 150 to 300 ms. Animations go to their targets at
// once where the desktop asks for less motion.
var (
	quick  = ui.Animation{Duration: 150 * time.Millisecond}
	medium = ui.Animation{Duration: 220 * time.Millisecond}
	fadeIn = ui.Animation{Duration: 300 * time.Millisecond}
	spring = ui.Animation{Spring: ui.SpringOptions{Duration: 280 * time.Millisecond, Bounce: 0.12}}
)

// palette is the app's colors for one appearance, derived from the
// framework's theme, which follows the system (or the user's choice) and the
// system's accent color. Views read it from a.pal, set each frame.
type palette struct {
	dark bool

	bg        ui.Color // the content area, opaque
	sidebar   ui.Color // the sidebar where the window has no vibrancy
	surface   ui.Color // elevated surfaces: panels, popovers
	fill      ui.Color // quiet fills: chips, fields, tracks
	fillHover ui.Color
	hairline  ui.Color // separators and edges
	skeleton  ui.Color // placeholders standing in for what loads
	sheen     ui.Color // the light band sweeping over skeletons
	hover     ui.Color // the wash under a hovered row or button
	press     ui.Color // and while pressed
	selected  ui.Color // the chosen sidebar row

	text  ui.Color // primary text
	text2 ui.Color // secondary text (DESIGN.md's TextMuted)
	text3 ui.Color // tertiary text, placeholders, disabled glyphs

	accent   ui.Color // the system's accent
	onAccent ui.Color // text and glyphs on it

	// Over pictures: text, its secondary tier, the dark scrim that keeps
	// it legible, and the controls floating on them.
	onImage   ui.Color
	onImage2  ui.Color
	scrim     ui.Color
	scrimSoft ui.Color

	shadow      ui.Color // a card at rest
	shadowLift  ui.Color // a card under the pointer
	star        ui.Color // ratings
	favorite    ui.Color // the heart of favorites
	danger      ui.Color
	focusRing   ui.Color
	progress    ui.Color // played part of a progress bar over a picture
	progressBed ui.Color // the rest of it
}

var (
	white = ui.RGB(255, 255, 255)
	black = ui.RGB(0, 0, 0)
)

// paletteFor derives the palette from the window's theme.
func paletteFor(t *ui.Theme) palette {
	accent := t.Accent
	onAccent := t.AccentText
	if accent.A == 0 {
		accent, onAccent = ui.Hex("#0a84ff"), white
	}
	if t.Dark {
		return palette{
			dark:        true,
			bg:          ui.Hex("#141416"),
			sidebar:     ui.Hex("#1c1c1f"),
			surface:     ui.Hex("#1e1e21"),
			fill:        ui.RGBA(118, 118, 128, 0.24),
			fillHover:   ui.RGBA(118, 118, 128, 0.34),
			hairline:    ui.RGBA(255, 255, 255, 0.08),
			skeleton:    ui.RGBA(118, 118, 128, 0.20),
			sheen:       ui.RGBA(150, 150, 160, 0.28),
			hover:       ui.RGBA(255, 255, 255, 0.06),
			press:       ui.RGBA(255, 255, 255, 0.10),
			selected:    ui.RGBA(255, 255, 255, 0.10),
			text:        ui.Hex("#f5f5f7"),
			text2:       ui.RGBA(235, 235, 245, 0.62),
			text3:       ui.RGBA(235, 235, 245, 0.36),
			accent:      accent,
			onAccent:    onAccent,
			onImage:     white,
			onImage2:    ui.RGBA(255, 255, 255, 0.78),
			scrim:       ui.RGBA(0, 0, 0, 0.62),
			scrimSoft:   ui.RGBA(0, 0, 0, 0.32),
			shadow:      ui.RGBA(0, 0, 0, 0.36),
			shadowLift:  ui.RGBA(0, 0, 0, 0.6),
			star:        ui.Hex("#ffb340"),
			favorite:    ui.Hex("#ff4f73"),
			danger:      t.Danger,
			focusRing:   accent.Alpha(0.6),
			progress:    white,
			progressBed: ui.RGBA(255, 255, 255, 0.3),
		}
	}
	return palette{
		bg:          ui.Hex("#ffffff"),
		sidebar:     ui.Hex("#f3f3f5"),
		surface:     ui.Hex("#ffffff"),
		fill:        ui.RGBA(118, 118, 128, 0.12),
		fillHover:   ui.RGBA(118, 118, 128, 0.2),
		hairline:    ui.RGBA(0, 0, 0, 0.08),
		skeleton:    ui.RGBA(118, 118, 128, 0.12),
		sheen:       ui.RGBA(118, 118, 128, 0.05),
		hover:       ui.RGBA(0, 0, 0, 0.045),
		press:       ui.RGBA(0, 0, 0, 0.08),
		selected:    ui.RGBA(0, 0, 0, 0.075),
		text:        ui.Hex("#1d1d1f"),
		text2:       ui.RGBA(60, 60, 67, 0.68),
		text3:       ui.RGBA(60, 60, 67, 0.4),
		accent:      accent,
		onAccent:    onAccent,
		onImage:     white,
		onImage2:    ui.RGBA(255, 255, 255, 0.8),
		scrim:       ui.RGBA(0, 0, 0, 0.55),
		scrimSoft:   ui.RGBA(0, 0, 0, 0.26),
		shadow:      ui.RGBA(0, 0, 0, 0.12),
		shadowLift:  ui.RGBA(0, 0, 0, 0.3),
		star:        ui.Hex("#ff9f0a"),
		favorite:    ui.Hex("#ff375f"),
		danger:      t.Danger,
		focusRing:   accent.Alpha(0.5),
		progress:    white,
		progressBed: ui.RGBA(255, 255, 255, 0.36),
	}
}

// applyTheme derives the palette from the window's theme and sets the
// theme the widgets draw with: the app's colors, a softer focus ring, and
// slimmer scroll bars. It runs at the start of every frame, so the window
// follows the system's appearance and accent as they change.
func (a *app) applyTheme(c *ui.Context) {
	t := *c.Theme()
	a.pal = paletteFor(&t)
	p := a.pal
	t.Background = p.bg
	t.Text, t.TextMuted = p.text, p.text2
	t.Selection = p.accent.Alpha(0.28)
	t.Focus = p.focusRing
	t.FocusRingWidth, t.FocusRingOffset = 3, 1
	t.Radius = radiusButton
	t.FontSize = fsBody
	t.ScrollbarStyle = ui.ScrollbarStyle{ThumbWidth: 7, TrackWidth: 13, MinLength: 32}
	if p.dark {
		t.Inverse, t.InverseText = ui.Hex("#3a3a3e"), ui.Hex("#f5f5f7")
	}
	c.SetTheme(&t)
}

// Theme modes the user picks among in the settings.
const (
	themeSystem = "system"
	themeLight  = "light"
	themeDark   = "dark"
)

// applyThemeMode makes the windows light, dark, or follow the system. The
// system's own parts follow too: the window's material, its buttons and
// menus. Only a change is applied, since it repaints every window.
func applyThemeMode(mode string) {
	src := mygo.ThemeSystem
	switch mode {
	case themeLight:
		src = mygo.ThemeLight
	case themeDark:
		src = mygo.ThemeDark
	}
	if mygo.Theme.Source() != src {
		mygo.Theme.SetSource(src)
	}
}
