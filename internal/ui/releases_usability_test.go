package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUsabilityBootstrapOldCacheBeforeRefresh(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("old", 1)[0]
	seedTestPlanned(&m, []provider.TitleRef{ref})
	old := []provider.Episode{{Number: 1, Releases: []provider.Release{{Studio: "A", Kind: provider.KindDub}}}}
	_ = m.eng.Store.SaveEpisodes(ref, old)
	m.eng.Provider = episodesStub(append(old, provider.Episode{Number: 2, Releases: old[0].Releases}))
	m = New(m.eng, Options{})
	cmd := m.bootstrapCmd()
	if cmd == nil {
		t.Fatal("missing bootstrap")
	}
	msg := cmd()
	if m.eng.Lib.Entries[0].ReleaseBaseline != nil {
		t.Fatal("command mutated library")
	}
	m, _ = updateTestModel(t, m, msg)
	if m.eng.Lib.Entries[0].ReleaseBaseline == nil {
		t.Fatal("old metadata was not seeded")
	}
	m.eng.Lib.Entries[0].StudioPin = "A"
	news := m.eng.Lib.PreferredFresh(ref.Slug, append(old, provider.Episode{Number: 2, Releases: old[0].Releases}), library.Prefs{})
	if news.Studio != "A" || news.Preferred != 1 {
		t.Fatalf("news %+v", news)
	}
}
func TestUsabilityBadgeSchedulerFreshDoesNotConsumeBudget(t *testing.T) {
	m := newTestModel(t)
	refs := make([]provider.TitleRef, 45)
	for i := range refs {
		refs[i] = provider.TitleRef{Provider: "test", Slug: fmt.Sprintf("title-%03d", i)}
	}
	seedTestPlanned(&m, refs)
	for _, ref := range refs[:25] {
		_ = m.eng.Store.SaveEpisodes(ref, testEpisodes(1))
	}
	var calls atomic.Int32
	m.eng.Provider = episodesStub(nil)
	p := episodesStub(nil)
	p.EpisodesFn = func(context.Context, provider.TitleRef) ([]provider.Episode, error) { calls.Add(1); return nil, nil }
	m.eng.Provider = p
	cmd := m.libraryEpisodesCmd()
	if cmd == nil {
		t.Fatal("no scheduler")
	}
	_ = cmd()
	if calls.Load() != 20 {
		t.Fatalf("calls=%d want20", calls.Load())
	}
	if m.libraryEpisodesCmd() != nil {
		t.Fatal("scheduler budget restarted")
	}
}
func TestUsabilityBulkFilteredZeroAndUndo(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("bulk", 1)[0]
	m.episodesRef = m.ref
	m.episodes = []provider.Episode{{Number: 0}, {Number: 2}, {Number: 7}}
	_ = m.showEpisodes()
	m = applyTestListFilter(t, m, "7")
	m, _ = pressTestKey(t, m, 'W', "W")
	title := m.eng.Lib.TitleByRef(m.ref)
	if title == nil {
		t.Fatal("bulk did not create title")
	}
	for _, n := range []int{0, 2} {
		p := m.eng.Lib.ProgressFor(title.ID, n)
		if p == nil || !p.Completed {
			t.Fatalf("episode%d not marked", n)
		}
	}
	if !m.eng.WatchedUndoAvailable(m.undo) {
		t.Fatal("missing undo")
	}
	m, _ = pressTestKey(t, m, 'u', "u")
	if !m.eng.WatchedUndoAvailable(m.undo) {
		t.Fatal("lowercase u consumed undo")
	}
	m, _ = pressTestKey(t, m, 'U', "U")
	if m.eng.Lib.TitleByRef(m.ref) != nil {
		t.Fatal("undo did not restore missing title")
	}
}

var _ = playback.ReleaseSeed{}

