package ui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
)

// refreshStub — провайдер із каталогом і списком серій на кожен тайтл;
// calls рахує звернення за серіями.
func refreshStub(eps func(ref provider.TitleRef) ([]provider.Episode, error), calls *atomic.Int32) providertest.Stub {
	return providertest.Stub{
		IDValue:   "test",
		NameValue: "Test",
		CapsValue: provider.Caps{Catalog: true},
		EpisodesFn: func(_ context.Context, ref provider.TitleRef) ([]provider.Episode, error) {
			if calls != nil {
				calls.Add(1)
			}
			return eps(ref)
		},
		CatalogFn: func(_ context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error) {
			return []provider.TitleCard{{TitleRef: provider.TitleRef{Provider: "test", Slug: "cat-" + string(kind), Name: "Каталог " + string(kind)}}}, nil
		},
	}
}

// refreshModel — домівка з count тайтлами «у планах», базова лінія 10 серій і
// свіжий кеш на 10 серій: без новинок бейджів немає.
func refreshModel(t *testing.T, count int) (Model, []provider.TitleRef) {
	t.Helper()
	m := newTestModel(t)
	refs := testRefs("fresh", count)
	seedTestPlanned(&m, refs)
	for i, ref := range refs {
		m.eng.Lib.Entries[i].KnownEpisodes = 10
		if err := m.eng.Store.SaveEpisodes(ref, testEpisodes(10)); err != nil {
			t.Fatal(err)
		}
	}
	m.showHome()
	return m, refs
}

// runMsgs виконує команду (і всі команди пакета) й повертає повідомлення.
func runMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, runMsgs(c)...)
	}
	return out
}

func TestLibraryEpisodesMsgReleasesScheduler(t *testing.T) {
	m, _ := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, nil)
	for _, ref := range m.visibleRefs() {
		_ = m.eng.Store.SaveEpisodes(ref, nil) // кеш є, але порожній — пробіг має що робити
	}
	cmd := m.libraryEpisodesCmd()
	if cmd == nil {
		t.Fatal("first run not scheduled")
	}
	msg := cmd()
	if m.libraryEpisodesCmd() != nil {
		t.Fatal("second run scheduled while the first is in flight")
	}
	m, _ = updateTestModel(t, m, msg)
	if m.libraryEpisodesCmd() == nil {
		t.Fatal("scheduler stayed latched after libraryEpisodesMsg")
	}
}

func TestRefreshTickRunsOnlyWhenIdle(t *testing.T) {
	m, _ := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, nil)
	m.refreshEvery = time.Hour

	m.playCancel = func() {}
	m, cmd := updateTestModel(t, m, refreshTickMsg{})
	if cmd == nil {
		t.Fatal("tick during playback did not re-arm")
	}
	if m.badgeScheduled.Load() {
		t.Fatal("tick during playback scheduled a library probe")
	}
	m.playCancel = nil

	m, cmd = updateTestModel(t, m, refreshTickMsg{})
	if cmd == nil {
		t.Fatal("idle tick returned nil")
	}
	if !m.badgeScheduled.Load() {
		t.Fatal("idle tick did not schedule the library probe")
	}

	if m.refreshTickCmd() == nil {
		t.Fatal("refresh tick not armed with a provider and refreshEvery > 0")
	}
	m.refreshEvery = 0
	if m.refreshTickCmd() != nil {
		t.Fatal("refreshEvery=0 still arms the tick")
	}
}

func TestRefreshAllCmdCoversWholeLibraryAndCatalog(t *testing.T) {
	m, _ := refreshModel(t, 25)
	var calls atomic.Int32
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, &calls)

	msg, ok := m.refreshAllCmd(1)().(refreshDoneMsg)
	if !ok {
		t.Fatal("refreshAllCmd returned a foreign message")
	}
	if calls.Load() != 25 {
		t.Fatalf("provider calls = %d, want 25 (fresh cache must not skip titles)", calls.Load())
	}
	if len(msg.seeds) != 25 || msg.err != nil {
		t.Fatalf("seeds=%d err=%v", len(msg.seeds), msg.err)
	}
	for _, kind := range catalogKinds {
		if len(msg.catalog[kind]) != 1 {
			t.Errorf("catalog %q not refreshed", kind)
		}
	}

	p := refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, nil)
	p.CatalogFn = func(context.Context, provider.CatalogKind) ([]provider.TitleCard, error) {
		return nil, fmt.Errorf("catalog: %w", errs.ErrProvider)
	}
	m.eng.Provider = p
	if msg := m.refreshAllCmd(2)().(refreshDoneMsg); !errors.Is(msg.err, errs.ErrProvider) {
		t.Fatalf("catalog failure lost: err=%v", msg.err)
	}
}

