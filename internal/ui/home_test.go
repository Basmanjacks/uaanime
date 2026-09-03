package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/charmbracelet/x/ansi"
)

func TestHomeResumeAlsoAppearsInBookmarks(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("dedupe", 1)
	seedTestLibrary(&m, refs, library.StateWatching)

	name := refs[0].Name
	resumeRows := 0
	bookmarkRows := 0
	for _, it := range homeItems(t, m) {
		if it.header {
			continue
		}
		if !strings.Contains(it.title, name) {
			continue
		}
		switch it.payload.(type) {
		case payloadResume:
			resumeRows++
			if it.title != fmt.Sprintf("%s · серія %d", name, 1) {
				t.Errorf("resume title = %q, want title and episode without duplicated section name", it.title)
			}
			if !it.iconAccent {
				t.Error("resume icon is not accented")
			}
		case payloadTitle:
			bookmarkRows++
			if it.role != "lib" {
				t.Errorf("bookmark row role = %q, want %q", it.role, "lib")
			}
			if it.meta != i18n.TuiStateWatching {
				t.Errorf("bookmark row meta = %q, want %q", it.meta, i18n.TuiStateWatching)
			}
		}
	}
	if resumeRows != 1 || bookmarkRows != 1 {
		t.Fatalf("rows mentioning %q: resume = %d, bookmarks = %d; want 1 each", name, resumeRows, bookmarkRows)
	}

	m.eng.Lib.Entries[0].Hidden = true
	m.showHome()
	for _, it := range homeItems(t, m) {
		if it.header && it.title == i18n.TuiBlockLibrary {
			t.Error("empty bookmarks section rendered its header")
		}
	}
}

