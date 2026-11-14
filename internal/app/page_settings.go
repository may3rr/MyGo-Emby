package app

import (
	"fmt"
	"math"
	"slices"

	"github.com/egoist/mygo/ui"
)

// Settings (DESIGN.md §4.10): appearance, playback, the pictures' cache and
// what the app and the server are, in groups of rows as macOS's settings
// lay them out. A choice is kept as it is made.

// choice is an option of a menu of the settings, with the value it keeps.
type choice[T comparable] struct {
	label string
	value T
}

var (
	bitrateChoices = []choice[int]{
		{str.Auto, 0}, {"120 Mbps", 120_000_000}, {"80 Mbps", 80_000_000}, {"60 Mbps", 60_000_000},
		{"40 Mbps", 40_000_000}, {"20 Mbps", 20_000_000}, {"10 Mbps", 10_000_000}, {"8 Mbps", 8_000_000},
		{"4 Mbps", 4_000_000}, {"2 Mbps", 2_000_000},
	}
	languageChoices = []choice[string]{
		{str.Auto, ""}, {"中文", "chi"}, {"粤语", "yue"}, {"英语", "eng"}, {"日语", "jpn"}, {"韩语", "kor"},
		{"法语", "fre"}, {"德语", "ger"}, {"西班牙语", "spa"},
	}
	subScaleChoices = []choice[float64]{
		{str.SubtitleSizeNormal, 0},
		{str.SubtitleSizeSmall, 0.85},
		{str.SubtitleSizeLarge, 1.2},
		{str.SubtitleSizeXL, 1.45},
	}
	themeChoices = []string{themeSystem, themeLight, themeDark}
)

func labels[T comparable](cs []choice[T]) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.label
	}
	return out
}

func labelOf[T comparable](cs []choice[T], v T) string {
	for _, c := range cs {
		if c.value == v {
			return c.label
		}
	}
	return cs[0].label
}

func valueOf[T comparable](cs []choice[T], label string) T {
	for _, c := range cs {
		if c.label == label {
			return c.value
		}
	}
	return cs[0].value
}

// settingsForm holds the controls' values while the page shows: bound to
// them, and written to the settings as they change.
type settingsForm struct {
	shown     bool
	theme     int
	bitrate   string
	subtitles string
	subScale  string
	audio     string
	autoNext  bool

	cacheBytes int64 // -1 while it is measured
	measured   bool
}

func normalizeSubtitleScale(v float64) float64 {
	if v <= 0 || math.Abs(v-1) < 0.01 {
		return 0
	}
	return v
}

// syncForm takes the values from the settings, as the page first shows.
func (a *app) syncForm() {
	f := &a.form
	s := a.settings
	f.theme = max(slices.Index(themeChoices, s.ThemeMode), 0)
	f.bitrate = labelOf(bitrateChoices, s.MaxBitrate)
	subs := append([]choice[string]{{str.LangOff, "off"}}, languageChoices...)
	f.subtitles = labelOf(subs, s.SubtitleLang)
	f.subScale = labelOf(subScaleChoices, normalizeSubtitleScale(s.SubtitleScale))
	f.audio = labelOf(languageChoices, s.AudioLang)
	f.autoNext = s.AutoPlayNext
	f.shown = true
}

// measureCache finds how much the pictures take on disk, off the main
// thread.
func (a *app) measureCache() {
	a.form.cacheBytes, a.form.measured = -1, true
	img := a.img
	go func() {
		n := img.DiskUsage()
		a.update(func() { a.form.cacheBytes = n })
	}()
}

// clearCache empties the pictures' cache on disk.
func (a *app) clearCache() {
	a.form.cacheBytes = -1
	img := a.img
	go func() {
		err := img.ClearDisk()
		n := img.DiskUsage()
		a.update(func() {
			a.form.cacheBytes = n
			if err != nil {
				a.toast = err.Error()
				return
			}
			a.toast = str.CacheCleared
		})
	}()
}

// byteSize says a size as people read it: "12.3 MB".
func byteSize(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}