func TestRefreshKeyHomeReportsNewEpisodes(t *testing.T) {
	m, refs := refreshModel(t, 2)
	m.eng.Provider = refreshStub(func(ref provider.TitleRef) ([]provider.Episode, error) {
		if ref.Same(refs[0]) {
			return testEpisodes(12), nil
		}
		return testEpisodes(10), nil
	}, nil)

	m, cmd := pressTestKey(t, m, 'r', "r")
	if m.status != i18n.TuiRefreshing || m.statusKind != statusLoading {
		t.Fatalf("status after r = %q/%v", m.status, m.statusKind)
	}
	if m2, _ := pressTestKey(t, m, 'r', "r"); m2.status != i18n.TuiRefreshingAlready {
		t.Fatalf("second r status = %q", m2.status)
	}
	msgs := runMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatalf("r on home returned %d messages, want 1", len(msgs))
	}
	m, _ = updateTestModel(t, m, msgs[0])
	if want := fmt.Sprintf(i18n.TuiRefreshedNews, i18n.NewEpisodes(2)); m.status != want || m.statusKind != statusSuccess {
		t.Fatalf("status = %q/%v, want %q", m.status, m.statusKind, want)
	}
	if got := libraryRow(t, m, refs[0].Name).badge; got != i18n.NewEpisodes(2) {
		t.Fatalf("badge = %q, want %q", got, i18n.NewEpisodes(2))
	}
	if m.refreshBusy != 0 {
		t.Fatal("refreshBusy not released by its own reply")
	}
	if !hasSection(t, m, catalogBlockTitle(provider.CatalogFresh)) {
		t.Fatal("catalog block not applied from the refresh")
	}
}

func TestRefreshKeyHomeNoNewsAndErrors(t *testing.T) {
	m, _ := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, nil)
	m, cmd := pressTestKey(t, m, 'r', "r")
	m, _ = updateTestModel(t, m, runMsgs(cmd)[0])
	if m.status != i18n.TuiRefreshedNone {
		t.Fatalf("status = %q, want %q", m.status, i18n.TuiRefreshedNone)
	}

	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) {
		return nil, fmt.Errorf("dial: %w", errs.ErrOffline)
	}, nil)
	m, cmd = pressTestKey(t, m, 'r', "r")
	m, _ = updateTestModel(t, m, runMsgs(cmd)[0])
	if m.errText != m.errorText(errs.ErrOffline) || m.status != "" {
		t.Fatalf("offline: errText=%q status=%q", m.errText, m.status)
	}

	// порожня бібліотека: оновлюється лише каталог, статус — просто «Оновлено»
	empty := newTestModel(t)
	empty.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return nil, nil }, nil)
	empty.showHome()
	empty, cmd = pressTestKey(t, empty, 'r', "r")
	if cmd == nil {
		t.Fatal("r on an empty library did nothing (catalog should still refresh)")
	}
	empty, _ = updateTestModel(t, empty, runMsgs(cmd)[0])
	if empty.status != i18n.TuiRefreshed {
		t.Fatalf("empty library status = %q, want %q", empty.status, i18n.TuiRefreshed)
	}
}

