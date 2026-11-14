package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	st, s := openSettings(path)
	if !reflect.DeepEqual(s, defaultSettings()) {
		t.Fatalf("a first run has %+v", s)
	}
	s.ThemeMode = themeDark
	s.MaxBitrate = 20_000_000
	s.SubtitleLang, s.AudioLang = "chi", "jpn"
	s.AutoPlayNext = false
	s.Volume = 0.35
	s.SidebarCollapsed = true
	s.Servers = json.RawMessage(`[{"url":"https://demo.jellyfin.org/stable"}]`)
	s.Session = json.RawMessage(`{"token":"x"}`)
	for i := range 50 { // saved in quick succession, the latest wins
		s.MaxBitrate = 20_000_000 + i
		st.Save(s)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	_, got := openSettings(path)
	if !reflect.DeepEqual(compact(t, got), compact(t, s)) {
		t.Errorf("read back %+v\nwant %+v", got, s)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("left %d files beside the settings", len(entries)-1)
	}
}

// compact makes the raw JSON of s comparable.
func compact(t *testing.T, s Settings) Settings {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSettingsSurviveDamage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte("{not json"), 0o600)
	if _, s := openSettings(path); !reflect.DeepEqual(s, defaultSettings()) {
		t.Errorf("a damaged file gave %+v", s)
	}
	os.WriteFile(path, []byte(`{"themeMode":"purple","volume":7}`), 0o600)
	_, s := openSettings(path)
	if s.ThemeMode != themeSystem || s.Volume != 1 || !s.AutoPlayNext {
		t.Errorf("odd values gave %+v", s)
	}
}

func TestSettingsWithoutSidebarPreferenceStayExpanded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"themeMode":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, s := openSettings(path)
	if s.SidebarCollapsed {
		t.Fatal("older settings without sidebarCollapsed became collapsed")
	}
}

// Use a store without its background writer to make failed writes deterministic.
func TestSettingsFlushRetriesFailedWrite(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &settingsStore{path: filepath.Join(parent, "settings.json"), wake: make(chan struct{}, 1)}
	s := defaultSettings()
	s.ThemeMode = themeDark
	st.Save(s)
	if err := st.Flush(); err == nil {
		t.Fatal("saving through a file should fail")
	}
	if err := st.Flush(); err == nil {
		t.Fatal("a failed save must not become a successful no-op")
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("retried settings = %+v, want %+v", got, s)
	}
}

func TestSettingsNewSaveReplacesFailedWrite(t *testing.T) {
	st := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json"), wake: make(chan struct{}, 1)}
	s := defaultSettings()
	s.Session = json.RawMessage(`invalid JSON`)
	st.Save(s)
	if err := st.Flush(); err == nil {
		t.Fatal("invalid JSON should fail to marshal")
	}
	s.Session = nil
	s.ThemeMode = themeLight
	st.Save(s)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.path)
	if err != nil {
		t.Fatal(err)
	}
	var got Settings
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("saved settings = %+v, want latest %+v", got, s)
	}
}

func TestSettingsWithoutPathKeepNothing(t *testing.T) {
	st, s := openSettings("")
	st.Save(s)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestSetThemeModeSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newApp(path, "")
	defer a.img.Close()
	a.setThemeMode(themeLight)
	if err := a.store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, s := openSettings(path); s.ThemeMode != themeLight {
		t.Errorf("saved %q", s.ThemeMode)
	}
}

func TestSetSidebarCollapsedSaves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newApp(path, "")
	defer a.img.Close()
	a.setSidebarCollapsed(true)
	if err := a.store.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, s := openSettings(path); !s.SidebarCollapsed {
		t.Error("the collapsed sidebar was not saved")
	}
}