// TestHomeContinueRows — «Продовжити» показує кілька тайтлів, найсвіжіший
// зверху, і зупиняється на homeContinueRows.
func TestHomeContinueRows(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("continue", 4)
	seedTestLibrary(&m, refs, library.StateWatching)

	// seedTestHistory дає кожному тайтлу свій час перегляду, зростаючий за
	// індексом, тому найсвіжіший — останній.
	want := []string{refs[3].Name, refs[2].Name, refs[1].Name}
	rows := sectionRows(t, m, i18n.TuiBlockContinue)
	if len(rows) != len(want) {
		t.Fatalf("continue rows = %d, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		if !strings.HasPrefix(row.title, want[i]) {
			t.Errorf("continue row %d = %q, want title %q", i, row.title, want[i])
		}
		p, ok := row.payload.(payloadResume)
		if !ok {
			t.Fatalf("continue row %d payload = %T, want payloadResume", i, row.payload)
		}
		if p.ep != resumeEpisodeFor(t, m, want[i]) {
			t.Errorf("continue row %d episode = %d", i, p.ep)
		}
	}

	// Прихований тайтл зникає з «Продовжити», звільняючи місце наступному.
	m.eng.Lib.EntryLookup(refs[3].Slug).Hidden = true
	m.showHome()
	rows = sectionRows(t, m, i18n.TuiBlockContinue)
	if len(rows) != 3 {
		t.Fatalf("continue rows after hiding = %d, want 3", len(rows))
	}
	for i, name := range []string{refs[2].Name, refs[1].Name, refs[0].Name} {
		if !strings.HasPrefix(rows[i].title, name) {
			t.Errorf("continue row %d after hiding = %q, want title %q", i, rows[i].title, name)
		}
	}

	// Прогрес без тайтлу — це запис, для якого немає що продовжувати.
	m.eng.Lib.Progress = append(m.eng.Lib.Progress, &library.Progress{
		TitleID:   "ghost",
		Episode:   7,
		WatchedAt: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	m.showHome()
	rows = sectionRows(t, m, i18n.TuiBlockContinue)
	if len(rows) != 3 {
		t.Fatalf("continue rows with a title-less progress = %d, want 3", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(row.title, "ghost") {
			t.Errorf("continue row %q built from a title-less progress record", row.title)
		}
	}
}

// resumeEpisodeFor — яку серію бібліотека пропонує продовжити для тайтлу з
// такою назвою; тест звіряє з ним номер у рядку.
func resumeEpisodeFor(t *testing.T, m Model, name string) int {
	t.Helper()

	for _, title := range m.eng.Lib.Titles {
		if title.Name != name {
			continue
		}
		ep, _, ok := m.eng.Lib.Resume(title.ID)
		if !ok {
			t.Fatalf("library has no resume point for %q", name)
		}
		return ep
	}
	t.Fatalf("title %q not found", name)
	return 0
}

// TestHomeBookmarkOrder — закладки сортуються за користю: спершу тайтли з
// новими серіями, далі — за свіжістю перегляду.
func TestHomeBookmarkOrder(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("order", 3)
	seedTestLibrary(&m, refs, library.StateWatching)

	names := func() []string {
		t.Helper()
		var out []string
		for _, row := range sectionRows(t, m, i18n.TuiBlockLibrary) {
			out = append(out, row.title)
		}
		return out
	}

	// seedTestHistory робить останній тайтл найсвіжішим.
	want := []string{refs[2].Name, refs[1].Name, refs[0].Name}
	if got := names(); !slices.Equal(got, want) {
		t.Fatalf("bookmarks by recency = %v, want %v", got, want)
	}

	// Нові серії в найстарішого — і він піднімається над усіма.
	m, _ = updateTestModel(t, m, badgesMsg{counts: map[string]int{refs[0].Slug: 2}})
	want = []string{refs[0].Name, refs[2].Name, refs[1].Name}
	if got := names(); !slices.Equal(got, want) {
		t.Fatalf("bookmarks after badges = %v, want %v", got, want)
	}
	if got := libraryRow(t, m, refs[0].Name).badge; got != i18n.NewEpisodes(2) {
		t.Errorf("badge = %q, want %q", got, i18n.NewEpisodes(2))
	}
}

// pressTestRoulette — «Що подивитись?» з домівки: рядок обирається за payload,
// бо його позиція в секції «Ще» залежить від наявності історії.
func pressTestRoulette(t *testing.T, m Model) Model {
	t.Helper()

	selectTestItem(t, &m, func(it item) bool {
		_, ok := it.payload.(payloadRoulette)
		return ok
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	return m
}

// TestHomeRouletteRowPlacement — рулетка стоїть одразу за «Пошуком нового»:
// це той самий жест «не знаю, що далі», лише без набору запиту.
func TestHomeRouletteRowPlacement(t *testing.T) {
	m := newTestModel(t)
	rows := sectionRows(t, m, i18n.TuiBlockMore)
	if len(rows) < 2 {
		t.Fatalf("«%s» rows = %d, want at least 2", i18n.TuiBlockMore, len(rows))
	}
	if _, ok := rows[0].payload.(payloadSearch); !ok {
		t.Fatalf("first row payload = %T, want payloadSearch", rows[0].payload)
	}
	if _, ok := rows[1].payload.(payloadRoulette); !ok {
		t.Fatalf("second row payload = %T, want payloadRoulette", rows[1].payload)
	}
	if rows[1].title != i18n.TuiRouletteItem {
		t.Errorf("roulette row title = %q, want %q", rows[1].title, i18n.TuiRouletteItem)
	}
}

// TestHomeRoulettePicksPlanned — кандидати рулетки: тільки «у планах» і тільки
// видимі; вибір іде через шов randN, інакше перевіряти нічого.
func TestHomeRoulettePicksPlanned(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("roulette", 3)
	seedTestLibrary(&m, refs, library.StatePlanned)
	// Прибраний із бібліотеки план рулетці не пропонується.
	m.eng.Lib.Entries[0].Hidden = true
	// Каталог є, але поки живі плани — він не потрібен.
	m.catalog[provider.CatalogTopSeason] = testCards("roulette-cat", 2)
	m.showHome()

	size := 0
	m.randN = func(n int) int {
		size = n
		return 1
	}
	m = pressTestRoulette(t, m)

	if size != 2 {
		t.Errorf("randN argument = %d, want 2 visible planned titles", size)
	}
	if m.ref != refs[2] {
		t.Errorf("roulette ref = %+v, want %+v", m.ref, refs[2])
	}
	if m.status != i18n.TuiSearching {
		t.Errorf("status = %q, want %q", m.status, i18n.TuiSearching)
	}
	if m.pending == nil {
		t.Error("roulette did not push a stack frame: Esc would have nowhere to return")
	}
}

// TestHomeRouletteFallsBackToCatalog — без планів рулетка бере з каталогу:
// «нема з чого обирати» на порожній бібліотеці — правда, але марна.
func TestHomeRouletteFallsBackToCatalog(t *testing.T) {
	m := newTestModel(t)
	cards := testCards("roulette-fresh", 3)
	m.catalog[provider.CatalogFresh] = cards
	m.showHome()

	size := 0
	m.randN = func(n int) int {
		size = n
		return 2
	}
	m = pressTestRoulette(t, m)

	if size != len(cards) {
		t.Errorf("randN argument = %d, want %d catalog cards", size, len(cards))
	}
	if m.ref != cards[2].TitleRef {
		t.Errorf("roulette ref = %+v, want %+v", m.ref, cards[2].TitleRef)
	}
}

// TestHomeRouletteEmpty — ні планів, ні каталогу: рядок лишається, але
// відповідає статусом, а не переходом у нікуди.
func TestHomeRouletteEmpty(t *testing.T) {
	m := newTestModel(t)
	m.randN = func(int) int {
		t.Fatal("roulette rolled a die with no candidates")
		return 0
	}
	m = pressTestRoulette(t, m)

	if m.status != i18n.TuiRouletteEmpty {
		t.Errorf("status = %q, want %q", m.status, i18n.TuiRouletteEmpty)
	}
	if m.screen != screenHome {
		t.Errorf("screen = %d, want home %d", m.screen, screenHome)
	}
	if m.pending != nil {
		t.Error("empty roulette pushed a stack frame")
	}
}

func typeTestRunes(t *testing.T, m Model, s string) (Model, tea.Cmd) {
	t.Helper()

	var cmd tea.Cmd
	for _, r := range s {
		m, cmd = pressTestKey(t, m, r, string(r))
	}
	return m, cmd
}

// TestHomeNyaEasterEgg — «ня» на домівці вдягає банер котячим орнаментом, а
// nyaOffMsg повертає сезонний. Обидві розкладки, бо набирають тією, яка є.
func TestHomeNyaEasterEgg(t *testing.T) {
	for _, seq := range []string{"nya", "ня", "NYA"} {
		t.Run(seq, func(t *testing.T) {
			m := newTestModel(t)
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			cat := brandCatOrnament.unicode
			if strings.Contains(ansi.Strip(m.View().Content), cat) {
				t.Fatal("home banner shows the cat ornament before the sequence")
			}

			m, _ = typeTestRunes(t, m, seq)
			if !m.nya {
				t.Fatalf("typing %q did not arm the easter egg", seq)
			}
			if m.screen != screenHome {
				t.Fatalf("screen after %q = %d, want home %d", seq, m.screen, screenHome)
			}
			if !strings.Contains(ansi.Strip(m.View().Content), cat) {
				t.Error("home banner does not show the cat ornament")
			}

			m, _ = updateTestModel(t, m, nyaOffMsg{})
			if m.nya {
				t.Error("nyaOffMsg did not disarm the easter egg")
			}
			if strings.Contains(ansi.Strip(m.View().Content), cat) {
				t.Error("home banner still shows the cat ornament after nyaOffMsg")
			}
		})
	}
}

// TestHomeNyaDoesNotStealKeys — пасхалка лише слухає: клавіші домівки роблять
// те саме, що робили.
func TestHomeNyaDoesNotStealKeys(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m, _ = typeTestRunes(t, m, "ny")
	m, _ = pressTestKey(t, m, ',', ",")
	if m.screen != screenSettings {
		t.Fatalf("screen after «,» = %d, want settings %d", m.screen, screenSettings)
	}
	// Буфер накопичується лише на домівці: «a» з екрана налаштувань не
	// добудовує послідовність.
	m, _ = typeTestRunes(t, m, "a")
	if m.nya {
		t.Error("easter egg armed from the settings screen")
	}
}

// TestEpisodesScreenHasNoEasterEgg — послідовність працює лише на домівці.
func TestEpisodesScreenHasNoEasterEgg(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.setScreen(screenEpisodes)

	m, _ = typeTestRunes(t, m, "nya")
	if m.nya || m.easter != "" {
		t.Errorf("episodes screen recorded the sequence: nya = %t, buffer = %q", m.nya, m.easter)
	}
}

func TestHomeSectionsPresent(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("section", 2)
	seedTestLibrary(&m, refs, library.StateWatching)

	var headers []string
	for _, it := range homeItems(t, m) {
		if it.header && !it.spacer {
			headers = append(headers, it.title)
		}
	}
	want := []string{i18n.TuiBlockContinue, i18n.TuiBlockLibrary, i18n.TuiBlockMore}
	if len(headers) != len(want) {
		t.Fatalf("headers = %v, want %v", headers, want)
	}
	for i, h := range want {
		if headers[i] != h {
			t.Errorf("header %d = %q, want %q", i, headers[i], h)
		}
	}
}

func TestHomeSectionSpacers(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	seedTestLibrary(&m, testRefs("spacers", 1), library.StateWatching)
	m.catalog[provider.CatalogTopSeason] = testCards("top-spacers", 1)
	m.catalog[provider.CatalogFresh] = testCards("fresh-spacers", 1)

	m.showHome()
	items := homeItems(t, m)
	if items[0].spacer {
		t.Fatal("home starts with a spacer")
	}
	wantGaps := map[string]int{
		i18n.TuiBlockContinue: 0,
		i18n.TuiBlockLibrary:  1,
		i18n.TuiBlockMore:     1,
		i18n.TuiBlockCatalog:  1,
		i18n.TuiBlockTop:      1,
		i18n.TuiBlockFresh:    1,
	}
	for index, it := range items {
		if !it.header || it.spacer {
			continue
		}
		gap := 0
		for previous := index - 1; previous >= 0 && items[previous].spacer; previous-- {
			gap++
		}
		if want, ok := wantGaps[it.title]; !ok {
			t.Errorf("unexpected section header %q", it.title)
		} else if gap != want {
			t.Errorf("spacers before %q = %d, want %d", it.title, gap, want)
		}
	}

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})
	if got := spacerCount(homeItems(t, m)); got != 0 {
		t.Errorf("short home spacers = %d, want 0", got)
	}
}

func TestHomeSpacersFollowResizeAndPreserveSelection(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})
	seedTestLibrary(&m, testRefs("resize", 1), library.StateWatching)
	m.catalog[provider.CatalogTopSeason] = testCards("resize-top", 1)
	m.showHome()
	selectTestItem(t, &m, func(it item) bool {
		_, ok := it.payload.(payloadSearch)
		return ok
	})
	selectedKey := m.list.SelectedItem().(item).key()
	if got := spacerCount(homeItems(t, m)); got != 0 {
		t.Fatalf("initial short home spacers = %d, want 0", got)
	}

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	if got := spacerCount(homeItems(t, m)); got == 0 {
		t.Fatal("tall home has no spacers after resize")
	}
	if got := m.list.SelectedItem().(item).key(); got != selectedKey {
		t.Fatalf("selection after growing = %q, want %q", got, selectedKey)
	}

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})
	if got := spacerCount(homeItems(t, m)); got != 0 {
		t.Fatalf("short home spacers after shrinking = %d, want 0", got)
	}
	if got := m.list.SelectedItem().(item).key(); got != selectedKey {
		t.Fatalf("selection after shrinking = %q, want %q", got, selectedKey)
	}
}

