package ui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/Basmanjacks/uaanime/internal/i18n"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestViewHeightFits(t *testing.T) {
	for _, size := range []struct {
		w int
		h int
	}{
		{w: 80, h: 24},
		{w: 120, h: 30},
	} {
		for _, scr := range []screen{screenHome, screenSearch} {
			m := newTestModel(t)
			m.screen = scr
			updated, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m = updated.(Model)

			if got := lipgloss.Height(m.View().Content); got != size.h {
				t.Errorf("screen=%d size=%dx%d: view height = %d, want %d", scr, size.w, size.h, got, size.h)
			}
		}
	}
}

func TestRelayoutCapsWidth(t *testing.T) {
	const wantCap = 92
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 200, Height: 40})
	if got := m.list.Width(); got != wantCap {
		t.Errorf("wide list width = %d, want %d", got, wantCap)
	}

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	if got := m.list.Width(); got != 78 {
		t.Errorf("regular list width = %d, want 78", got)
	}
}

func TestQuitKeyDisabled(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenEpisodes
	m.setItems([]item{
		{title: "Епізод 1"},
		{title: "Епізод 2"},
		{title: "Епізод 3"},
	}, 0)

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if cmd == nil {
		return
	}
	if msg := cmd(); msg == (tea.QuitMsg{}) {
		t.Fatal("v key returned tea.Quit")
	}
}

func TestHomeQuitKeys(t *testing.T) {
	for _, key := range []rune{'q', 'Q'} {
		t.Run(string(key), func(t *testing.T) {
			m := newTestModel(t)
			_, cmd := pressTestKey(t, m, key, string(key))
			if cmd == nil {
				t.Fatal("quit key returned no command")
			}
			if msg := cmd(); msg != (tea.QuitMsg{}) {
				t.Fatalf("quit key returned %T, want tea.QuitMsg", msg)
			}
		})
	}
}

func TestBackStack(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("library", 1))
	m = openTestSearch(t, m)

	const query = "фрірен"
	m, req := launchTestSearch(t, m, query)
	cards := testCards("result", 3)
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: cards})
	m.list.Select(1)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      req,
		ref:      cards[1].TitleRef,
		eps:      testEpisodes(2),
		navigate: true,
	})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after episodes = %d, want %d", m.screen, screenEpisodes)
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenSearch {
		t.Fatalf("screen after first esc = %d, want %d", m.screen, screenSearch)
	}
	if got := m.list.Index(); got != 1 {
		t.Errorf("search cursor after back = %d, want 1", got)
	}
	if m.query != query {
		t.Errorf("query after back = %q, want %q", m.query, query)
	}
	if got := m.input.Value(); got != query {
		t.Errorf("input after back = %q, want %q", got, query)
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after second esc = %d, want %d", m.screen, screenHome)
	}
}

func TestStaleMsgIgnored(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, staleReq := launchTestSearch(t, m, "old")
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after esc = %d, want %d", m.screen, screenHome)
	}
	wantItems := len(m.list.Items())

	m, _ = updateTestModel(t, m, searchDoneMsg{req: staleReq, page: 1, cards: testCards("stale", 2)})
	if m.screen != screenHome {
		t.Errorf("stale result changed screen to %d", m.screen)
	}
	if got := len(m.list.Items()); got != wantItems {
		t.Errorf("stale result changed item count to %d, want %d", got, wantItems)
	}
	for _, listItem := range m.list.Items() {
		if it := listItem.(item); it.title == "stale 1" || it.title == "stale 2" {
			t.Errorf("stale result item %q was applied", it.title)
		}
	}
}

func TestDoubleRequest(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, reqA := launchTestSearch(t, m, "A")
	m, _ = pressTestKey(t, m, '/', "/")
	m.input.SetValue("B")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	reqB := m.reqID
	if reqB == reqA {
		t.Fatalf("second search reused request ID %d", reqB)
	}

	m, _ = updateTestModel(t, m, searchDoneMsg{req: reqA, page: 1, cards: testCards("A", 1)})
	if got := len(m.list.Items()); got != 0 {
		t.Fatalf("stale search A applied %d items", got)
	}
	cardsB := testCards("B", 2)
	m, _ = updateTestModel(t, m, searchDoneMsg{req: reqB, page: 1, cards: cardsB})
	if got := len(m.list.Items()); got != len(cardsB) {
		t.Fatalf("search B applied %d items, want %d", got, len(cardsB))
	}
	for i, ref := range cardsB {
		if got := m.list.Items()[i].(item).title; got != ref.Name {
			t.Errorf("item %d title = %q, want %q", i, got, ref.Name)
		}
	}
}