func (a *app) settingsPage(c *ui.Context, r *ui.Route) {
	if !a.form.shown {
		a.syncForm()
	}
	if !a.form.measured {
		a.measureCache()
	}
	f := &a.form
	a.scrollPage(c, r, pageSpec{Title: str.Settings}, func() {
		ui.Column(c.Key("settings")).Gap(26).MaxWidth(640).Children(func() {
			a.settingsGroup(c, str.Appearance, func() {
				a.settingsRow(c, str.Appearance, "", func() {
					seg := ui.Segmented(c.Key("theme"), &f.theme, str.FollowSystem, str.Light, str.Dark).Label(str.Appearance)
					if seg.Changed() {
						a.setThemeMode(themeChoices[f.theme])
					}
				})
			})
			a.settingsGroup(c, str.Playback, func() {
				a.settingsRow(c, str.MaxBitrate, "", func() {
					if ui.Select(c.Key("bitrate"), &f.bitrate, labels(bitrateChoices)).Label(str.MaxBitrate).Changed() {
						a.settings.MaxBitrate = valueOf(bitrateChoices, f.bitrate)
						a.saveSettings()
					}
				})
				a.settingsSeparator(c)
				a.settingsRow(c, str.SubtitleLang, "", func() {
					subs := append([]choice[string]{{str.LangOff, "off"}}, languageChoices...)
					if ui.Select(c.Key("subtitles"), &f.subtitles, labels(subs)).Label(str.SubtitleLang).Changed() {
						a.settings.SubtitleLang = valueOf(subs, f.subtitles)
						a.saveSettings()
					}
				})
				a.settingsSeparator(c)
				a.settingsRow(c, str.SubtitleSize, "", func() {
					if ui.Select(c.Key("sub-scale"), &f.subScale, labels(subScaleChoices)).Label(str.SubtitleSize).Changed() {
						a.settings.SubtitleScale = valueOf(subScaleChoices, f.subScale)
						a.saveSettings()
						if a.player != nil {
							a.player.SetSubtitleScale(a.settings.SubtitleScale)
						}
					}
				})
				a.settingsSeparator(c)
				a.settingsRow(c, str.AudioLang, "", func() {
					if ui.Select(c.Key("audio"), &f.audio, labels(languageChoices)).Label(str.AudioLang).Changed() {
						a.settings.AudioLang = valueOf(languageChoices, f.audio)
						a.saveSettings()
					}
				})
				a.settingsSeparator(c)
				a.settingsRow(c, str.AutoNext, str.AutoNextBody, func() {
					if ui.Switch(c.Key("auto-next"), &f.autoNext).Label(str.AutoNext).Changed() {
						a.settings.AutoPlayNext = f.autoNext
						a.saveSettings()
					}
				})
			})
			a.settingsGroup(c, str.Cache, func() {
				size := str.Measuring
				if f.cacheBytes >= 0 {
					size = byteSize(f.cacheBytes)
				}
				a.settingsRow(c, str.CacheSize, size, func() {
					b := a.pillButton(c, nil, str.ClearCache, pillPlain).Disabled(f.cacheBytes < 0)
					b.OnClick(a.clearCache)
				})
			})
			a.settingsGroup(c, str.About, func() {
				a.settingsFact(c, str.AppVersion, str.AppName+" "+appVersion)
				if cl := a.client; cl != nil {
					s := cl.Session()
					a.settingsSeparator(c)
					a.settingsFact(c, str.ServerLabel, joinNonEmpty(" · ", serverTitle(s), kindLabel(s.ServerKind)+" "+s.ServerVersion, serverHost(s)))
					a.settingsSeparator(c)
					a.settingsFact(c, str.SignedInAs, s.UserName)
				}
				a.settingsSeparator(c)
				a.settingsFact(c, str.DeviceLabel, a.device.Device)
				a.settingsSeparator(c)
				credStatus := str.CredentialKeychain
				if a.plainTokens {
					credStatus = str.CredentialPlaintext
				}
				a.settingsFact(c, str.CredentialStore, credStatus)
			})
		})
	})
}

// settingsGroup is a titled panel of rows.
func (a *app) settingsGroup(c *ui.Context, title string, rows func()) {
	p := a.pal
	ui.Column(c.Key(title)).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(fsBody).FontWeight(wSemibold).TextColor(p.text2).Margin(0, 0, 0, 4)
		ui.Column(c).Radius(radiusPanel - 4).Background(p.fill.Alpha(0.55)).PaddingX(16).Children(rows)
	})
}

// settingsRow is a setting: its name, a line under it, and its control.
func (a *app) settingsRow(c *ui.Context, label, detail string, control func()) {
	p := a.pal
	ui.Row(c.Key(label)).MinHeight(48).Padding(8, 0).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, label).FontSize(fsBody).TextColor(p.text)
			if detail != "" {
				ui.Text(c, detail).FontSize(fsCaption).TextColor(p.text2)
			}
		})
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Children(control)
	})
}

// settingsFact is a row of what something is, without a control.
func (a *app) settingsFact(c *ui.Context, label, value string) {
	p := a.pal
	ui.Row(c.Key(label)).MinHeight(44).Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(fsBody).TextColor(p.text).Shrink(0)
		ui.Spacer(c)
		ui.Text(c, value).FontSize(fsBody).TextColor(p.text2).SingleLine().Shrink(1).MinWidth(0)
	})
}

func (a *app) settingsSeparator(c *ui.Context) {
	ui.Box(c).Height(1).Background(a.pal.hairline)
}
