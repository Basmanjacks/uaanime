package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// dirtyLibraryJSON — library.json, який пережив ручне редагування і старішу
// версію без санітизації: `null` у масиві, тайтл із traversal-слагом плюс його
// запис і прогрес, і назва з ESC-послідовністю, що чистить чужий екран.
const dirtyLibraryJSON = `{
  "titles": [
    null,
    {"id":"bad","name":"Погана","sources":[
      {"provider":"anitube","slug":"../x","name":"Погана","url":"https://evil.invalid/x"}]},
    {"id":"good","name":"Фрірен\u001b[2J","sources":[
      {"provider":"anitube","slug":"4465-frren","name":"Фрірен","url":"https://evil.invalid/y"}]}
  ],
  "entries": [
    null,
    {"title_id":"bad","state":"watching"},
    {"title_id":"good","state":"watching","kind_pin":"\u001b"}
  ],
  "progress": [
    {"title_id":"bad","episode":1,"position_sec":60,"duration_sec":1440,"watched_at":"2026-01-01T00:00:00Z"},
    {"title_id":"good","episode":2,"position_sec":120,"duration_sec":1440,"watched_at":"2026-02-01T00:00:00Z"}
  ]
}`

// Домівка бере t.Sources[0] без перевірок і показує назву як є, тому битий
// library.json має знешкоджуватися ще на читанні, а не в місці показу.
func TestViewSurvivesDirtyLibrary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library.json"), []byte(dirtyLibraryJSON), 0o600); err != nil {
		t.Fatalf("write library.json: %v", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("load library: %v", err)
	}

	for i, title := range lib.Titles {
		if title == nil || len(title.Sources) == 0 {
			t.Fatalf("titles[%d] = %+v, дірка пережила читання", i, title)
		}
	}
	if len(lib.Titles) != 1 || lib.Titles[0].ID != "good" {
		t.Fatalf("titles = %+v, очікував лише валідний тайтл", lib.Titles)
	}

	m := New(&playback.Engine{Store: st, Lib: lib, Provider: providertest.Stub{}}, Options{})
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	// СИРИЙ View: ansi.Strip прибрав би і шкідливу послідовність — хибний успіх.
	if view := m.View().Content; strings.Contains(view, "\x1b[2J") {
		t.Errorf("у View() є ESC[2J: %q", view)
	}
}
