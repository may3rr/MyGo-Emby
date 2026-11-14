package app

import (
	"encoding/json"
	"os"
	"sync"
)

// Settings are the user's choices and what the app keeps between runs, in
// settings.json in the user's data directory. The app holds them in
// a.settings, on the main thread; a.saveSettings hands a copy to the store,
// which writes it in the background.
type Settings struct {
	// ThemeMode is themeSystem, themeLight or themeDark.
	ThemeMode string `json:"themeMode"`
	// MaxBitrate caps the bit rate asked of the server for streaming, in
	// bits per second; 0 lets the server choose.
	MaxBitrate int `json:"maxBitrate"`
	// SubtitleLang and AudioLang are the languages preferred for subtitles
	// and audio, as ISO 639-2 codes ("chi", "eng"); "" leaves it to the
	// server and the file, and "off" turns subtitles off.
	SubtitleLang string `json:"subtitleLang"`
	AudioLang    string `json:"audioLang"`
	// AutoPlayNext plays the next episode when one ends.
	AutoPlayNext bool `json:"autoPlayNext"`
	// Volume is the player's volume as last left, from 0 to 1.
	Volume float64 `json:"volume"`
	// SidebarCollapsed hides the sidebar, giving its width to the page.
	// The false zero value keeps older settings expanded.
	SidebarCollapsed bool `json:"sidebarCollapsed,omitempty"`
	// SubtitleScale scales rendered subtitles in the built-in player (0 = 1.0x).
	SubtitleScale float64 `json:"subtitleScale,omitempty"`
	// SearchHistory holds recent searches shown on the empty search page.
	SearchHistory []string `json:"searchHistory,omitempty"`
	// DeviceID tells this installation apart to the servers, which keep
	// its sessions by it: made once, on the first run, and kept.
	DeviceID string `json:"deviceId,omitempty"`
	// Libraries is how the user sorts and filters each library, by its
	// ID. Replace the map to change it: copies of Settings share it.
	Libraries map[string]LibraryView `json:"libraries,omitempty"`
	// Servers and Session are the servers saved and the session signed in,
	// kept as the login code encodes them. Treat them as read-only: copies
	// of Settings share their bytes.
	Servers json.RawMessage `json:"servers,omitempty"`
	Session json.RawMessage `json:"session,omitempty"`
}

// LibraryView is how a library is sorted and filtered: Sort is "name",
// "added", "year" or "rating", Filter "all", "unplayed" or "favorites".
type LibraryView struct {
	Sort   string `json:"sort"`
	Desc   bool   `json:"desc,omitempty"`
	Filter string `json:"filter,omitempty"`
}

// defaultSettings are the settings of a first run.
func defaultSettings() Settings {
	return Settings{ThemeMode: themeSystem, AutoPlayNext: true, Volume: 0.8}
}

// settingsStore keeps the settings in a file. Save never blocks the caller:
// a goroutine writes the latest settings handed to it, through a temporary
// file renamed over the old one, so that a crash never leaves half a file.
type settingsStore struct {
	path string

	mu      sync.Mutex
	pending *Settings // the latest settings not written yet

	write sync.Mutex // held while writing
	wake  chan struct{}
}

// openSettings returns the store at path and the settings it holds, or the
// defaults where it holds none or cannot be read. An empty path keeps
// nothing, as in tests.
func openSettings(path string) (*settingsStore, Settings) {
	s := defaultSettings()
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			loaded := defaultSettings()
			if json.Unmarshal(data, &loaded) == nil {
				s = loaded
			}
		}
	}
	switch s.ThemeMode {
	case themeSystem, themeLight, themeDark:
	default:
		s.ThemeMode = themeSystem
	}
	s.Volume = min(max(s.Volume, 0), 1)
	st := &settingsStore{path: path, wake: make(chan struct{}, 1)}
	if path != "" {
		go st.writer()
	}
	return st, s
}

// Save hands s to the store, which writes it soon. Settings saved in quick
// succession are written once, the latest.
func (st *settingsStore) Save(s Settings) {
	if st == nil || st.path == "" {
		return
	}
	st.mu.Lock()
	st.pending = &s
	st.mu.Unlock()
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// Flush writes what Save was handed and has not been written, and waits
// for it: as the app quits, and in tests.
func (st *settingsStore) Flush() error {
	if st == nil || st.path == "" {
		return nil
	}
	return st.writePending()
}

func (st *settingsStore) writer() {
	for range st.wake {
		_ = st.writePending()
	}
}

func (st *settingsStore) writePending() error {
	st.write.Lock()
	defer st.write.Unlock()
	st.mu.Lock()
	s := st.pending
	st.pending = nil
	st.mu.Unlock()
	if s == nil {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		err = writeFileAtomic(st.path, data)
	}
	if err != nil {
		st.mu.Lock()
		if st.pending == nil {
			st.pending = s
		}
		st.mu.Unlock()
		return err
	}
	return nil
}

// saveSettings writes the app's settings in the background.
func (a *app) saveSettings() {
	a.store.Save(a.settings)
}

// setThemeMode changes the appearance and keeps the choice.
func (a *app) setThemeMode(mode string) {
	if a.settings.ThemeMode == mode {
		return
	}
	a.settings.ThemeMode = mode
	if a.win != nil {
		applyThemeMode(mode)
	}
	a.saveSettings()
}
