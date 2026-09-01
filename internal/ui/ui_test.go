package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

type stubProvider struct {
	episodes []provider.Episode
}

func TestThemeIcons(t *testing.T) {
	unicode, ascii := themeIcons(false), themeIcons(true)
	if unicode.Cursor != "❯" || unicode.Spark != "✳" {
		t.Errorf("unicode icons: cursor = %q, spark = %q", unicode.Cursor, unicode.Spark)
	}
	if ascii.Cursor != ">" || ascii.Spark != "*" {
		t.Errorf("ASCII icons: cursor = %q, spark = %q", ascii.Cursor, ascii.Spark)
	}
}

func TestBrandBannerUniformWidth(t *testing.T) {
	if len(brandBanner) != 4 {
		t.Fatalf("brandBanner lines = %d, want 4", len(brandBanner))
	}
	w := lipgloss.Width(brandBanner[0])
	for i, line := range brandBanner[1:] {
		if got := lipgloss.Width(line); got != w {
			t.Errorf("brandBanner line %d width = %d, want %d", i+1, got, w)
		}
	}
}

func TestBrandBannerUsesTwoColors(t *testing.T) {
	oldProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = oldProfile })

	m := newTestModel(t)
	lines := strings.Split(m.brandHeader(), "\n")
	uaSGR := firstSGR(styleBrandUA.Render("x"))
	restSGR := firstSGR(styleBrandRest.Render("x"))
	if uaSGR == "" || restSGR == "" || uaSGR == restSGR {
		t.Fatalf("brand SGR styles = %q and %q, want distinct non-empty sequences", uaSGR, restSGR)
	}

	for i, want := range brandBanner {
		if got := strings.TrimPrefix(ansi.Strip(lines[i]), "  "); got != want {
			t.Errorf("banner line %d text = %q, want %q", i, got, want)
		}
		if !strings.Contains(lines[i], uaSGR) || !strings.Contains(lines[i], restSGR) {
			t.Errorf("banner line %d does not contain both brand SGR styles: %q", i, lines[i])
		}
	}
}

func TestBrandFallbackTitleUsesTwoColorsWithBothIconSets(t *testing.T) {
	oldProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = oldProfile })

	uaSGR := firstSGR(styleBrandUA.Render("x"))
	restSGR := firstSGR(styleBrandRest.Render("x"))
	if uaSGR == "" || restSGR == "" || uaSGR == restSGR {
		t.Fatalf("brand SGR styles = %q and %q, want distinct non-empty sequences", uaSGR, restSGR)
	}

	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%t", ascii), func(t *testing.T) {
			m := newTestModel(t)
			m.ic = themeIcons(ascii)
			got := m.brandFallbackTitle()
			want := m.ic.Spark + " " + i18n.TuiAppTitle + metaSep + i18n.TuiTaglineShort
			if stripped := strings.TrimSpace(ansi.Strip(got)); stripped != want {
				t.Errorf("fallback title text = %q, want %q", stripped, want)
			}

			title := []rune(i18n.TuiAppTitle)
			accent := styleBrandUA.Render(m.ic.Spark + " " + string(title[:2]))
			rest := styleBrandRest.Render(string(title[2:]))
			if !strings.Contains(got, accent+rest) {
				t.Errorf("fallback title does not contain distinct accent/rest segments: %q", got)
			}
		})
	}
}

func firstSGR(s string) string {
	start := strings.Index(s, "\x1b[")
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start:], 'm')
	if end < 0 {
		return ""
	}
	return s[start : start+end+1]
}

func TestHomeBannerRendered(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, brandBanner[2]) {
		t.Error("home view does not contain brand banner")
	}
	if !strings.Contains(view, i18n.TuiTagline) {
		t.Error("home view does not contain full tagline")
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Errorf("view height = %d, want 24", got)
	}
}

func TestHomeBannerFallbackNarrow(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 24})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, brandBanner[2]) {
		t.Error("narrow home view contains brand banner")
	}
	if !strings.Contains(view, i18n.TuiTaglineShort) {
		t.Error("narrow home view does not contain short tagline")
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Errorf("view height = %d, want 24", got)
	}
}

func TestHomeBannerFallbackShort(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, brandBanner[2]) {
		t.Error("short home view contains brand banner")
	}
	if !strings.Contains(view, i18n.TuiTaglineShort) {
		t.Error("short home view does not contain short tagline")
	}
	if got := lipgloss.Height(view); got != 14 {
		t.Errorf("view height = %d, want 14", got)
	}
}

