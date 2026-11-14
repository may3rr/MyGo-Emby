package app

import "github.com/egoist/mygo/ui"

// The app's icons: one family on a 24-point grid, drawn in the color of the
// text (ui.Icon), outlined with a 1.75-point stroke with round caps and
// joins, and the transport controls filled, as in the system's media apps.
// The outlines follow Lucide's geometry (ISC License, lucide.dev); the
// filled glyphs and the ten-second skips are drawn for this app.

// strokeIcon makes an outlined icon of SVG shapes on the 24-point grid.
func strokeIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

// fillIcon makes a solid icon of SVG shapes on the 24-point grid.
func fillIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + shapes + `</svg>`))
}

const (
	heartPath  = `M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z`
	speaker    = `<path d="M11 4.7a.7.7 0 0 0-1.2-.5L6.4 7.6a1.4 1.4 0 0 1-1 .4H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.4a1.4 1.4 0 0 1 1 .4l3.4 3.4a.7.7 0 0 0 1.2-.5z"/>`
	ten        = `<path d="M8.7 10.1 10 9v6" stroke-width="1.6"/><rect x="12.4" y="9" width="3.4" height="6" rx="1.7" stroke-width="1.6"/>`
	starPath   = `M11.5 2.3a.55.55 0 0 1 1 0l2.3 4.7a2.1 2.1 0 0 0 1.6 1.2l5.2.7a.55.55 0 0 1 .3.95l-3.7 3.6a2.1 2.1 0 0 0-.6 1.9l.9 5.1a.55.55 0 0 1-.8.6l-4.6-2.4a2.1 2.1 0 0 0-2 0L6.4 21a.55.55 0 0 1-.8-.6l.9-5.1a2.1 2.1 0 0 0-.6-1.9L2.2 9.8a.55.55 0 0 1 .3-.95l5.2-.7a2.1 2.1 0 0 0 1.6-1.2z`
	playPath   = `M7 4.9v14.2a1.2 1.2 0 0 0 1.82 1.03l11.5-7.1a1.2 1.2 0 0 0 0-2.06L8.82 3.87A1.2 1.2 0 0 0 7 4.9z`
	gearPath   = `M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z`
	eyeOutline = `M2.06 12.35a1 1 0 0 1 0-.7 10.75 10.75 0 0 1 19.88 0 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-19.88 0`
)

