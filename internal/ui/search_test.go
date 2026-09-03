package ui

import (
	"reflect"
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

// TestSearchRecentQueriesJourney — повний шлях клавішами: пошук запам'ятовується,
// а наступного разу повторюється з рядка історії без набору тексту.
func TestSearchRecentQueriesJourney(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m, _ = pressTestKey(t, m, '/', "/")
	if len(m.searches) != 0 {
		t.Fatalf("history on a fresh store = %#v, want empty", m.searches)
	}
	m, req := launchTestSearch(t, m, "фрірен")
	cards := testCards("recent", 3)
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: cards})
	first := homeItems(t, m)
	if !reflect.DeepEqual(m.searches, []string{"фрірен"}) {
		t.Fatalf("history after a search = %#v, want [фрірен]", m.searches)
	}
	if len(first) != len(cards) {
		t.Fatalf("rows with results = %d, want %d (history must be gone)", len(first), len(cards))
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	m, _ = pressTestKey(t, m, '/', "/")
	rows := homeItems(t, m)
	if len(rows) != 2 || !rows[0].header || rows[0].title != i18n.TuiBlockRecent {
		t.Fatalf("recent section = %#v", rows)
	}
	if rows[1].title != "фрірен" {
		t.Fatalf("recent row = %q, want %q", rows[1].title, "фрірен")
	}

	// down переводить фокус у список, enter повторює запит тим самим кодом
	m, _ = pressTestKey(t, m, tea.KeyDown, "")
	if m.input.Focused() {
		t.Fatal("input is still focused after down")
	}
	if m.list.Index() != 1 {
		t.Fatalf("cursor after down = %d, want 1 (header skipped)", m.list.Index())
	}
	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("enter on a recent row returned no command")
	}
	if m.query != "фрірен" || m.input.Value() != "фрірен" {
		t.Fatalf("query = %q, input = %q, want фрірен", m.query, m.input.Value())
	}
	if m.status != i18n.TuiSearching {
		t.Fatalf("status = %q, want %q", m.status, i18n.TuiSearching)
	}
	m, _ = updateTestModel(t, m, searchDoneMsg{req: m.reqID, page: 1, cards: cards})
	if got := homeItems(t, m); !reflect.DeepEqual(got, first) {
		t.Fatalf("repeated search rows differ: %#v vs %#v", got, first)
	}
}

func TestSearchUpReturnsFocusToInput(t *testing.T) {
	m := newTestModel(t)
	m, _ = pressTestKey(t, m, '/', "/")
	m, req := launchTestSearch(t, m, "фрірен")
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: testCards("up", 2)})
	if m.input.Focused() {
		t.Fatal("input stayed focused after a search")
	}

	// не з першого рядка up лишається навігацією списку
	m.list.Select(1)
	m, _ = pressTestKey(t, m, tea.KeyUp, "")
	if m.input.Focused() {
		t.Fatal("up from the second row must not steal focus")
	}
	if m.list.Index() != 0 {
		t.Fatalf("cursor after up = %d, want 0", m.list.Index())
	}

	m, _ = pressTestKey(t, m, tea.KeyUp, "")
	if !m.input.Focused() {
		t.Fatal("up on the first row did not return focus to the input")
	}
	if m.input.Value() != "фрірен" {
		t.Fatalf("input value after up = %q, want the query back", m.input.Value())
	}
}

// TestSearchForgetRecentQuery — «x» прибирає рядок історії, і це переживає
// перезапуск: історія лежить на диску, а не в моделі.
func TestSearchForgetRecentQuery(t *testing.T) {
	m := newTestModel(t)
	m, _ = pressTestKey(t, m, '/', "/")
	for i, q := range []string{"перший", "другий"} {
		var req int
		m, req = launchTestSearch(t, m, q)
		m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: testCards("f", i+1)})
		m, _ = pressTestKey(t, m, tea.KeyEsc, "")
		m, _ = pressTestKey(t, m, '/', "/")
	}
	if !reflect.DeepEqual(m.searches, []string{"другий", "перший"}) {
		t.Fatalf("history = %#v, want [другий перший]", m.searches)
	}

	m, _ = pressTestKey(t, m, tea.KeyDown, "")
	m, _ = pressTestKey(t, m, 'x', "x")
	if !reflect.DeepEqual(m.searches, []string{"перший"}) {
		t.Fatalf("history after x = %#v, want [перший]", m.searches)
	}
	rows := homeItems(t, m)
	if len(rows) != 2 || rows[1].title != "перший" {
		t.Fatalf("rows after x = %#v", rows)
	}

	// новий Model читає той самий каталог даних — рядок не має воскреснути
	fresh := New(m.eng, Options{})
	fresh, _ = pressTestKey(t, fresh, '/', "/")
	if !reflect.DeepEqual(fresh.searches, []string{"перший"}) {
		t.Fatalf("history after New = %#v, want [перший]", fresh.searches)
	}

	// остання позначка знята — фокус повертається в поле, бо список порожній
	fresh, _ = pressTestKey(t, fresh, tea.KeyDown, "")
	fresh, _ = pressTestKey(t, fresh, 'x', "x")
	if len(fresh.searches) != 0 {
		t.Fatalf("history after the last x = %#v, want empty", fresh.searches)
	}
	if !fresh.input.Focused() {
		t.Fatal("focus did not return to the input after the list emptied")
	}
}

func TestSearchEmptyResultNotRemembered(t *testing.T) {
	m := newTestModel(t)
	m, _ = pressTestKey(t, m, '/', "/")
	m, req := launchTestSearch(t, m, "нічого")
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: nil})

	if m.status != i18n.TuiNothingFound {
		t.Fatalf("status = %q, want %q", m.status, i18n.TuiNothingFound)
	}
	if len(m.searches) != 0 {
		t.Fatalf("history = %#v, want empty", m.searches)
	}
	if got := m.eng.Store.LoadSearches(); len(got) != 0 {
		t.Fatalf("history on disk = %#v, want empty", got)
	}
}