func TestSearchScreenHasNoBanner(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenSearch
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if strings.Contains(ansi.Strip(m.View().Content), brandBanner[2]) {
		t.Error("search view contains brand banner")
	}
}

func (stubProvider) ID() string          { return "test" }
func (stubProvider) Name() string        { return "Test" }
func (stubProvider) Caps() provider.Caps { return provider.Caps{} }
func (stubProvider) Search(context.Context, string, int) (provider.Page, error) {
	return provider.Page{}, nil
}
func (stubProvider) Catalog(context.Context, provider.CatalogKind) ([]provider.TitleCard, error) {
	return nil, nil
}
func (p stubProvider) Episodes(context.Context, provider.TitleRef) ([]provider.Episode, error) {
	return p.episodes, nil
}
func (stubProvider) Sources(context.Context, provider.TitleRef, int) ([]provider.Source, error) {
	return nil, nil
}

func newTestModel(t *testing.T) Model {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	eng := &playback.Engine{Store: st, Lib: &library.Library{}}
	return New(eng)
}

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

func updateTestModel(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()

	updated, cmd := m.Update(msg)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want ui.Model", updated)
	}
	return result, cmd
}

func pressTestKey(t *testing.T, m Model, code rune, text string) (Model, tea.Cmd) {
	t.Helper()
	return updateTestModel(t, m, tea.KeyPressMsg{Code: code, Text: text})
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

func filterMatchesFromCmd(cmd tea.Cmd) (tea.Msg, bool) {
	if cmd == nil {
		return nil, false
	}
	msg := cmd()
	if _, ok := msg.(list.FilterMatchesMsg); ok {
		return msg, true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			if msg, ok := filterMatchesFromCmd(nested); ok {
				return msg, true
			}
		}
	}
	return nil, false
}

func applyTestListFilter(t *testing.T, m Model, filter string) Model {
	t.Helper()

	m, _ = pressTestKey(t, m, '/', "/")
	if m.list.FilterState() != list.Filtering {
		t.Fatalf("filter state after / = %s, want filtering", m.list.FilterState())
	}
	for _, r := range filter {
		var cmd tea.Cmd
		m, cmd = pressTestKey(t, m, r, string(r))
		msg, ok := filterMatchesFromCmd(cmd)
		if !ok {
			t.Fatalf("typing %q returned no filter matches command", r)
		}
		m, _ = updateTestModel(t, m, msg)
	}
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("filter state after enter = %s, want filter applied", m.list.FilterState())
	}
	return m
}

func selectTestItem(t *testing.T, m *Model, match func(item) bool) {
	t.Helper()

	for index, listItem := range m.list.Items() {
		it, ok := listItem.(item)
		if ok && match(it) {
			m.list.Select(index)
			return
		}
	}
	t.Fatal("matching list item not found")
}