func TestRefreshKeyEpisodesScreen(t *testing.T) {
	m, refs := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(12), nil }, nil)
	m.ref, m.episodesRef, m.episodes = refs[0], refs[0], testEpisodes(11)
	_ = m.showEpisodes()
	m.list.Select(3)

	m, cmd := pressTestKey(t, m, 'r', "r")
	if m.status != i18n.TuiRefreshing {
		t.Fatalf("status = %q", m.status)
	}
	msgs := runMsgs(cmd)
	done, ok := msgs[0].(episodesDoneMsg)
	if !ok || done.purpose != epsManual {
		t.Fatalf("r on episodes returned %#v", msgs[0])
	}
	m, _ = updateTestModel(t, m, done)
	if len(m.list.Items()) != 12 || m.list.Index() != 3 {
		t.Fatalf("items=%d index=%d, want 12 rows with the cursor kept", len(m.list.Items()), m.list.Index())
	}
	if want := i18n.EpisodesAppeared([]int{12}); m.status != want {
		t.Fatalf("status = %q, want %q", m.status, want)
	}

	// та сама відповідь без новинок і з помилкою
	m, cmd = pressTestKey(t, m, 'r', "r")
	m, _ = updateTestModel(t, m, runMsgs(cmd)[0])
	if m.status != i18n.TuiRefreshedNone {
		t.Fatalf("status = %q, want %q", m.status, i18n.TuiRefreshedNone)
	}
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) {
		return nil, fmt.Errorf("parse: %w", errs.ErrProvider)
	}, nil)
	m, cmd = pressTestKey(t, m, 'r', "r")
	// відкладений кадр з'являється вже після старту: помилка не має його викинути
	snap := m.snapshot()
	m.pending = &snap
	m, _ = updateTestModel(t, m, runMsgs(cmd)[0])
	if m.errText != m.errorText(errs.ErrProvider) || m.screen != screenEpisodes || len(m.list.Items()) != 12 || m.pending == nil {
		t.Fatalf("provider error: errText=%q screen=%v rows=%d pending=%v", m.errText, m.screen, len(m.list.Items()), m.pending != nil)
	}
}

// afterPlay — playDoneMsg на екрані «Грає» і команда тихої перевірки списку.
func afterPlay(t *testing.T, m Model, fresh []provider.Episode) (Model, episodesDoneMsg) {
	t.Helper()
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return fresh, nil }, nil)
	m.screen, m.playCancel, m.pendingEp = screenPlaying, func() {}, 11
	m, cmd := updateTestModel(t, m, playDoneMsg{})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after playback = %v", m.screen)
	}
	for _, msg := range runMsgs(cmd) {
		if done, ok := msg.(episodesDoneMsg); ok && done.purpose == epsAfterPlay {
			return m, done
		}
	}
	t.Fatal("no epsAfterPlay request after playback")
	return m, episodesDoneMsg{}
}

func TestAfterPlayRefreshShowsNewEpisode(t *testing.T) {
	m, refs := refreshModel(t, 1)
	m.ref, m.episodesRef, m.episodes = refs[0], refs[0], testEpisodes(11)
	m, done := afterPlay(t, m, testEpisodes(12))
	if m.refreshBusy == 0 {
		t.Fatal("after-play refresh not marked busy")
	}
	m, _ = updateTestModel(t, m, done)
	if len(m.list.Items()) != 12 || m.status != i18n.EpisodesAppeared([]int{12}) {
		t.Fatalf("rows=%d status=%q", len(m.list.Items()), m.status)
	}
	if m.refreshBusy != 0 {
		t.Fatal("busy flag survived its own reply")
	}
}

func TestAfterPlayRefreshIsQuietWithoutNews(t *testing.T) {
	m, refs := refreshModel(t, 1)
	m.ref, m.episodesRef, m.episodes = refs[0], refs[0], testEpisodes(11)
	m, done := afterPlay(t, m, testEpisodes(11))
	m.status, m.statusKind = fmt.Sprintf(i18n.MsgProgressSaved, 14, 32), statusSuccess
	m, _ = updateTestModel(t, m, done)
	if m.status != fmt.Sprintf(i18n.MsgProgressSaved, 14, 32) {
		t.Fatalf("status overwritten: %q", m.status)
	}

	failed := done
	failed.eps, failed.err = nil, fmt.Errorf("dial: %w", errs.ErrOffline)
	m, _ = updateTestModel(t, m, failed)
	if m.errText != "" || len(m.list.Items()) != 11 {
		t.Fatalf("after-play failure must be silent: errText=%q rows=%d", m.errText, len(m.list.Items()))
	}
}