func TestUsabilityBootstrapDefersLatestForegroundAndKeepsRemoval(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("gate", 2)
	seedTestPlanned(&m, refs)
	for _, ref := range refs {
		_ = m.eng.Store.SaveEpisodes(ref, testEpisodes(1))
	}
	m.eng.Provider = episodesStub(testEpisodes(2))
	m = New(m.eng, Options{})
	bootstrap := m.bootstrapCmd()
	m.reqID = 1
	if m.episodesCmd(refs[0], 1, epsOpen) != nil {
		t.Fatal("cache writer ran before bootstrap")
	}
	m.reqID = 2
	if m.episodesCmd(refs[1], 2, epsOpen) != nil {
		t.Fatal("second writer ran before bootstrap")
	}
	m.eng.Lib.Entries = m.eng.Lib.Entries[1:]
	m, _ = updateTestModel(t, m, bootstrap())
	if m.bootstrapPending || m.deferredEpisodes != nil {
		t.Fatal("gate did not open")
	}
	if len(m.eng.Lib.Entries) != 1 || m.eng.Lib.Entries[0].TitleID != refs[1].Slug {
		t.Fatal("bootstrap recreated removed entry")
	}
	if m.episodesCmd(refs[1], 2, epsOpen) == nil {
		t.Fatal("gate stayed closed")
	}
}
func TestUsabilityBootstrapSaveFailureDisablesOnlyAffectedNews(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(&playback.Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{}}, Options{})
	ref := testRefs("failure", 1)[0]
	seedTestPlanned(&m, []provider.TitleRef{ref})
	_ = st.SaveEpisodes(ref, testEpisodes(1))
	m.eng.Provider = episodesStub(nil)
	m = New(m.eng, Options{})
	if err := os.Mkdir(filepath.Join(dir, "library.json"), 0700); err != nil {
		t.Fatal(err)
	}
	m, _ = updateTestModel(t, m, m.bootstrapCmd()())
	if !m.baselineWarning || !m.newsDisabled[ref.Slug] || m.eng.Lib.Entries[0].ReleaseBaseline != nil || m.bootstrapPending {
		t.Fatal("failed bootstrap published state or blocked app")
	}
	_ = os.Remove(filepath.Join(dir, "library.json"))
	m, _ = updateTestModel(t, m, libraryEpisodesMsg{seeds: []playback.ReleaseSeed{{Ref: ref, Episodes: testEpisodes(2)}}})
	if m.eng.Lib.Entries[0].ReleaseBaseline != nil {
		t.Fatal("failed baseline retried this run")
	}
	m = New(m.eng, Options{})
	m, _ = updateTestModel(t, m, m.bootstrapCmd()())
	if m.eng.Lib.Entries[0].ReleaseBaseline == nil {
		t.Fatal("next launch did not retry")
	}
}
func TestUsabilityNoCacheEmptySuccessSeedsButErrorsDoNot(t *testing.T) {
	for _, failed := range []bool{false, true} {
		m := newTestModel(t)
		ref := testRefs("empty", 1)[0]
		seedTestPlanned(&m, []provider.TitleRef{ref})
		m.eng.Provider = episodesStub(nil)
		m = New(m.eng, Options{})
		m, _ = updateTestModel(t, m, m.bootstrapCmd()())
		m.reqID = 3
		msg := episodesDoneMsg{ref: ref, req: 3}
		if failed {
			msg.err = errors.New("offline")
		}
		m, _ = updateTestModel(t, m, msg)
		if (m.eng.Lib.Entries[0].ReleaseBaseline != nil) == failed {
			t.Fatalf("baseline after failed=%v", failed)
		}
	}
}
func TestUsabilitySchedulerBoundedFairCursorAndNoRepetition(t *testing.T) {
	m := newTestModel(t)
	refs := make([]provider.TitleRef, 45)
	for i := range refs {
		refs[i] = provider.TitleRef{Provider: "test", Slug: fmt.Sprintf("fair-%03d", i)}
	}
	seedTestPlanned(&m, refs)
	var active, maximum atomic.Int32
	var mu sync.Mutex
	seen := map[string]int{}
	p := episodesStub(nil)
	p.EpisodesFn = func(ctx context.Context, ref provider.TitleRef) ([]provider.Episode, error) {
		n := active.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		defer active.Add(-1)
		mu.Lock()
		seen[ref.Slug]++
		mu.Unlock()
		select {
		case <-time.After(time.Millisecond):
		case <-ctx.Done():
		}
		return nil, errors.New("offline")
	}
	m.eng.Provider = p
	_ = m.libraryEpisodesCmd()()
	first := m.eng.Store.LoadBadgeCursor()
	if first != "test/fair-019" {
		t.Fatalf("first cursor %q", first)
	}
	m = New(m.eng, Options{})
	m, _ = updateTestModel(t, m, m.bootstrapCmd()())
	cmd := m.libraryEpisodesCmd()
	if cmd != nil {
		t.Fatal("bootstrap already reserved run budget")
	}
	// Reconstruct one more run and directly drive its returned scheduler command.
	m = New(m.eng, Options{})
	var next tea.Cmd
	m, next = updateTestModel(t, m, m.bootstrapCmd()())
	for _, msg := range collectMsgs(next, time.Second) {
		m, _ = updateTestModel(t, m, msg)
	}
	if maximum.Load() > 4 || len(seen) != 40 {
		t.Fatalf("workers%d unique%d", maximum.Load(), len(seen))
	}
	for _, n := range seen {
		if n != 1 {
			t.Fatal("cursor repeated failed candidate")
		}
	}
}
func TestUsabilityBulkUndoFailureRetainsToken(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	m := New(&playback.Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{}}, Options{})
	m.ref = testRefs("undo-fail", 1)[0]
	m.episodesRef = m.ref
	m.episodes = []provider.Episode{{Number: 0}, {Number: 2}, {Number: 7}}
	_ = m.showEpisodes()
	m.list.Select(2)
	m, _ = pressTestKey(t, m, 'W', "W")
	token := m.undo
	_ = os.Remove(filepath.Join(dir, "library.json"))
	_ = os.Mkdir(filepath.Join(dir, "library.json"), 0700)
	m, _ = pressTestKey(t, m, 'U', "U")
	if m.undo != token || !m.eng.WatchedUndoAvailable(token) || m.errText == "" {
		t.Fatal("failed undo lost token")
	}
	_ = os.Remove(filepath.Join(dir, "library.json"))
	m, _ = pressTestKey(t, m, 'U', "U")
	if m.undo != nil || len(m.eng.Lib.Progress) != 0 {
		t.Fatal("undo retry failed")
	}
}