// TestHomeRendersOfflineFast — перший кадр домівки не має права чекати на
// мережу. Провайдер тут nil: будь-який мережевий виклик впав би панікою, тож
// сам факт, що екран будується й малюється, і є перевіркою.
func TestHomeRendersOfflineFast(t *testing.T) {
	m := newTestModel(t)
	if m.eng.Provider != nil {
		t.Fatal("test model must have no provider: network must be impossible")
	}
	if m.eng.Lib == nil {
		t.Fatal("test model has no library")
	}

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.screen != screenHome {
		t.Fatalf("screen = %d, want home %d", m.screen, screenHome)
	}
	if got := lipgloss.Height(m.View().Content); got != 24 {
		t.Errorf("view height = %d, want 24", got)
	}
	// Кеш порожній — блоків каталогу немає, а не порожні заголовки.
	for _, kind := range catalogKinds {
		if hasSection(t, m, catalogBlockTitle(kind)) {
			t.Errorf("catalog section %q rendered with an empty cache", catalogBlockTitle(kind))
		}
	}
	if cmd := m.Init(); cmd != nil {
		t.Error("Init scheduled work without a provider")
	}

	// DoD брифу: холодний старт — перший кадр без мережі за < 100 мс.
	start := time.Now()
	cold := New(m.eng, Options{})
	cold.w, cold.h = 80, 24
	_ = cold.View()
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("холодний старт %v, очікування < 100ms", d)
	}
}

