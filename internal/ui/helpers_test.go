package ui

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

type stubExtractor struct{}

// fakePlayer існує лише щоб Begin не відмовляв: тести не запускають фонову
// команду відтворення, тому Start ніколи не викликається.
type fakePlayer struct{}

func (fakePlayer) ID() string { return "fake" }
func (fakePlayer) Command(string, string, map[string]string, float64) *exec.Cmd {
	return nil
}

func (fakePlayer) Start(context.Context, string, string, map[string]string, float64) (player.Session, error) {
	return nil, errs.ErrPlayer
}

// Провайдер у тестах — providertest.Stub; конструктори лише вкладають у нього
// потрібну відповідь.
func episodesStub(eps []provider.Episode) providertest.Stub {
	return providertest.Stub{
		IDValue:   "test",
		NameValue: "Test",
		EpisodesFn: func(context.Context, provider.TitleRef) ([]provider.Episode, error) {
			return eps, nil
		},
	}
}

func sourcesStub(sources []provider.Source) providertest.Stub {
	return providertest.Stub{
		IDValue:   "test",
		NameValue: "Test",
		SourcesFn: func(context.Context, provider.TitleRef, int) ([]provider.Source, error) {
			return sources, nil
		},
	}
}

func (stubExtractor) ID() string { return "stub" }
func (stubExtractor) Handles(string) bool {
	return true
}
func (stubExtractor) Extract(context.Context, string, string) ([]extractor.Stream, error) {
	return []extractor.Stream{{URL: "https://stream.invalid/video.m3u8"}}, nil
}

func newTestModel(t *testing.T) Model {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	eng := &playback.Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{}}
	return New(eng)
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

func spacerCount(items []item) int {
	count := 0
	for _, it := range items {
		if it.spacer {
			count++
		}
	}
	return count
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