func TestUsabilityLateBookmarkResponseRefreshesFilteredFullList(t *testing.T) {
	m := newTestModel(t)
	cards := testCards("late-bookmark", 2)
	ref := cards[1].TitleRef
	m.eng.Provider = episodesStub(testEpisodes(4))
	if err := m.eng.Store.SaveEpisodes(ref, testEpisodes(1)); err != nil {
		t.Fatal(err)
	}
	searchTestCards(&m, cards)
	m.list.Select(1)
	m, _ = pressTestKey(t, m, 'm', "m")
	title := m.eng.Lib.TitleByRef(ref)
	late := m.bookmarkBaselineCmd(title.ID, ref, 1)
	m.showHome()
	selectTestItem(t, &m, func(it item) bool { _, ok := it.payload.(payloadBookmarks); return ok })
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	m = applyTestListFilter(t, m, ref.Name)
	key := m.selectedKey()
	before := m.list.SelectedItem().(item).meta
	var cmd tea.Cmd
	m, cmd = updateTestModel(t, m, late())
	if msg, ok := filterMatchesFromCmd(cmd); ok {
		m, _ = updateTestModel(t, m, msg)
	}
	after := m.list.SelectedItem().(item).meta
	if after == before || !strings.Contains(after, i18n.Episodes(4)) {
		t.Fatalf("late response left old metadata: before%q after%q", before, after)
	}
	if m.screen != screenBookmarks || m.list.FilterState() != list.FilterApplied || m.list.FilterValue() != ref.Name || m.selectedKey() != key {
		t.Fatal("late response lost full-list filter or selected identity")
	}
}