var (
	// Navigation and places.
	iconHome       = strokeIcon(`<path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .71-1.53l7-6a2 2 0 0 1 2.58 0l7 6A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	iconSearch     = strokeIcon(`<circle cx="11" cy="11" r="7.5"/><path d="m20.5 20.5-4.2-4.2"/>`)
	iconHeart      = strokeIcon(`<path d="` + heartPath + `"/>`)
	iconHeartFill  = strokeIcon(`<path fill="currentColor" d="` + heartPath + `"/>`)
	iconFilm       = strokeIcon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M7 3v18"/><path d="M3 7.5h4"/><path d="M3 12h18"/><path d="M3 16.5h4"/><path d="M17 3v18"/><path d="M17 7.5h4"/><path d="M17 16.5h4"/>`)
	iconTV         = strokeIcon(`<rect width="20" height="14" x="2" y="7" rx="2"/><path d="m17 2-5 5-5-5"/>`)
	iconFolder     = strokeIcon(`<path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/>`)
	iconMusic      = strokeIcon(`<path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/>`)
	iconCollection = strokeIcon(`<path d="M12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83z"/><path d="M2 12a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 12"/><path d="M2 17a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 17"/>`)
	iconPhoto      = strokeIcon(`<rect width="18" height="18" x="3" y="3" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.09-3.09a2 2 0 0 0-2.82 0L6 21"/>`)
	iconBook       = strokeIcon(`<path d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H19a1 1 0 0 1 1 1v18a1 1 0 0 1-1 1H6.5a1 1 0 0 1 0-5H20"/>`)
	iconMic        = strokeIcon(`<path d="M12 19v3"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><rect x="9" y="2" width="6" height="13" rx="3"/>`)
	iconUser       = strokeIcon(`<circle cx="12" cy="8" r="4.5"/><path d="M20 21a8 8 0 0 0-16 0"/>`)
	iconSettings   = strokeIcon(`<path d="` + gearPath + `"/><circle cx="12" cy="12" r="3"/>`)
	iconSignOut    = strokeIcon(`<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5"/><path d="M21 12H9"/>`)
	iconServer     = strokeIcon(`<rect width="20" height="8" x="2" y="2" rx="2"/><rect width="20" height="8" x="2" y="14" rx="2"/><path d="M6 6h.01"/><path d="M6 18h.01"/>`)

	// Directions.
	iconBack          = strokeIcon(`<path d="m15 18-6-6 6-6"/>`)
	iconForward       = strokeIcon(`<path d="m9 18 6-6-6-6"/>`)
	iconSidebar       = strokeIcon(`<rect width="18" height="18" x="3" y="3" rx="2"/><path d="M9 3v18"/>`)
	iconChevronDown   = strokeIcon(`<path d="m6 9 6 6 6-6"/>`)
	iconChevronRight  = strokeIcon(`<path d="m9 18 6-6-6-6"/>`)
	iconChevronUpDown = strokeIcon(`<path d="m7 15 5 5 5-5"/><path d="m7 9 5-5 5 5"/>`)

	// The player.
	iconPlay           = fillIcon(`<path d="` + playPath + `"/>`)
	iconPause          = fillIcon(`<rect x="5.5" y="4" width="4.5" height="16" rx="1.4"/><rect x="14" y="4" width="4.5" height="16" rx="1.4"/>`)
	iconSkipBack10     = strokeIcon(`<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>` + ten)
	iconSkipForward10  = strokeIcon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>` + ten)
	iconNextTrack      = fillIcon(`<path d="M4 5.7v12.6a1.15 1.15 0 0 0 1.8.95l9.1-6.3a1.15 1.15 0 0 0 0-1.9L5.8 4.75A1.15 1.15 0 0 0 4 5.7z"/><rect x="16.8" y="4.5" width="3" height="15" rx="1.5"/>`)
	iconVolume         = strokeIcon(speaker + `<path d="M16 9a5 5 0 0 1 0 6"/><path d="M19.36 18.36a9 9 0 0 0 0-12.72"/>`)
	iconVolumeLow      = strokeIcon(speaker + `<path d="M16 9a5 5 0 0 1 0 6"/>`)
	iconMute           = strokeIcon(speaker + `<path d="M22 9l-6 6"/><path d="M16 9l6 6"/>`)
	iconFullscreen     = strokeIcon(`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/>`)
	iconExitFullscreen = strokeIcon(`<path d="M8 3v3a2 2 0 0 1-2 2H3"/><path d="M21 8h-3a2 2 0 0 1-2-2V3"/><path d="M3 16h3a2 2 0 0 1 2 2v3"/><path d="M16 21v-3a2 2 0 0 1 2-2h3"/>`)
	iconSubtitles      = strokeIcon(`<rect width="18" height="14" x="3" y="5" rx="2"/><path d="M7 15h4"/><path d="M15 15h2"/><path d="M7 11h2"/><path d="M13 11h4"/>`)
	iconAudioTrack     = strokeIcon(`<path d="M2 10v3"/><path d="M6 6v11"/><path d="M10 3v18"/><path d="M14 8v7"/><path d="M18 5v13"/><path d="M22 10v3"/>`)
	iconSpeed          = strokeIcon(`<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>`)

	// Marks and actions.
	iconCheck       = strokeIcon(`<path d="M20 6 9 17l-5-5"/>`)
	iconCheckCircle = strokeIcon(`<circle cx="12" cy="12" r="10"/><path d="m8.5 12 2.5 2.5 4.5-5"/>`)
	iconX           = strokeIcon(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	iconMore        = fillIcon(`<circle cx="5" cy="12" r="1.75"/><circle cx="12" cy="12" r="1.75"/><circle cx="19" cy="12" r="1.75"/>`)
	iconRefresh     = strokeIcon(`<path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/>`)
	iconStar        = fillIcon(`<path d="` + starPath + `"/>`)
	iconSort        = strokeIcon(`<path d="m21 16-4 4-4-4"/><path d="M17 20V4"/><path d="m3 8 4-4 4 4"/><path d="M7 4v16"/>`)
	iconFilter      = strokeIcon(`<path d="M3 6h18"/><path d="M7 12h10"/><path d="M10 18h4"/>`)
	iconInfo        = strokeIcon(`<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`)
	iconPlus        = strokeIcon(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	iconClock       = strokeIcon(`<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`)
	iconEye         = strokeIcon(`<path d="` + eyeOutline + `"/><circle cx="12" cy="12" r="3"/>`)
	iconEyeOff      = strokeIcon(`<path d="M10.73 5.08a10.74 10.74 0 0 1 11.2 6.57 1 1 0 0 1 0 .7 10.75 10.75 0 0 1-1.44 2.49"/><path d="M14.08 14.16a3 3 0 0 1-4.24-4.24"/><path d="M17.48 17.5a10.75 10.75 0 0 1-15.42-5.15 1 1 0 0 1 0-.7 10.75 10.75 0 0 1 4.45-5.14"/><path d="m2 2 20 20"/>`)
	iconGrid        = strokeIcon(`<rect width="7" height="7" x="3" y="3" rx="1.5"/><rect width="7" height="7" x="14" y="3" rx="1.5"/><rect width="7" height="7" x="14" y="14" rx="1.5"/><rect width="7" height="7" x="3" y="14" rx="1.5"/>`)
	iconList        = strokeIcon(`<path d="M3 6h.01"/><path d="M3 12h.01"/><path d="M3 18h.01"/><path d="M8 6h13"/><path d="M8 12h13"/><path d="M8 18h13"/>`)
	iconAlert       = strokeIcon(`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`)
	iconArrowUp     = strokeIcon(`<path d="m5 12 7-7 7 7"/><path d="M12 19V5"/>`)
	iconArrowDown   = strokeIcon(`<path d="M12 5v14"/><path d="m19 12-7 7-7-7"/>`)
	iconWifiOff     = strokeIcon(`<path d="M12 20h.01"/><path d="M8.5 16.43a5 5 0 0 1 7 0"/><path d="M5 12.86a10 10 0 0 1 5.17-2.69"/><path d="M19 12.86a10 10 0 0 0-2-1.52"/><path d="M2 8.82a15 15 0 0 1 4.18-2.64"/><path d="M22 8.82a15 15 0 0 0-11.29-3.76"/><path d="m2 2 20 20"/>`)
	iconInbox       = strokeIcon(`<path d="M22 12h-6l-2 3h-4l-2-3H2"/><path d="M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z"/>`)
	iconExternal    = strokeIcon(`<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`)
)

// namedIcon is an icon with its name, for the gallery's sheet.
type namedIcon struct {
	name string
	svg  *ui.SVG
}

// allIcons lists the set, in the order the gallery shows it.
var allIcons = []namedIcon{
	{"home", iconHome}, {"search", iconSearch}, {"heart", iconHeart}, {"heart-fill", iconHeartFill},
	{"film", iconFilm}, {"tv", iconTV}, {"folder", iconFolder}, {"music", iconMusic},
	{"collection", iconCollection}, {"photo", iconPhoto}, {"book", iconBook}, {"mic", iconMic},
	{"user", iconUser}, {"settings", iconSettings}, {"sign-out", iconSignOut}, {"server", iconServer},
	{"back", iconBack}, {"forward", iconForward}, {"chevron-down", iconChevronDown}, {"chevron-right", iconChevronRight},
	{"chevron-up-down", iconChevronUpDown}, {"sidebar", iconSidebar}, {"play", iconPlay}, {"pause", iconPause}, {"skip-back-10", iconSkipBack10},
	{"skip-forward-10", iconSkipForward10}, {"next-track", iconNextTrack}, {"volume", iconVolume}, {"volume-low", iconVolumeLow},
	{"mute", iconMute}, {"fullscreen", iconFullscreen}, {"exit-fullscreen", iconExitFullscreen}, {"subtitles", iconSubtitles},
	{"audio-track", iconAudioTrack}, {"speed", iconSpeed}, {"check", iconCheck}, {"check-circle", iconCheckCircle},
	{"x", iconX}, {"more", iconMore}, {"refresh", iconRefresh}, {"star", iconStar},
	{"sort", iconSort}, {"filter", iconFilter}, {"info", iconInfo}, {"plus", iconPlus},
	{"clock", iconClock}, {"eye", iconEye}, {"eye-off", iconEyeOff}, {"grid", iconGrid},
	{"list", iconList}, {"alert", iconAlert}, {"inbox", iconInbox}, {"arrow-up", iconArrowUp},
	{"arrow-down", iconArrowDown}, {"wifi-off", iconWifiOff}, {"external", iconExternal},
}

// libraryIcon is the sidebar's icon of a library of a kind, as Emby and
// Jellyfin name their collection types; an unknown kind is a folder.
func libraryIcon(kind string) *ui.SVG {
	switch kind {
	case "movies":
		return iconFilm
	case "tvshows":
		return iconTV
	case "music", "musicvideos":
		return iconMusic
	case "boxsets", "playlists":
		return iconCollection
	case "photos", "homevideos":
		return iconPhoto
	case "books":
		return iconBook
	case "livetv":
		return iconTV
	case "podcasts", "audiobooks":
		return iconMic
	}
	return iconFolder
}