// TestHomeCatalogBlocks — блок каталогу обрізається до homeCatalogRows рядків:
// домівка лишається оглядом, а не другим екраном пошуку.
func TestHomeCatalogBlocks(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})

	cards := testCards("top", 8)
	m, _ = updateTestModel(t, m, catalogMsg{kind: provider.CatalogTopSeason, cards: cards})

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, strings.ToUpper(i18n.TuiBlockCatalog)) {
		t.Error("home view does not contain catalogue rule label")
	}
	if !strings.Contains(view, "─") {
		t.Error("home view does not contain catalogue rule")
	}

	header := i18n.TuiBlockTop
	if !hasSection(t, m, header) {
		t.Fatalf("section %q not rendered after catalogMsg", header)
	}
	headers := 0
	for _, it := range homeItems(t, m) {
		if it.header && it.title == header {
			headers++
		}
	}
	if headers != 1 {
		t.Errorf("headers %q = %d, want 1", header, headers)
	}

	rows := sectionRows(t, m, header)
	if len(rows) != homeCatalogRows {
		t.Fatalf("catalog rows = %d, want %d", len(rows), homeCatalogRows)
	}
	for i, row := range rows {
		if row.title != cards[i].Name {
			t.Errorf("row %d title = %q, want %q", i, row.title, cards[i].Name)
		}
		p, ok := row.payload.(payloadTitle)
		if !ok {
			t.Fatalf("row %d payload = %T, want payloadTitle", i, row.payload)
		}
		if p.ref != cards[i].TitleRef {
			t.Errorf("row %d ref = %+v, want %+v", i, p.ref, cards[i].TitleRef)
		}
	}
	// Другий блок не приїхав — його заголовка бути не повинно.
	if hasSection(t, m, i18n.TuiBlockFresh) {
		t.Error("fresh section rendered without cards")
	}
}

