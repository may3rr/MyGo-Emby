package player

import "github.com/egoist/mygo/ui"

// The player's icons: the same family as the app's (internal/app/icons.go),
// on a 24-point grid in the color of the text, outlined with a 1.75-point
// stroke with round caps and joins, and the transport controls filled. The
// outlines follow Lucide's geometry (ISC License, lucide.dev).

func strokeIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">` + shapes + `</svg>`))
}

func fillIcon(shapes string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor">` + shapes + `</svg>`))
}

const (
	speakerShape = `<path d="M11 4.7a.7.7 0 0 0-1.2-.5L6.4 7.6a1.4 1.4 0 0 1-1 .4H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.4a1.4 1.4 0 0 1 1 .4l3.4 3.4a.7.7 0 0 0 1.2-.5z"/>`
	tenShape     = `<path d="M8.7 10.1 10 9v6" stroke-width="1.6"/><rect x="12.4" y="9" width="3.4" height="6" rx="1.7" stroke-width="1.6"/>`
)

var (
	iconBack           = strokeIcon(`<path d="m15 18-6-6 6-6"/>`)
	iconPlay           = fillIcon(`<path d="M7 4.9v14.2a1.2 1.2 0 0 0 1.82 1.03l11.5-7.1a1.2 1.2 0 0 0 0-2.06L8.82 3.87A1.2 1.2 0 0 0 7 4.9z"/>`)
	iconPause          = fillIcon(`<rect x="5.5" y="4" width="4.5" height="16" rx="1.4"/><rect x="14" y="4" width="4.5" height="16" rx="1.4"/>`)
	iconSkipBack10     = strokeIcon(`<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/>` + tenShape)
	iconSkipForward10  = strokeIcon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>` + tenShape)
	iconNextTrack      = fillIcon(`<path d="M4 5.7v12.6a1.15 1.15 0 0 0 1.8.95l9.1-6.3a1.15 1.15 0 0 0 0-1.9L5.8 4.75A1.15 1.15 0 0 0 4 5.7z"/><rect x="16.8" y="4.5" width="3" height="15" rx="1.5"/>`)
	iconVolume         = strokeIcon(speakerShape + `<path d="M16 9a5 5 0 0 1 0 6"/><path d="M19.36 18.36a9 9 0 0 0 0-12.72"/>`)
	iconVolumeLow      = strokeIcon(speakerShape + `<path d="M16 9a5 5 0 0 1 0 6"/>`)
	iconMute           = strokeIcon(speakerShape + `<path d="M22 9l-6 6"/><path d="M16 9l6 6"/>`)
	iconFullscreen     = strokeIcon(`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/>`)
	iconExitFullscreen = strokeIcon(`<path d="M8 3v3a2 2 0 0 1-2 2H3"/><path d="M21 8h-3a2 2 0 0 1-2-2V3"/><path d="M3 16h3a2 2 0 0 1 2 2v3"/><path d="M16 21v-3a2 2 0 0 1 2-2h3"/>`)
	iconSubtitles      = strokeIcon(`<rect width="18" height="14" x="3" y="5" rx="2"/><path d="M7 15h4"/><path d="M15 15h2"/><path d="M7 11h2"/><path d="M13 11h4"/>`)
	iconAudioTrack     = strokeIcon(`<path d="M2 10v3"/><path d="M6 6v11"/><path d="M10 3v18"/><path d="M14 8v7"/><path d="M18 5v13"/><path d="M22 10v3"/>`)
	iconSpeed          = strokeIcon(`<path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/>`)
	iconAlert          = strokeIcon(`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`)
	iconChapters       = strokeIcon(`<line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/>`)
	iconInfo           = strokeIcon(`<circle cx="12" cy="12" r="9"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`)
	iconExternal       = strokeIcon(`<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`)
)