func openTestSearch(t *testing.T, m Model) Model {
	t.Helper()

	selectTestItem(t, &m, func(it item) bool {
		_, ok := it.payload.(payloadSearch)
		return ok
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenSearch {
		t.Fatalf("screen after opening search = %d, want %d", m.screen, screenSearch)
	}
	return m
}

func launchTestSearch(t *testing.T, m Model, query string) (Model, int) {
	t.Helper()

	m.input.SetValue(query)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	return m, m.reqID
}

func testRefs(prefix string, count int) []provider.TitleRef {
	refs := make([]provider.TitleRef, count)
	for i := range refs {
		refs[i] = provider.TitleRef{
			Provider: "test",
			Slug:     prefix + "-" + string(rune('1'+i)),
			Name:     prefix + " " + string(rune('1'+i)),
		}
	}
	return refs
}

// testCards — картки пошуку поверх тих самих посилань: назва лишається
// впізнаваною в перевірках, а метадані дають другий рядок.
func testCards(prefix string, count int) []provider.TitleCard {
	refs := testRefs(prefix, count)
	cards := make([]provider.TitleCard, len(refs))
	for i, ref := range refs {
		cards[i] = provider.TitleCard{
			TitleRef: ref,
			Year:     2020 + i,
			EpAired:  12,
			HasDub:   true,
		}
	}
	return cards
}

func testEpisodes(count int) []provider.Episode {
	eps := make([]provider.Episode, count)
	for i := range eps {
		eps[i] = provider.Episode{Number: i + 1}
	}
	return eps
}

func searchTestCards(m *Model, cards []provider.TitleCard) {
	m.setScreen(screenSearch)
	m.cards = cards
	m.input.Blur()
	m.setDelegate(true)
	m.setItems(m.searchRows(), 0)
}

func bookmarkTestEntry(t *testing.T, m Model, ref provider.TitleRef) *library.Entry {
	t.Helper()

	title := m.eng.Lib.TitleByRef(ref)
	if title == nil {
		t.Fatalf("title for %+v not found", ref)
	}
	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry == nil {
		t.Fatalf("entry for %q not found", title.ID)
	}
	return entry
}

func TestSearchBookmarkKeyAddsAndRemovesPlannedTitle(t *testing.T) {
	m := newTestModel(t)
	card := testCards("bookmark", 1)[0]
	searchTestCards(&m, []provider.TitleCard{card})

	m, _ = pressTestKey(t, m, 'm', "m")
	entry := bookmarkTestEntry(t, m, card.TitleRef)
	if entry.State != library.StatePlanned {
		t.Fatalf("state after first m = %q, want %q", entry.State, library.StatePlanned)
	}
	if entry.KnownEpisodes != card.EpAired {
		t.Errorf("known episodes after first m = %d, want %d", entry.KnownEpisodes, card.EpAired)
	}
	if m.status != i18n.TuiBookmarkAdded {
		t.Errorf("status after first m = %q, want bookmark-added status", m.status)
	}
	row := homeItems(t, m)[0]
	if row.badge != i18n.TuiStatePlanned {
		t.Errorf("search badge after first m = %q, want %q", row.badge, i18n.TuiStatePlanned)
	}

	m, _ = pressTestKey(t, m, 'm', "m")
	title := m.eng.Lib.TitleByRef(card.TitleRef)
	if title == nil {
		t.Fatal("bookmark removal deleted the local title")
	}
	if entry := m.eng.Lib.EntryLookup(title.ID); entry != nil {
		t.Fatalf("entry after second m = %+v, want removed", entry)
	}
	if m.status != i18n.TuiBookmarkRemoved {
		t.Errorf("status after second m = %q, want bookmark-removed status", m.status)
	}
	if row := homeItems(t, m)[0]; row.badge != "" {
		t.Errorf("search badge after second m = %q, want empty", row.badge)
	}
}

func TestHomeUppercaseBookmarkKeyAddsPlannedTitle(t *testing.T) {
	m := newTestModel(t)
	card := testCards("uppercase-bookmark", 1)[0]
	m.catalog[provider.CatalogTopSeason] = []provider.TitleCard{card}
	m.showHome()
	selectTestItem(t, &m, func(it item) bool {
		payload, ok := it.payload.(payloadTitle)
		return ok && payload.ref == card.TitleRef
	})

	m, _ = pressTestKey(t, m, 'M', "M")

	entry := bookmarkTestEntry(t, m, card.TitleRef)
	if entry.State != library.StatePlanned {
		t.Fatalf("state after uppercase M = %q, want %q", entry.State, library.StatePlanned)
	}
}

func TestSearchBookmarkKeyRemovesWatchingTitleFromHome(t *testing.T) {
	m := newTestModel(t)
	card := testCards("watching", 1)[0]
	m.eng.Lib.Titles = []*library.LocalTitle{{
		ID:      card.Slug,
		Name:    card.Name,
		Sources: []provider.TitleRef{card.TitleRef},
	}}
	m.eng.Lib.Entries = []*library.Entry{{TitleID: card.Slug, State: library.StateWatching}}
	searchTestCards(&m, []provider.TitleCard{card})

	m, _ = pressTestKey(t, m, 'm', "m")
	if m.status != i18n.TuiBookmarkRemoved {
		t.Errorf("status after m = %q, want %q", m.status, i18n.TuiBookmarkRemoved)
	}
	entry := bookmarkTestEntry(t, m, card.TitleRef)
	if entry.State != library.StateWatching || !entry.Hidden {
		t.Errorf("entry after m = %+v, want hidden watching entry", entry)
	}
	m.showHome()
	if rows := sectionRows(t, m, i18n.TuiBlockLibrary); len(rows) != 0 {
		t.Fatalf("hidden title remains in home library block: %+v", rows)
	}
}

func TestEpisodesBookmarkKeyUsesMaximumEpisodeNumber(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("episodes-bookmark", 1)[0]
	m.episodes = []provider.Episode{{Number: 3}, {Number: 9}, {Number: 4}}
	m.showEpisodes()

	m, _ = pressTestKey(t, m, 'm', "m")
	entry := bookmarkTestEntry(t, m, m.ref)
	if entry.KnownEpisodes != 9 {
		t.Errorf("known episodes = %d, want 9", entry.KnownEpisodes)
	}
}

func TestSearchBookmarkKeyFallsBackToEpisodeCache(t *testing.T) {
	m := newTestModel(t)
	card := testCards("cached-bookmark", 1)[0]
	card.EpAired = 0
	if err := m.eng.Store.SaveEpisodes(card.TitleRef, []provider.Episode{{Number: 2}, {Number: 7}, {Number: 5}}); err != nil {
		t.Fatalf("save episode cache: %v", err)
	}
	searchTestCards(&m, []provider.TitleCard{card})

	m, _ = pressTestKey(t, m, 'm', "m")
	entry := bookmarkTestEntry(t, m, card.TitleRef)
	if entry.KnownEpisodes != 7 {
		t.Errorf("known episodes from cache = %d, want 7", entry.KnownEpisodes)
	}
}

func TestSearchBookmarkRefreshRestoresFilteredItems(t *testing.T) {
	m := newTestModel(t)
	cards := testCards("filtered-bookmark", 2)
	// Закладка й фільтр саме на ДРУГІЙ картці: її глобальний індекс 1, а видимий
	// під фільтром — 0. Плутанина індексних просторів тут губила вибір.
	if _, err := m.eng.Bookmark(cards[1].TitleRef, cards[1].EpAired); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
	searchTestCards(&m, cards)
	m.list.SetFilterText(cards[1].Name)
	if m.list.FilterState() != list.FilterApplied {
		t.Fatalf("filter state = %s, want filter applied", m.list.FilterState())
	}

	m, cmd := pressTestKey(t, m, 'm', "m")
	if cmd == nil {
		t.Fatal("bookmark refresh returned no filtering command")
	}
	m, _ = updateTestModel(t, m, cmd())
	selected, ok := m.list.SelectedItem().(item)
	if !ok {
		t.Fatal("bookmark refresh left the filtered selection empty")
	}
	if selected.title != cards[1].Name {
		t.Fatalf("selected title = %q, want %q", selected.title, cards[1].Name)
	}
}

func seedTestHistory(m *Model, refs []provider.TitleRef) {
	m.eng.Lib.Titles = make([]*library.LocalTitle, len(refs))
	m.eng.Lib.Progress = make([]*library.Progress, len(refs))
	for i, ref := range refs {
		m.eng.Lib.Titles[i] = &library.LocalTitle{
			ID:      ref.Slug,
			Name:    ref.Name,
			Sources: []provider.TitleRef{ref},
		}
		m.eng.Lib.Progress[i] = &library.Progress{
			TitleID:     ref.Slug,
			Episode:     i + 1,
			PositionSec: float64(30 + i),
			WatchedAt:   time.Date(2026, 8, 31, 12+i, 0, 0, 0, time.UTC),
		}
	}
	m.showHome()
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

func TestEpisodesEscapeClearsAppliedFilterBeforeGoingBack(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("filtered-episodes", 1)[0]
	m.episodes = testEpisodes(3)
	m.showEpisodes()
	m = applyTestListFilter(t, m, "2")

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenEpisodes {
		t.Fatalf("screen after first esc = %d, want episodes %d", m.screen, screenEpisodes)
	}
	if m.list.FilterState() != list.Unfiltered {
		t.Fatalf("filter state after first esc = %s, want unfiltered", m.list.FilterState())
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after second esc = %d, want home %d", m.screen, screenHome)
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

func TestResumeAnyOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		episodesFirst bool
	}{
		{name: "episodes then resolved", episodesFirst: true},
		{name: "resolved then episodes", episodesFirst: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("resume", 1)[0]
			seedTestHistory(&m, []provider.TitleRef{ref})
			selectTestItem(t, &m, func(it item) bool {
				_, ok := it.payload.(payloadResume)
				return ok
			})
			m, _ = pressTestKey(t, m, tea.KeyEnter, "")
			req := m.reqID
			epsMsg := episodesDoneMsg{req: req, ref: ref, eps: testEpisodes(3), navigate: false}
			resMsg := resolvedMsg{req: req, res: &playback.Resolved{
				Ref:     ref,
				Episode: 1,
				Source:  provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 1},
			}}

			if tc.episodesFirst {
				m, _ = updateTestModel(t, m, epsMsg)
				m, _ = updateTestModel(t, m, resMsg)
			} else {
				m, _ = updateTestModel(t, m, resMsg)
				m, _ = updateTestModel(t, m, epsMsg)
			}

			if got := len(m.episodes); got != 3 {
				t.Errorf("episodes populated = %d, want 3", got)
			}
			if m.screen != screenPlaying {
				t.Errorf("screen = %d, want %d", m.screen, screenPlaying)
			}
		})
	}
}

func TestPlayDoneAutoplaysNextEpisodeAfterEOF(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.ref = testRefs("autoplay", 1)[0]
	m.episodes = []provider.Episode{{Number: 1}, {Number: 3}}
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{result: &playback.Result{Reason: player.EndEOF}})

	if cmd == nil {
		t.Fatal("EOF з автоплеєм не повернув команду resolve")
	}
	if m.screen != screenPlaying {
		t.Fatalf("screen = %d, want playing %d", m.screen, screenPlaying)
	}
	if m.pendingEp != 3 {
		t.Fatalf("pending episode = %d, want 3", m.pendingEp)
	}
	if m.status != i18n.TuiResolving {
		t.Errorf("status = %q, want %q", m.status, i18n.TuiResolving)
	}
}

