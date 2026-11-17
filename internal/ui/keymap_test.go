package ui

import "testing"

// Кожен екран (і обидва режими пошуку): у кожної клавіші таблиці є дія, а
// одна клавіша не належить двом рядкам — інакше друга ніколи б не спрацювала.
func TestKeymapEveryBindingRunsAndKeysAreUnique(t *testing.T) {
	screens := []screen{screenHome, screenSearch, screenEpisodes, screenStudio, screenPlaying,
		screenHistory, screenSettings, screenSettingValue, screenBookmarks,
		screenDownloadQuality, screenDownloads, screenDownloadDir}
	for _, s := range screens {
		for _, focused := range []bool{false, true} {
			m := newTestModel(t)
			m.screen = s
			if focused {
				m.input.Focus()
			} else {
				m.input.Blur()
			}
			seen := map[string]bool{}
			for _, b := range m.bindings() {
				if b.run == nil || len(b.keys) == 0 {
					t.Fatalf("екран %d: рядок %q без дії або клавіш", s, b.help)
				}
				for _, k := range b.keys {
					if seen[k] {
						t.Fatalf("екран %d (фокус %v): клавіша %q двічі", s, focused, k)
					}
					seen[k] = true
				}
			}
		}
	}
}

// Розбіжності, які таблиця виправила: «D» працювала на історії, закладках і
// пошуку, але там не показувалась; «Esc» на домівці показувалась, але нічого
// не робила.
func TestKeymapHintMatchesWhatWorks(t *testing.T) {
	m := newTestModel(t)
	for _, s := range []screen{screenHistory, screenBookmarks, screenSearch} {
		m.screen = s
		m.input.Blur()
		if !hasAction(m, "D") {
			t.Errorf("екран %d: «D» працює, але не показана", s)
		}
	}
	m.screen = screenHome
	if hasAction(m, "Esc") {
		t.Error("домівка показує «Esc», яка там нічого не робить")
	}
	// Поки поле пошуку активне, «?» — це текст запиту, тож і не підказка.
	m.screen = screenSearch
	m.input.Focus()
	if hasAction(m, "?") {
		t.Error("пошук із фокусом у полі показує «?»")
	}
}
