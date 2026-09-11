package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"context"
	"fmt"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"strings"
	"testing"
)

func TestUsabilityHomeBookmarksAndRemoveSelection(t *testing.T) {
	m := newTestModel(t)
	seedTestPlanned(&m, testRefs("book", 9))
	n := 0
	for _, v := range m.list.Items() {
		if v.(item).role == "lib" {
			n++
		}
	}
	if n != 5 {
		t.Fatalf("home bookmarks=%d want5", n)
	}
	selectTestItem(t, &m, func(i item) bool { return i.key() == "bookmarks" })
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if len(m.list.Items()) != 9 {
		t.Fatalf("all bookmarks=%d", len(m.list.Items()))
	}
	m.list.Select(3)
	next := m.list.Items()[4].(item).key()
	m, _ = pressTestKey(t, m, 'm', "m")
	if m.list.SelectedItem().(item).key() != next {
		t.Fatal("removal lost next visible selection")
	}
}
func TestUsabilityHistoryAllFilterAndMore(t *testing.T) {
	m := newTestModel(t)
	refs := make([]provider.TitleRef, 300)
	for i := range refs {
		refs[i] = provider.TitleRef{Provider: "test", Slug: fmt.Sprintf("t%03d", i), Name: fmt.Sprintf("Title %03d", i)}
	}
	seedTestHistory(&m, refs)
	m.showHistory()
	if len(m.list.Items()) != 21 {
		t.Fatalf("history rows=%d want20+more", len(m.list.Items()))
	}
	m.list.Select(20)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if len(m.list.Items()) != 41 || m.list.Index() != 20 {
		t.Fatal("More should expand/select first new")
	}
	m = applyTestListFilter(t, m, "Title 001")
	if len(m.list.VisibleItems()) != 1 {
		t.Fatalf("filter missed old title: %d", len(m.list.VisibleItems()))
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.list.FilterState() != list.Unfiltered || len(m.list.Items()) < 41 {
		t.Fatal("cancel lost history page")
	}
}
func TestUsabilityHelpOwnsEscapeOverPending(t *testing.T) {
	m := newTestModel(t)
	f := m.snapshot()
	m.pending = &f
	m.pendingReq = 1
	m.reqID = 1
	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.pending == nil || m.reqID != 1 {
		t.Fatal("help escape canceled navigation")
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.pending != nil {
		t.Fatal("second escape must cancel navigation")
	}
}
func TestUsabilityHelpOwnsEscapeOverPlaying(t *testing.T) {
	m := newTestModel(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.playCancel = cancel
	m.screen = screenPlaying
	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if ctx.Err() != nil {
		t.Fatal("help escape stopped player")
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if ctx.Err() == nil {
		t.Fatal("second escape should stop")
	}
}
func TestUsabilityFilteredHistoryBackAndStaleResults(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("restore", 30))
	m.showHistory()
	m = applyTestListFilter(t, m, "restore 1")
	key := m.selectedKey()
	model, _ := m.openTitle(m.list.SelectedItem().(item).payload.(payloadResume).ref)
	m = model.(Model)
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: m.ref, eps: testEpisodes(1), req: m.reqID, purpose: epsOpen})
	var cmd tea.Cmd
	m, cmd = pressTestKey(t, m, tea.KeyEsc, "")
	msg, ok := filterMatchesFromCmd(cmd)
	if !ok {
		t.Fatal("missing asynchronous restoration")
	}
	m, _ = updateTestModel(t, m, msg)
	if m.list.FilterState() != list.FilterApplied || m.list.FilterValue() != "restore 1" || m.selectedKey() != key || len(m.list.VisibleItems()) != 1 {
		t.Fatalf("restore: state%v filter%q key%q", m.list.FilterState(), m.list.FilterValue(), m.selectedKey())
	}
}

func TestUsabilitySuccessExpiryDoesNotClearLoading(t *testing.T) {
	m := newTestModel(t)
	_ = m.success("saved")
	gen := m.statusGen
	updated, _ := m.openTitle(testRefs("load", 1)[0])
	m = updated.(Model)
	m, _ = updateTestModel(t, m, statusExpiredMsg{gen})
	if m.status == "" {
		t.Fatal("old success expired current loading")
	}
}
func TestUsabilityStaleFilterCannotReplaceHelpRows(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("stale", 30))
	m.showHistory()
	m, _ = pressTestKey(t, m, '/', "/")
	var cmd tea.Cmd
	m, cmd = pressTestKey(t, m, 's', "s")
	stale := cmd()
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	m, _ = pressTestKey(t, m, '?', "?")
	before := m.list.SelectedItem().(item).title
	m, _ = updateTestModel(t, m, stale)
	if m.list.SelectedItem().(item).title != before {
		t.Fatal("old filter replaced help selection")
	}
}
func TestUsabilityOldFilterCannotReplaceNewFilter(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("stale", 30))
	m.showHistory()
	m, _ = pressTestKey(t, m, '/', "/")
	var cmd tea.Cmd
	m, cmd = pressTestKey(t, m, '1', "1")
	stale, _ := filterMatchesFromCmd(cmd)
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	m = applyTestListFilter(t, m, "stale 2")
	before := m.selectedKey()
	m, _ = updateTestModel(t, m, stale)
	if m.selectedKey() != before {
		t.Fatal("old filter replaced newer filter")
	}
}
func TestUsabilitySearchCompletionClosesHelp(t *testing.T) {
	m := newTestModel(t)
	m = openTestSearch(t, m)
	m, _ = launchTestSearch(t, m, "query")
	m, _ = pressTestKey(t, m, '?', "?")
	if m.overlay != overlayHelp {
		t.Fatal("no help")
	}
	m, _ = updateTestModel(t, m, searchDoneMsg{req: m.reqID, cards: testCards("result", 2), page: 1})
	if m.overlay != overlayNone {
		t.Fatal("search completion left help above results")
	}
}
func TestUsabilityBackRebuildsHistoryAndUsesVisibleFallback(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("freshhistory", 10))
	m.showHistory()
	m = applyTestListFilter(t, m, "freshhistory")
	m.list.Select(3)
	selected := m.list.SelectedItem().(item).payload.(payloadResume)
	want := m.list.VisibleItems()[4].(item).key()
	model, _ := m.openTitle(selected.ref)
	m = model.(Model)
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: m.ref, eps: testEpisodes(1), req: m.reqID, purpose: epsOpen})
	for i, title := range m.eng.Lib.Titles {
		if title.ID == selected.ref.Slug {
			m.eng.Lib.Titles = append(m.eng.Lib.Titles[:i], m.eng.Lib.Titles[i+1:]...)
			break
		}
	}
	var cmd tea.Cmd
	m, cmd = pressTestKey(t, m, tea.KeyEsc, "")
	msg, ok := filterMatchesFromCmd(cmd)
	if !ok {
		t.Fatal("no restoration filter")
	}
	m, _ = updateTestModel(t, m, msg)
	if m.selectedKey() != want || len(m.list.VisibleItems()) != 9 {
		t.Fatal("back restored obsolete rows or wrong fallback")
	}
}
func TestUsabilitySuccessExpiresAndBackgroundRefreshKeepsGeneration(t *testing.T) {
	m := newTestModel(t)
	seedTestPlanned(&m, testRefs("feedback", 1))
	_ = m.success("saved")
	gen := m.statusGen
	m, _ = updateTestModel(t, m, libraryEpisodesMsg{})
	if m.statusGen != gen || m.statusKind != statusSuccess {
		t.Fatal("background refresh altered feedback generation")
	}
	m, _ = updateTestModel(t, m, statusExpiredMsg{gen})
	if m.status != "" {
		t.Fatal("success did not expire")
	}
}
func TestUsabilityHelpDescribesSettingsAndRecentQueryKeys(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenSettings
	has := func(key string) bool {
		for _, a := range m.actions() {
			if a.key == key {
				return true
			}
		}
		return false
	}
	if !has("←/→") {
		t.Fatal("settings help omitted cycling")
	}
	m.screen = screenSearch
	m.cards = nil
	m.input.Blur()
	if !has("X") {
		t.Fatal("recent queries help omitted removal")
	}
}
func TestUsabilityRemovingLastBookmarkShowsLocalizedEmptyState(t *testing.T) {
	m := newTestModel(t)
	seedTestPlanned(&m, testRefs("last", 1))
	m.showBookmarks()
	m, _ = pressTestKey(t, m, 'm', "m")
	m, _ = updateTestModel(t, m, statusExpiredMsg{m.statusGen})
	if !strings.Contains(m.View().Content, i18n.TuiBookmarksEmpty) {
		t.Fatal("empty bookmarks has no localized state")
	}
}