func TestPlayDoneStopsAutoplayAfterLastEpisode(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 2

	m, cmd := updateTestModel(t, m, playDoneMsg{result: &playback.Result{Reason: player.EndEOF}})

	if cmd != nil {
		t.Fatal("last episode returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestPlayDoneDoesNotAutoplayAfterQuit(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{result: &playback.Result{Reason: player.EndQuit}})

	if cmd != nil {
		t.Fatal("quit returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestPlayDoneDoesNotAutoplayWhenDisabled(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{result: &playback.Result{Reason: player.EndEOF}})

	if cmd != nil {
		t.Fatal("disabled autoplay returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestResolvedErrorLeavesPlayingScreen(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.reqID = 7

	m, _ = updateTestModel(t, m, resolvedMsg{req: 7, err: errors.New("resolve failed")})

	if m.screen == screenPlaying {
		t.Fatal("resolve error left model on playing screen")
	}
	if m.errText == "" {
		t.Fatal("resolve error was not shown")
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

func TestStudioTransient(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, req := launchTestSearch(t, m, "studio")
	cards := testCards("studio", 1)
	ref := cards[0].TitleRef
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: cards})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      req,
		ref:      ref,
		eps:      testEpisodes(2),
		navigate: true,
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	candidates := []provider.Source{
		{Studio: "Alpha", Kind: provider.KindDub, Episode: 1},
		{Studio: "Beta", Kind: provider.KindVoiceover, Episode: 1},
	}
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:        ref,
		Episode:    1,
		Source:     candidates[0],
		Candidates: candidates,
	}})
	if m.screen != screenStudio {
		t.Fatalf("screen after multiple candidates = %d, want %d", m.screen, screenStudio)
	}

	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:     ref,
		Episode: 1,
		Source:  candidates[0],
	}})
	if m.screen != screenPlaying {
		t.Fatalf("screen after studio choice resolved = %d, want %d", m.screen, screenPlaying)
	}

	m, _ = updateTestModel(t, m, playDoneMsg{})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after playback = %d, want %d", m.screen, screenEpisodes)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenSearch {
		t.Fatalf("screen after leaving episodes = %d, want search %d", m.screen, screenSearch)
	}
}