func TestPendingFrameNoDup(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, req := launchTestSearch(t, m, "double")
	cards := testCards("double", 1)
	ref := cards[0].TitleRef
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: cards})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	secondReq := m.reqID
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      secondReq,
		ref:      ref,
		eps:      testEpisodes(1),
		navigate: true,
	})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after second response = %d, want %d", m.screen, screenEpisodes)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenSearch {
		t.Fatalf("screen after esc = %d, want %d", m.screen, screenSearch)
	}
	if got := len(m.stack); got != 1 {
		t.Fatalf("stack length after esc = %d, want 1 home frame", got)
	}
	if m.stack[0].screen != screenHome {
		t.Fatalf("remaining stack frame = %d, want home %d", m.stack[0].screen, screenHome)
	}

	stackLen := len(m.stack)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      req,
		ref:      ref,
		err:      errors.New("episodes failed"),
		navigate: true,
	})
	if m.screen != screenSearch {
		t.Errorf("error response changed screen to %d, want %d", m.screen, screenSearch)
	}
	if got := len(m.stack); got != stackLen {
		t.Errorf("error response changed stack length to %d, want %d", got, stackLen)
	}
	if m.errText == "" {
		t.Error("error response did not set errText")
	}
}

func TestHomeCursorSkipsHeaders(t *testing.T) {
	m := newTestModel(t)
	seedTestLibrary(&m, testRefs("cursor", 3), library.StateWatching)
	m.catalog[provider.CatalogTopSeason] = testCards("cursor-catalog", 1)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})

	items := homeItems(t, m)
	if !items[0].header {
		t.Fatalf("home does not start with a section header: %+v", items[0])
	}
	if m.list.Index() == 0 || items[m.list.Index()].header {
		t.Fatalf("initial selection = %d, lands on a header", m.list.Index())
	}

	assertRow := func(step string) {
		t.Helper()
		if items[m.list.Index()].header {
			t.Fatalf("%s: cursor stopped on header %q (index %d)",
				step, items[m.list.Index()].title, m.list.Index())
		}
	}

	for i := range items {
		m, _ = pressTestKey(t, m, tea.KeyDown, "")
		assertRow(fmt.Sprintf("down #%d", i+1))
	}
	for i := range items {
		m, _ = pressTestKey(t, m, tea.KeyUp, "")
		assertRow(fmt.Sprintf("up #%d", i+1))
	}
	m, _ = pressTestKey(t, m, tea.KeyHome, "")
	assertRow("home")
	m, _ = pressTestKey(t, m, tea.KeyEnd, "")
	assertRow("end")
}

func TestHomeHeaderEnterIsNoop(t *testing.T) {
	m := newTestModel(t)
	seedTestLibrary(&m, testRefs("noop", 1), library.StateWatching)
	m.list.Select(0) // заголовок «Продовжити»

	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenHome {
		t.Fatalf("enter on header changed screen to %d", m.screen)
	}
	if cmd != nil {
		t.Error("enter on header returned a command")
	}
}

// Повернення назад не має воскрешати статус минулої дії: після «додано в
// закладки» на результатах і переходу в серії Esc показує підказку, а не
// той самий застарілий рядок.
func TestBackDropsStaleStatus(t *testing.T) {
	m := newTestModel(t)
	m.eng.Provider = episodesStub(testEpisodes(2))
	searchTestCards(&m, testCards("Тайтл", 1))
	m.status = i18n.TuiBookmarkAdded

	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	m, _ = updateTestModel(t, m, cmd())
	if m.screen != screenEpisodes {
		t.Fatalf("екран = %d, want серії", m.screen)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenSearch {
		t.Fatalf("екран після Esc = %d, want пошук", m.screen)
	}
	if m.status != "" {
		t.Fatalf("статус після Esc = %q, want порожній (підказка)", m.status)
	}
}