// TestCatalogSurvivesNavigation — catalogMsg пасивне: воно оновлює модель,
// але не має права перемалювати екран, на якому людина зараз працює.
func TestCatalogSurvivesNavigation(t *testing.T) {
	m := newTestModel(t)
	seedTestLibrary(&m, testRefs("nav", 1), library.StateWatching)
	m = openTestSearch(t, m)
	if got := len(m.list.Items()); got != 0 {
		t.Fatalf("fresh search screen has %d items, want 0", got)
	}

	cards := testCards("catalog", 3)
	m, _ = updateTestModel(t, m, catalogMsg{kind: provider.CatalogFresh, cards: cards})
	if m.screen != screenSearch {
		t.Fatalf("catalogMsg changed screen to %d, want search %d", m.screen, screenSearch)
	}
	if got := len(m.list.Items()); got != 0 {
		t.Fatalf("catalogMsg rebuilt the search list: %d items", got)
	}
	if got := len(m.catalog[provider.CatalogFresh]); got != len(cards) {
		t.Fatalf("model catalog = %d cards, want %d", got, len(cards))
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after esc = %d, want home %d", m.screen, screenHome)
	}
	rows := sectionRows(t, m, i18n.TuiBlockFresh)
	if len(rows) != len(cards) {
		t.Fatalf("catalog rows after back = %d, want %d", len(rows), len(cards))
	}
}

func TestHistoryGrouping(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("group", 2)
	m.eng.Lib.Titles = []*library.LocalTitle{
		{ID: refs[0].Slug, Name: refs[0].Name, Sources: []provider.TitleRef{refs[0]}},
		{ID: refs[1].Slug, Name: refs[1].Name, Sources: []provider.TitleRef{refs[1]}},
	}
	m.eng.Lib.Progress = []*library.Progress{
		{TitleID: refs[0].Slug, Episode: 1, WatchedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)},
		{TitleID: refs[0].Slug, Episode: 2, WatchedAt: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)},
		{TitleID: refs[0].Slug, Episode: 3, WatchedAt: time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)},
		{TitleID: refs[1].Slug, Episode: 1, WatchedAt: time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)},
	}

	m.showHistory()
	items := homeItems(t, m)
	if len(items) != 2 {
		t.Fatalf("history rows = %d, want 2", len(items))
	}
	if items[0].title != refs[0].Name {
		t.Errorf("first history title = %q, want %q", items[0].title, refs[0].Name)
	}
	if !strings.Contains(items[0].meta, fmt.Sprintf(i18n.TuiEpisodeNo, 3)) {
		t.Errorf("first history meta = %q, want newest episode", items[0].meta)
	}
	if !strings.Contains(items[0].meta, i18n.Episodes(3)) {
		t.Errorf("first history meta = %q, want episode count %q", items[0].meta, i18n.Episodes(3))
	}
}