func TestResumeFromHistory(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("history", 2)
	seedTestHistory(&m, refs)
	selectTestItem(t, &m, func(it item) bool {
		_, ok := it.payload.(payloadHistory)
		return ok
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenHistory {
		t.Fatalf("screen after opening history = %d, want %d", m.screen, screenHistory)
	}
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req := m.reqID
	selectedRef := refs[1]
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      req,
		ref:      selectedRef,
		eps:      testEpisodes(2),
		navigate: false,
	})
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:     selectedRef,
		Episode: 2,
		Source:  provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 2},
	}})
	if m.screen != screenPlaying {
		t.Fatalf("screen after history resume = %d, want %d", m.screen, screenPlaying)
	}
	m, _ = updateTestModel(t, m, playDoneMsg{})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after playback = %d, want %d", m.screen, screenEpisodes)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHistory {
		t.Fatalf("screen after leaving episodes = %d, want %d", m.screen, screenHistory)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after leaving history = %d, want %d", m.screen, screenHome)
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

// seedTestLibrary додає до історії ще й записи списку перегляду: без Entries
// секція «Бібліотека» на домівці не будується.
func seedTestLibrary(m *Model, refs []provider.TitleRef, state library.State) {
	seedTestHistory(m, refs)
	m.eng.Lib.Entries = make([]*library.Entry, len(refs))
	for i, ref := range refs {
		m.eng.Lib.Entries[i] = &library.Entry{TitleID: ref.Slug, State: state}
	}
	m.showHome()
}