func TestAfterPlayRefreshDoesNotStealOverlayOrScreen(t *testing.T) {
	m, refs := refreshModel(t, 1)
	m.ref, m.episodesRef, m.episodes = refs[0], refs[0], testEpisodes(11)
	m, done := afterPlay(t, m, testEpisodes(12))
	m, _ = pressTestKey(t, m, '?', "?")
	if m.overlay != overlayHelp {
		t.Fatal("help overlay did not open")
	}
	m, _ = updateTestModel(t, m, done)
	if m.overlay != overlayHelp {
		t.Fatal("refresh reply closed the help overlay")
	}
	if len(m.episodes) != 12 {
		t.Fatal("fresh list not kept while the overlay is open")
	}

	// перехід під час оновлення: відповідь старого покоління не підміняє екран
	m, refs = refreshModel(t, 1)
	m.ref, m.episodesRef, m.episodes = refs[0], refs[0], testEpisodes(11)
	m, done = afterPlay(t, m, testEpisodes(12))
	m.back()
	if m.screen != screenHome {
		t.Fatalf("screen after back = %v", m.screen)
	}
	rows := len(m.list.Items())
	m, _ = updateTestModel(t, m, done)
	if m.screen != screenHome || len(m.list.Items()) != rows || m.status != "" || m.refreshBusy != 0 {
		t.Fatalf("stale reply touched the home screen: screen=%v rows=%d status=%q busy=%d", m.screen, len(m.list.Items()), m.status, m.refreshBusy)
	}
}

func TestRefreshKeyIgnoredWhenBusyOrBlocked(t *testing.T) {
	m, _ := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(10), nil }, nil)

	m.screen = screenPlaying
	m.playCancel = func() {}
	m, _ = pressTestKey(t, m, 'r', "r")
	if m.refreshBusy != 0 {
		t.Fatal("r during playback started a refresh")
	}
	m.screen, m.playCancel = screenHome, nil

	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, 'r', "r")
	if m.refreshBusy != 0 {
		t.Fatal("r inside the help overlay started a refresh")
	}
	_ = m.closeOverlay()

	m.bootstrapPending = true
	m, _ = pressTestKey(t, m, 'r', "r")
	if m.refreshBusy != 0 || m.status != "" {
		t.Fatalf("r during bootstrap: busy=%d status=%q", m.refreshBusy, m.status)
	}
	m.bootstrapPending = false

	m.badgeScheduled.Store(true)
	m, _ = pressTestKey(t, m, 'r', "r")
	if m.status != i18n.TuiRefreshingAlready || m.refreshBusy != 0 {
		t.Fatalf("r during a background probe: status=%q busy=%d", m.status, m.refreshBusy)
	}
}

func TestRefreshSurvivesPassiveUpdatesButNotNavigation(t *testing.T) {
	m, refs := refreshModel(t, 1)
	m.eng.Provider = refreshStub(func(provider.TitleRef) ([]provider.Episode, error) { return testEpisodes(12), nil }, nil)
	m, cmd := pressTestKey(t, m, 'r', "r")
	done := runMsgs(cmd)[0]
	// пасивні відповіді стартового пробігу перебудовують домівку, але не
	// роблять ручне оновлення застарілим
	m, _ = updateTestModel(t, m, catalogMsg{kind: provider.CatalogFresh, cards: []provider.TitleCard{{TitleRef: refs[0]}}})
	m, _ = updateTestModel(t, m, libraryEpisodesMsg{})
	if m.status != i18n.TuiRefreshing {
		t.Fatalf("passive update dropped the loading status: %q", m.status)
	}
	m, _ = updateTestModel(t, m, done)
	if want := fmt.Sprintf(i18n.TuiRefreshedNews, i18n.NewEpisodes(2)); m.status != want {
		t.Fatalf("status = %q, want %q", m.status, want)
	}

	// навігація під час оновлення: відповідь лише знімає зайнятість
	m, cmd = pressTestKey(t, m, 'r', "r")
	if m.status != i18n.TuiRefreshing {
		t.Fatalf("status = %q", m.status)
	}
	done = runMsgs(cmd)[0]
	m.back()
	m, _ = updateTestModel(t, m, done)
	if m.status != "" || m.refreshBusy != 0 {
		t.Fatalf("stale reply after back(): status=%q busy=%d", m.status, m.refreshBusy)
	}
}