func TestUsabilityFastAcceptKeepsOutstandingFilterResult(t *testing.T) {
	for _, key := range []rune{tea.KeyEnter, tea.KeyLeft} {
		t.Run(fmt.Sprint(key), func(t *testing.T) {
			m := newTestModel(t)
			seedTestHistory(&m, testRefs("accept", 30))
			m.showHistory()
			m, _ = pressTestKey(t, m, '/', "/")
			var cmd tea.Cmd
			m, cmd = pressTestKey(t, m, '1', "1")
			outstanding, ok := filterMatchesFromCmd(cmd)
			if !ok {
				t.Fatal("no pending filter")
			}
			m, _ = pressTestKey(t, m, key, "")
			m, _ = updateTestModel(t, m, outstanding)
			if len(m.list.VisibleItems()) != 1 || m.list.VisibleItems()[0].(item).payload.(payloadResume).ref.Slug != "accept-1" {
				t.Fatalf("key%v discarded current query: %d rows", key, len(m.list.VisibleItems()))
			}
		})
	}
}

func TestUsabilityFastAcceptAfterEmptyResultKeepsCurrentQuery(t *testing.T) {
	m := newTestModel(t)
	seedTestHistory(&m, testRefs("accept", 30))
	m.showHistory()
	m, _ = pressTestKey(t, m, '/', "/")
	var cmd tea.Cmd
	m, cmd = pressTestKey(t, m, 'z', "z")
	msg, _ := filterMatchesFromCmd(cmd)
	m, _ = updateTestModel(t, m, msg)
	m, cmd = pressTestKey(t, m, tea.KeyBackspace, "")
	_, _ = filterMatchesFromCmd(cmd)
	m, cmd = pressTestKey(t, m, '1', "1")
	pending, _ := filterMatchesFromCmd(cmd)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	m, _ = updateTestModel(t, m, pending)
	if m.list.FilterState() != list.FilterApplied || m.list.FilterValue() != "1" || len(m.list.VisibleItems()) != 1 {
		t.Fatalf("accept lost query after old empty result: state%v query%q rows%d", m.list.FilterState(), m.list.FilterValue(), len(m.list.VisibleItems()))
	}
}
