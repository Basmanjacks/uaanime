package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/charmbracelet/x/ansi"
)

func TestSearchUppercaseMStaysInFocusedInput(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))

	m, _ = pressTestKey(t, m, 'M', "M")

	if got := m.input.Value(); got != "M" {
		t.Fatalf("search input value = %q, want %q", got, "M")
	}
	if len(m.eng.Lib.Entries) != 0 {
		t.Fatalf("uppercase M toggled a bookmark while search input was focused: %+v", m.eng.Lib.Entries)
	}
}

// TestSearchMoreRow — «показати ще» дозаписує сторінку, а не перезапускає пошук:
// накопичені результати лишаються, а курсор стає на перший новий рядок.
func TestSearchMoreRow(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, req := launchTestSearch(t, m, "фрірен")

	first := testCards("page1", 10)
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: first, hasMore: true})
	items := homeItems(t, m)
	if len(items) != len(first)+1 {
		t.Fatalf("rows after page 1 = %d, want %d", len(items), len(first)+1)
	}
	last := items[len(items)-1]
	if _, ok := last.payload.(payloadMore); !ok {
		t.Fatalf("last row payload = %T, want payloadMore", last.payload)
	}
	if last.title != i18n.TuiShowMore {
		t.Errorf("more row title = %q, want %q", last.title, i18n.TuiShowMore)
	}
	if got := lipgloss.Height(m.View().Content); got != 24 {
		t.Errorf("view height with two-line rows = %d, want 24", got)
	}

	// Enter на «показати ще» — навігаційна дія з власним req.
	m.list.Select(len(items) - 1)
	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("enter on more row returned no command")
	}
	if m.status != i18n.TuiSearching {
		t.Errorf("status after more = %q, want %q", m.status, i18n.TuiSearching)
	}

	second := testCards("page2", 10)
	m, _ = updateTestModel(t, m, searchDoneMsg{req: m.reqID, page: 2, cards: second, hasMore: false})
	items = homeItems(t, m)
	if len(items) != len(first)+len(second) {
		t.Fatalf("rows after page 2 = %d, want %d", len(items), len(first)+len(second))
	}
	for i, it := range items {
		if _, ok := it.payload.(payloadTitle); !ok {
			t.Fatalf("row %d payload = %T, want payloadTitle", i, it.payload)
		}
	}
	if got := m.list.Index(); got != len(first) {
		t.Errorf("cursor after page 2 = %d, want %d (first new card)", got, len(first))
	}
	if items[m.list.Index()].title != second[0].Name {
		t.Errorf("cursor row = %q, want %q", items[m.list.Index()].title, second[0].Name)
	}
	if m.hasMore {
		t.Error("hasMore still set after the last page")
	}
}

func TestHomeSlashOpensSearch(t *testing.T) {
	m := newTestModel(t)
	seedTestLibrary(&m, testRefs("slash", 1), library.StateWatching)

	m, _ = pressTestKey(t, m, '/', "/")
	if m.screen != screenSearch {
		t.Fatalf("screen after / on home = %d, want %d", m.screen, screenSearch)
	}
	if !m.input.Focused() {
		t.Error("search input is not focused after /")
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after esc = %d, want %d", m.screen, screenHome)
	}
}

func TestSearchPlaceholderHasNoReverseVideo(t *testing.T) {
	m := newTestModel(t)
	m, _ = pressTestKey(t, m, '/', "/")

	view := m.input.View()
	if strings.Contains(view, "\x1b[7m") {
		t.Fatalf("search input placeholder contains reverse video: %q", view)
	}
	if !strings.Contains(ansi.Strip(view), i18n.TuiSearchPrompt) {
		t.Errorf("search input placeholder = %q, want full %q", ansi.Strip(view), i18n.TuiSearchPrompt)
	}
}

func TestSearchViewExposesRealCursorOnlyWhileFocused(t *testing.T) {
	m := newTestModel(t)
	m, _ = pressTestKey(t, m, '/', "/")

	view := m.View()
	if view.Cursor == nil {
		t.Fatal("focused search view cursor is nil")
	}
	if view.Cursor.Y != 2 {
		t.Errorf("focused search view cursor Y = %d, want 2", view.Cursor.Y)
	}

	m.input.SetValue("фрірен")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if cursor := m.View().Cursor; cursor != nil {
		t.Errorf("blurred search view cursor = %+v, want nil", cursor)
	}
}