func homeItems(t *testing.T, m Model) []item {
	t.Helper()

	items := make([]item, 0, len(m.list.Items()))
	for _, listItem := range m.list.Items() {
		it, ok := listItem.(item)
		if !ok {
			t.Fatalf("list item %T is not ui.item", listItem)
		}
		items = append(items, it)
	}
	return items
}

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
		i18n.TuiBlockTop:      2,
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

func spacerCount(items []item) int {
	count := 0
	for _, it := range items {
		if it.spacer {
			count++
		}
	}
	return count
}

func TestHomeCursorSkipsHeaders(t *testing.T) {
	m := newTestModel(t)
	seedTestLibrary(&m, testRefs("cursor", 3), library.StateWatching)
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

// sectionRows — рядки під заголовком header, до наступного заголовка.
func sectionRows(t *testing.T, m Model, header string) []item {
	t.Helper()

	var rows []item
	inside := false
	for _, it := range homeItems(t, m) {
		if it.spacer {
			continue
		}
		if it.header {
			if inside {
				break
			}
			inside = it.title == header
			continue
		}
		if inside {
			rows = append(rows, it)
		}
	}
	return rows
}

func hasSection(t *testing.T, m Model, header string) bool {
	t.Helper()

	for _, it := range homeItems(t, m) {
		if it.header && it.title == header {
			return true
		}
	}
	return false
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
	cold := New(m.eng)
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

func TestHomeBookmarkKeepsCursorOnCatalogCard(t *testing.T) {
	m := newTestModel(t)
	card := testCards("catalog-bookmark", 1)[0]
	m.catalog[provider.CatalogTopSeason] = []provider.TitleCard{card}
	m.showHome()
	selectTestItem(t, &m, func(it item) bool {
		payload, ok := it.payload.(payloadTitle)
		return ok && payload.ref == card.TitleRef
	})

	m, _ = pressTestKey(t, m, 'm', "m")

	libraryIndex, catalogIndex := -1, -1
	section := ""
	for index, listItem := range m.list.Items() {
		it := listItem.(item)
		if it.header {
			section = it.title
			continue
		}
		if it.title != card.Name {
			continue
		}
		switch section {
		case i18n.TuiBlockLibrary:
			libraryIndex = index
		case i18n.TuiBlockTop:
			catalogIndex = index
		}
	}
	if libraryIndex < 0 || catalogIndex < 0 {
		t.Fatalf("same title rows after bookmark: library=%d catalog=%d", libraryIndex, catalogIndex)
	}
	if got := m.list.GlobalIndex(); got != catalogIndex {
		t.Fatalf("selected index after bookmark = %d, want catalog card %d (library row %d)", got, catalogIndex, libraryIndex)
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

// libraryRow — рядок секції «БІБЛІОТЕКА» за назвою тайтлу.
func libraryRow(t *testing.T, m Model, name string) item {
	t.Helper()

	for _, it := range sectionRows(t, m, i18n.TuiBlockLibrary) {
		if it.title == name {
			return it
		}
	}
	t.Fatalf("library row %q not found", name)
	return item{}
}

// seedBadgeModel — бібліотека з двох тайтлів; свіжіший прогрес у другого, тому
// в секції «БІБЛІОТЕКА» лишається перший — саме на ньому й перевіряємо бейдж.
func seedBadgeModel(t *testing.T, m *Model, refs []provider.TitleRef, episodes, lastEpisode int) {
	t.Helper()

	if err := m.eng.Store.SaveEpisodes(refs[0], testEpisodes(episodes)); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	seedTestLibrary(m, refs, library.StateWatching)
	e := m.eng.Lib.EntryLookup(refs[0].Slug)
	if e == nil {
		t.Fatalf("entry for %q not seeded", refs[0].Slug)
	}
	e.LastEpisode = lastEpisode
	m.showHome()
}

// TestBadgeFromCache — бейдж рахується з кешу на диску, без мережі: він має
// стояти вже в першому кадрі, а не з'являтися через секунду після нього.
func TestBadgeFromCache(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("badge", 3)
	seedBadgeModel(t, &m, refs, 5, 3)

	row := libraryRow(t, m, refs[0].Name)
	if want := i18n.NewEpisodes(2); row.badge != want {
		t.Fatalf("badge = %q, want %q", row.badge, want)
	}
	// Тайтл без кешу серій бейджа не має — і не вигадує його.
	if got := libraryRow(t, m, refs[1].Name).badge; got != "" {
		t.Errorf("badge without an episode cache = %q, want empty", got)
	}
}

func TestPlannedBadgeUsesKnownEpisodesBaseline(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("planned-badge", 1)[0]
	m.eng.Lib.Titles = []*library.LocalTitle{{
		ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref},
	}}
	m.eng.Lib.Entries = []*library.Entry{{
		TitleID: ref.Slug, State: library.StatePlanned, KnownEpisodes: 3,
	}}
	if err := m.eng.Store.SaveEpisodes(ref, testEpisodes(5)); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	m.showHome()

	if got, want := libraryRow(t, m, ref.Name).badge, i18n.NewEpisodes(2); got != want {
		t.Fatalf("planned badge = %q, want %q", got, want)
	}
}

func TestNewEpisodeCountCountsUniqueNumbersAfterBaseline(t *testing.T) {
	episodes := []provider.Episode{{Number: 1}, {Number: 3}, {Number: 3}}

	if got := newEpisodeCount(episodes, 1); got != 1 {
		t.Fatalf("newEpisodeCount = %d, want 1", got)
	}
}

func TestBadgesCmdIncludesPlannedEntries(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("planned-probe", 1)[0]
	m.eng.Provider = stubProvider{episodes: testEpisodes(4)}
	m.eng.Lib.Titles = []*library.LocalTitle{{
		ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref},
	}}
	m.eng.Lib.Entries = []*library.Entry{{
		TitleID: ref.Slug, State: library.StatePlanned, KnownEpisodes: 2,
	}}

	cmd := m.badgesCmd()
	if cmd == nil {
		t.Fatal("badgesCmd returned nil for planned entry")
	}
	msg, ok := cmd().(badgesMsg)
	if !ok {
		t.Fatalf("badgesCmd message = %T, want badgesMsg", msg)
	}
	if got := msg.counts[ref.Slug]; got != 2 {
		t.Fatalf("planned badge count = %d, want 2", got)
	}
}

func TestBadgesCmdSkipsHiddenEntriesWithoutConsumingProbeSlots(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("hidden-probe", maxBadgeProbes+1)
	m.eng.Provider = stubProvider{episodes: testEpisodes(4)}
	for i, ref := range refs {
		m.eng.Lib.Titles = append(m.eng.Lib.Titles, &library.LocalTitle{
			ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref},
		})
		m.eng.Lib.Entries = append(m.eng.Lib.Entries, &library.Entry{
			TitleID: ref.Slug, State: library.StateWatching, Hidden: i < maxBadgeProbes,
		})
	}

	cmd := m.badgesCmd()
	if cmd == nil {
		t.Fatal("badgesCmd returned nil with a visible entry")
	}
	msg, ok := cmd().(badgesMsg)
	if !ok {
		t.Fatalf("badgesCmd message = %T, want badgesMsg", msg)
	}
	visibleID := refs[maxBadgeProbes].Slug
	if got := msg.counts[visibleID]; got != 4 {
		t.Fatalf("visible badge count = %d, want 4; counts=%+v", got, msg.counts)
	}
	if len(msg.counts) != 1 {
		t.Fatalf("badgesCmd probed hidden entries: %+v", msg.counts)
	}
}

func TestOpeningPlannedTitleMarksEpisodesSeen(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("planned-open", 1)[0]
	if _, err := m.eng.Bookmark(ref, 2); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
	title := m.eng.Lib.TitleByRef(ref)
	m.badges[title.ID] = 3
	m.reqID = 7

	m, _ = updateTestModel(t, m, episodesDoneMsg{
		ref: ref, eps: testEpisodes(5), req: 7, navigate: true,
	})

	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry.KnownEpisodes != 5 {
		t.Fatalf("KnownEpisodes after open = %d, want 5", entry.KnownEpisodes)
	}
	if got := m.badges[title.ID]; got != 0 {
		t.Fatalf("badge after open = %d, want 0", got)
	}
}

func TestBookmarkAddedFetchesFreshBaseline(t *testing.T) {
	m := newTestModel(t)
	card := testCards("bookmark-fresh", 1)[0]
	card.EpAired = 12
	m.eng.Provider = stubProvider{episodes: []provider.Episode{{Number: 1}, {Number: 10}}}
	searchTestCards(&m, []provider.TitleCard{card})

	m, cmd := pressTestKey(t, m, 'm', "m")
	if cmd == nil {
		t.Fatal("BookmarkAdded returned no reconcile command")
	}
	msg, ok := cmd().(bookmarkBaselineMsg)
	if !ok {
		t.Fatalf("reconcile command message = %T, want bookmarkBaselineMsg", msg)
	}
	title := m.eng.Lib.TitleByRef(card.TitleRef)
	if msg.titleID != title.ID || msg.ref != card.TitleRef || msg.provisional != 12 || msg.maxEp != 10 || msg.err != nil {
		t.Fatalf("bookmarkBaselineMsg = %+v", msg)
	}
}

func TestBookmarkBaselineLowersProvisionalKnownEpisodes(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("bookmark-lower", 1)[0]
	if _, err := m.eng.Bookmark(ref, 12); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
	title := m.eng.Lib.TitleByRef(ref)
	if err := m.eng.Store.SaveEpisodes(ref, testEpisodes(10)); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	m.badges[title.ID] = 4

	m, _ = updateTestModel(t, m, bookmarkBaselineMsg{
		titleID: title.ID, ref: ref, provisional: 12, maxEp: 10,
	})

	if got := m.eng.Lib.EntryLookup(title.ID).KnownEpisodes; got != 10 {
		t.Fatalf("KnownEpisodes = %d, want 10", got)
	}
	if got := m.badges[title.ID]; got != 0 {
		t.Fatalf("badge after reconcile = %d, want 0", got)
	}
}

func TestBookmarkBaselinePreservesAddedStatus(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("bookmark-status", 1)[0]
	if _, err := m.eng.Bookmark(ref, 12); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
	title := m.eng.Lib.TitleByRef(ref)
	m.showHome()
	m.status = i18n.TuiBookmarkAdded

	m, _ = updateTestModel(t, m, bookmarkBaselineMsg{
		titleID: title.ID, ref: ref, provisional: 12, maxEp: 10,
	})

	if m.status != i18n.TuiBookmarkAdded {
		t.Fatalf("status after bookmark baseline = %q, want %q", m.status, i18n.TuiBookmarkAdded)
	}
}

func TestBookmarkBaselineWaitsForPlaybackToFinish(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("bookmark-playing", 1)[0]
	if _, err := m.eng.Bookmark(ref, 12); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}
	title := m.eng.Lib.TitleByRef(ref)
	if err := m.eng.Store.SaveEpisodes(ref, testEpisodes(10)); err != nil {
		t.Fatalf("save episodes: %v", err)
	}
	m.screen = screenPlaying
	m.playCancel = func() {}
	msg := bookmarkBaselineMsg{titleID: title.ID, ref: ref, provisional: 12, maxEp: 10}

	m, _ = updateTestModel(t, m, msg)
	if m.pendingBaseline == nil {
		t.Fatal("baseline message was not stashed during playback")
	}
	if got := m.eng.Lib.EntryLookup(title.ID).KnownEpisodes; got != 12 {
		t.Fatalf("KnownEpisodes during playback = %d, want 12", got)
	}

	m, _ = updateTestModel(t, m, playDoneMsg{})
	if m.pendingBaseline != nil {
		t.Fatal("pending baseline was not cleared after playback")
	}
	if got := m.eng.Lib.EntryLookup(title.ID).KnownEpisodes; got != 10 {
		t.Fatalf("KnownEpisodes after playback = %d, want 10", got)
	}
}

