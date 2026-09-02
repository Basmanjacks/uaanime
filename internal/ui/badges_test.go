package ui

import (
	"testing"
	"time"

	"charm.land/bubbles/v2/list"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

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
	m.eng.Provider = episodesStub(testEpisodes(4))
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
	m.eng.Provider = episodesStub(testEpisodes(4))
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
	m.eng.Provider = episodesStub([]provider.Episode{{Number: 1}, {Number: 10}})
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