// TestBadgeShrinksAfterWatch — переглянув нові серії, повернувся на домівку —
// бейдж зник. Домівка перебудовується з showHome, тож окремого оновлення не треба.
func TestBadgeShrinksAfterWatch(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("watched", 2)
	seedBadgeModel(t, &m, refs, 5, 3)
	if libraryRow(t, m, refs[0].Name).badge == "" {
		t.Fatal("no badge before watching")
	}

	// Позначаємо серії 4 і 5 переглянутими датою старішою за прогрес другого
	// тайтла: інакше тайтл переїхав би в «ПРОДОВЖИТИ» і бейдж зник би не тому.
	at := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	m.eng.Lib.RecordPosition(refs[0].Slug, 4, 1400, 1500, at)
	m.eng.Lib.RecordPosition(refs[0].Slug, 5, 1400, 1500, at)
	m.showHome()

	if got := libraryRow(t, m, refs[0].Name).badge; got != "" {
		t.Fatalf("badge after watching every episode = %q, want empty", got)
	}

	// А фонова перевірка, що знайшла ще одну серію, бейдж повертає.
	m, _ = updateTestModel(t, m, badgesMsg{counts: map[string]int{refs[0].Slug: 1}})
	if want := i18n.NewEpisodes(1); libraryRow(t, m, refs[0].Name).badge != want {
		t.Fatalf("badge after badgesMsg = %q, want %q", libraryRow(t, m, refs[0].Name).badge, want)
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
