package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// Провайдер у цих тестах — providertest.Stub із полями-функціями; локальні
// конструктори лише вкладають у нього потрібну поведінку.
func episodesStub(eps []provider.Episode, err error, calls *int) providertest.Stub {
	return providertest.Stub{
		IDValue:   "stub",
		NameValue: "Stub",
		EpisodesFn: func(context.Context, provider.TitleRef) ([]provider.Episode, error) {
			if calls != nil {
				*calls++
			}
			return eps, err
		},
	}
}

func catalogStub(cards []provider.TitleCard, err error) providertest.Stub {
	return providertest.Stub{
		IDValue:   "stub",
		NameValue: "Stub",
		CatalogFn: func(context.Context, provider.CatalogKind) ([]provider.TitleCard, error) {
			return cards, err
		},
	}
}

func sourcesStub(sources []provider.Source) providertest.Stub {
	return providertest.Stub{
		IDValue:   "stub",
		NameValue: "Stub",
		SourcesFn: func(context.Context, provider.TitleRef, int) ([]provider.Source, error) {
			return sources, nil
		},
	}
}

// perEpisodeSourcesStub фільтрує джерела за номером серії, як це робить anitube,
// і рахує звернення — цього досить для перевірки memo.
func perEpisodeSourcesStub(sources []provider.Source, calls *int) providertest.Stub {
	return providertest.Stub{
		IDValue:   "stub",
		NameValue: "Stub",
		SourcesFn: func(_ context.Context, _ provider.TitleRef, episode int) ([]provider.Source, error) {
			if calls != nil {
				*calls++
			}
			var out []provider.Source
			for _, s := range sources {
				if s.Episode == episode {
					out = append(out, s)
				}
			}
			if len(out) == 0 {
				return nil, fmt.Errorf("серія %d: не знайдено жодного джерела: %w", episode, errs.ErrNoStream)
			}
			return out, nil
		},
	}
}

type stubExtractor struct {
	streams []extractor.Stream
	err     error
	handle  string
}

type fakePlayer struct {
	session  player.Session
	startErr error
}

func (fakePlayer) ID() string { return "fake" }
func (fakePlayer) Command(string, string, map[string]string, float64) *exec.Cmd {
	return nil
}
func (p fakePlayer) Start(context.Context, string, string, map[string]string, float64) (player.Session, error) {
	if p.startErr != nil {
		return nil, p.startErr
	}
	return p.session, nil
}

type fakeSession struct {
	end       chan player.EndReason
	positions []float64
	durations []float64
	posIndex  int
	durIndex  int
}

func newFakeSession(reason player.EndReason, positions, durations []float64) *fakeSession {
	end := make(chan player.EndReason, 1)
	end <- reason
	return &fakeSession{end: end, positions: positions, durations: durations}
}

func (s *fakeSession) TimePos() (float64, error) {
	if len(s.positions) == 0 {
		return 0, nil
	}
	index := min(s.posIndex, len(s.positions)-1)
	s.posIndex++
	return s.positions[index], nil
}

func (s *fakeSession) Duration() (float64, error) {
	if len(s.durations) == 0 {
		return 0, nil
	}
	index := min(s.durIndex, len(s.durations)-1)
	s.durIndex++
	return s.durations[index], nil
}

func (s *fakeSession) End() <-chan player.EndReason { return s.end }
func (s *fakeSession) Wait() error                  { return nil }
func (s *fakeSession) Close()                       {}

func (stubExtractor) ID() string { return "stub" }
func (e stubExtractor) Handles(embed string) bool {
	return e.handle == "" || e.handle == embed
}
func (e stubExtractor) Extract(context.Context, string, string) ([]extractor.Stream, error) {
	return e.streams, e.err
}

func TestStudioChoicesFiltersUnplayableHosts(t *testing.T) {
	playableEmbed := "https://handled.invalid/embed"
	sources := []provider.Source{
		{Studio: "X", Kind: provider.KindDub, Embed: "https://unsupported.invalid/embed"},
		{Studio: "Y", Kind: provider.KindVoiceover, Embed: playableEmbed},
	}
	engine := testEngine(sources, []extractor.Extractor{stubExtractor{handle: playableEmbed}})

	choices, err := engine.StudioChoices(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1)
	if err != nil {
		t.Fatalf("StudioChoices: %v", err)
	}
	if len(choices) != 1 || choices[0].Studio != "Y" {
		t.Fatalf("StudioChoices = %+v, want only Y", choices)
	}

	engine.Extractors = nil
	choices, err = engine.StudioChoices(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1)
	if !errors.Is(err, errs.ErrNoStream) || len(choices) != 0 {
		t.Fatalf("StudioChoices all unplayable = (%+v, %v), want ErrNoStream", choices, err)
	}
}

func TestResolvePinFallback(t *testing.T) {
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	playableEmbed := "https://handled.invalid/embed"
	sources := []provider.Source{
		{Studio: "X", Kind: provider.KindDub, Embed: "https://unsupported.invalid/embed"},
		{Studio: "Y", Kind: provider.KindVoiceover, Embed: playableEmbed},
	}
	engine := testEngine(sources, []extractor.Extractor{stubExtractor{
		handle:  playableEmbed,
		streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}},
	}})
	title := engine.Lib.EnsureTitle(ref, func() string { return "title-id" })
	engine.Lib.EntryFor(title.ID).StudioPin = "X"

	resolved, err := engine.Resolve(t.Context(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !resolved.PinFallback || resolved.Source.Studio != "Y" {
		t.Fatalf("Resolved = %+v, want fallback to Y", resolved)
	}
}

func TestResolveCandidatesPlayable(t *testing.T) {
	playableEmbed := "https://handled.invalid/embed"
	sources := []provider.Source{
		{Studio: "X", Kind: provider.KindVoiceover, Embed: "https://unsupported.invalid/embed"},
		{Studio: "Y", Kind: provider.KindVoiceover, Embed: playableEmbed},
		{Studio: "Z", Kind: provider.KindVoiceover, Embed: playableEmbed},
	}
	engine := testEngine(sources, []extractor.Extractor{stubExtractor{
		handle:  playableEmbed,
		streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}},
	}})

	resolved, err := engine.Resolve(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(resolved.Candidates) != 2 {
		t.Fatalf("Candidates = %+v, want Y and Z", resolved.Candidates)
	}
	for _, candidate := range resolved.Candidates {
		if candidate.Studio == "X" {
			t.Fatalf("Candidates contains unplayable studio X: %+v", resolved.Candidates)
		}
	}
}

func TestPinStudioClearsKindPin(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	lib := &library.Library{}
	title := lib.EnsureTitle(ref, func() string { return "title-id" })
	entry := lib.EntryFor(title.ID)
	entry.StudioPin = "Old"
	entry.KindPin = provider.KindDub
	engine := &Engine{Store: st, Lib: lib}

	if err := engine.PinStudio(ref, "New"); err != nil {
		t.Fatalf("PinStudio: %v", err)
	}
	if entry.StudioPin != "New" || entry.KindPin != "" {
		t.Fatalf("pins = (%q, %q), want (New, empty)", entry.StudioPin, entry.KindPin)
	}
}

func TestResolveDoesNotMutateLibrary(t *testing.T) {
	engine := testEngine(
		[]provider.Source{{Studio: "Студія", Kind: provider.KindDub, Embed: "https://video.invalid/embed"}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}}}},
	)

	if _, err := engine.Resolve(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1, nil); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(engine.Lib.Titles) != 0 || len(engine.Lib.Entries) != 0 {
		t.Fatalf("Resolve mutated library: %d titles, %d entries", len(engine.Lib.Titles), len(engine.Lib.Entries))
	}
}

func TestParallelResolvesDoNotMutateLibrary(t *testing.T) {
	engine := testEngine(
		[]provider.Source{{Studio: "Студія", Kind: provider.KindDub, Embed: "https://video.invalid/embed"}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}}}},
	)
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title"}

	const resolves = 16
	errs := make(chan error, resolves)
	var wg sync.WaitGroup
	for range resolves {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := engine.Resolve(t.Context(), ref, 1, nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Resolve: %v", err)
		}
	}
	if len(engine.Lib.Titles) != 0 || len(engine.Lib.Entries) != 0 {
		t.Fatalf("parallel Resolve mutated library: %d titles, %d entries", len(engine.Lib.Titles), len(engine.Lib.Entries))
	}
}

func TestPlayEnsuresTitleBeforeStartingPlayer(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{
		Store:  st,
		Lib:    &library.Library{},
		Player: fakePlayer{startErr: errors.New("player start failed")},
	}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}

	_, err = engine.Play(t.Context(), &Resolved{
		Ref:        ref,
		Episode:    1,
		Source:     provider.Source{Studio: "Студія", Kind: provider.KindDub},
		Stream:     extractor.Stream{URL: "https://video.invalid/stream.m3u8"},
		MediaTitle: "Title · 1",
	})
	if err == nil {
		t.Fatal("Play unexpectedly started player")
	}
	if engine.Lib.TitleByRef(ref) == nil {
		t.Fatal("Play did not ensure title before starting player")
	}
}

func TestPlayWithoutPlayerDoesNotMutateLibrary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{Store: st, Lib: &library.Library{}}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}

	_, err = engine.Play(t.Context(), &Resolved{Ref: ref, Episode: 1})
	if !errors.Is(err, errs.ErrNoPlayer) {
		t.Fatalf("Play error = %v, очікував ErrNoPlayer", err)
	}
	if len(engine.Lib.Titles) != 0 || len(engine.Lib.Entries) != 0 {
		t.Fatalf("Play mutated library: %d titles, %d entries", len(engine.Lib.Titles), len(engine.Lib.Entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "library.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("library.json мав бути відсутній, Stat error = %v", err)
	}
}

func TestBookmarkPersistsAddedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{Store: st, Lib: &library.Library{}}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}

	result, err := engine.Bookmark(ref, 12)
	if err != nil || result != library.BookmarkAdded {
		t.Fatalf("Bookmark add = (%v, %v), очікував BookmarkAdded", result, err)
	}
	saved, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("load added bookmark: %v", err)
	}
	title := saved.TitleByRef(ref)
	if title == nil {
		t.Fatal("Bookmark не зберіг тайтл")
	}
	entry := saved.EntryLookup(title.ID)
	if entry == nil || entry.State != library.StatePlanned || entry.KnownEpisodes != 12 {
		t.Fatalf("збережена закладка = %+v", entry)
	}

	result, err = engine.Bookmark(ref, 20)
	if err != nil || result != library.BookmarkRemoved {
		t.Fatalf("Bookmark remove = (%v, %v), очікував BookmarkRemoved", result, err)
	}
	saved, err = st.LoadLibrary()
	if err != nil {
		t.Fatalf("load removed bookmark: %v", err)
	}
	title = saved.TitleByRef(ref)
	if title == nil || saved.EntryLookup(title.ID) != nil {
		t.Fatalf("закладка не зникла з бібліотеки: title=%+v entries=%+v", title, saved.Entries)
	}
}

func TestMarkSeenPersistsKnownEpisodes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{Store: st, Lib: &library.Library{}}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	if _, err := engine.Bookmark(ref, 3); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}

	if err := engine.MarkSeen(ref, 5); err != nil {
		t.Fatalf("MarkSeen: %v", err)
	}
	saved, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	title := saved.TitleByRef(ref)
	if title == nil || saved.EntryLookup(title.ID).KnownEpisodes != 5 {
		t.Fatalf("MarkSeen не збережено: title=%+v entries=%+v", title, saved.Entries)
	}
}

func TestReconcileKnownPersistsActualEpisodes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{Store: st, Lib: &library.Library{}}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	if _, err := engine.Bookmark(ref, 12); err != nil {
		t.Fatalf("Bookmark: %v", err)
	}

	if err := engine.ReconcileKnown(ref, 12, 10); err != nil {
		t.Fatalf("ReconcileKnown: %v", err)
	}
	saved, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	title := saved.TitleByRef(ref)
	if title == nil || saved.EntryLookup(title.ID).KnownEpisodes != 10 {
		t.Fatalf("ReconcileKnown не збережено: title=%+v entries=%+v", title, saved.Entries)
	}
}

func TestBookmarkRemoveWritesLibrary(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	lib := &library.Library{}
	title := lib.EnsureTitle(ref, func() string { return "title-id" })
	lib.RecordPosition(title.ID, 3, 872, 1440, time.Now())
	lib.EntryFor(title.ID).StudioPin = "Студія"
	engine := &Engine{Store: st, Lib: lib}
	path := filepath.Join(dir, "library.json")
	if err := st.SaveLibrary(lib); err != nil {
		t.Fatalf("SaveLibrary: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read library before Bookmark: %v", err)
	}

	result, err := engine.Bookmark(ref, 12)
	if err != nil || result != library.BookmarkRemoved {
		t.Fatalf("Bookmark = (%v, %v), очікував BookmarkRemoved", result, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read library after Bookmark: %v", err)
	}
	if string(after) == string(before) {
		t.Fatal("Bookmark не перезаписав library.json")
	}
	saved, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	entry := saved.EntryLookup(title.ID)
	if entry == nil || !entry.Hidden || entry.StudioPin != "Студія" {
		t.Fatalf("збережений запис = %+v", entry)
	}
	progress := saved.ProgressFor(title.ID, 3)
	if progress == nil || progress.PositionSec != 872 {
		t.Fatalf("збережений прогрес = %+v", progress)
	}
}

func TestPlayEOFForceCompletesProgress(t *testing.T) {
	engine, title := engineWithProgress(t, player.EndEOF)

	result, err := engine.Play(t.Context(), testResolved(title.Sources[0]))
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if result.Reason != player.EndEOF || !result.Completed {
		t.Fatalf("Result = %+v, очікував EOF і Completed", result)
	}
}

func TestPlayQuitPreservesIncompleteProgress(t *testing.T) {
	engine, title := engineWithProgress(t, player.EndQuit)

	result, err := engine.Play(t.Context(), testResolved(title.Sources[0]))
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if result.Reason != player.EndQuit || result.Completed {
		t.Fatalf("Result = %+v, очікував Quit без Completed", result)
	}
}

func TestNextEpisodeNumberUsesSmallestGreaterNumber(t *testing.T) {
	episodes := []provider.Episode{{Number: 8}, {Number: 2}, {Number: 5}, {Number: 5}}

	got, ok := NextEpisodeNumber(episodes, 2)
	if !ok || got != 5 {
		t.Fatalf("NextEpisodeNumber = (%d, %v), очікував (5, true)", got, ok)
	}

	if got, ok := NextEpisodeNumber(episodes, 8); ok {
		t.Fatalf("NextEpisodeNumber після останньої серії = (%d, true), очікував (_, false)", got)
	}
}

func engineWithProgress(t *testing.T, reason player.EndReason) (*Engine, *library.LocalTitle) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	lib := &library.Library{}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	title := lib.EnsureTitle(ref, func() string { return "title-id" })
	lib.RecordPosition(title.ID, 1, 60, 1200, time.Now())
	return &Engine{
		Store:  st,
		Lib:    lib,
		Player: fakePlayer{session: newFakeSession(reason, []float64{60}, []float64{1200})},
	}, title
}

func testResolved(ref provider.TitleRef) *Resolved {
	return &Resolved{
		Ref:        ref,
		Episode:    1,
		Source:     provider.Source{Studio: "Студія", Kind: provider.KindDub},
		Stream:     extractor.Stream{URL: "https://video.invalid/stream.m3u8"},
		MediaTitle: "Title · 1",
	}
}

func TestResolveClassifiesAllOfflineAttempts(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "video.invalid", IsNotFound: true}
	engine := testEngine(
		[]provider.Source{{Studio: "Студія", Kind: provider.KindDub, Embed: "https://video.invalid/embed"}},
		[]extractor.Extractor{stubExtractor{err: fmt.Errorf("embed: %w", dnsErr)}},
	)

	_, err := engine.Resolve(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1, nil)
	if !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("Resolve error = %v, очікував ErrOffline", err)
	}
}

func TestResolveClassifiesUnsupportedHostAsNoStream(t *testing.T) {
	engine := testEngine(
		[]provider.Source{{Studio: "Студія", Kind: provider.KindDub, Embed: "https://unsupported.invalid/embed"}},
		nil,
	)

	_, err := engine.Resolve(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1, nil)
	if !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Resolve error = %v, очікував ErrNoStream", err)
	}
}

func TestResolveClassifiesEmptySourcesAsNoStream(t *testing.T) {
	engine := testEngine(nil, nil)

	_, err := engine.Resolve(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1, nil)
	if !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Resolve error = %v, очікував ErrNoStream", err)
	}
}

func TestEpisodesCachedStaleFallback(t *testing.T) {
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title"}
	cached := []provider.Episode{
		{Number: 1, Releases: []provider.Release{{Studio: "Студія", Kind: provider.KindDub}}},
		{Number: 2, Releases: []provider.Release{{Studio: "Студія", Kind: provider.KindDub}}},
	}
	tests := []struct {
		name        string
		providerErr error
		wantEps     []provider.Episode
		wantOffline bool
		wantErr     error
	}{
		{
			name:        "offline returns stale cache",
			providerErr: fmt.Errorf("episodes: %w", errs.ErrOffline),
			wantEps:     cached,
			wantOffline: true,
		},
		{
			name:        "provider failure is not masked",
			providerErr: fmt.Errorf("episodes: %w", errs.ErrProvider),
			wantErr:     errs.ErrProvider,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.Open(dir)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			cache, err := json.Marshal(struct {
				FetchedAt time.Time          `json:"fetched_at"`
				Episodes  []provider.Episode `json:"episodes"`
			}{
				FetchedAt: time.Now().Add(-7 * time.Hour),
				Episodes:  cached,
			})
			if err != nil {
				t.Fatalf("marshal stale cache: %v", err)
			}
			cachePath := filepath.Join(dir, "cache", "episodes-stub-1-title.json")
			if err := os.WriteFile(cachePath, cache, 0o644); err != nil {
				t.Fatalf("write stale cache: %v", err)
			}

			engine := &Engine{
				Provider: episodesStub(nil, tt.providerErr, nil),
				Store:    st,
			}
			gotEps, gotOffline, gotErr := engine.EpisodesCached(t.Context(), ref)

			if !reflect.DeepEqual(gotEps, tt.wantEps) {
				t.Errorf("episodes = %#v, want %#v", gotEps, tt.wantEps)
			}
			if gotOffline != tt.wantOffline {
				t.Errorf("offline = %v, want %v", gotOffline, tt.wantOffline)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("error = %v, want classification %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestEpisodesFreshBypassesCacheAndSavesResult(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ref := provider.TitleRef{Provider: "stub", Slug: "fresh"}
	if err := st.SaveEpisodes(ref, []provider.Episode{{Number: 1}}); err != nil {
		t.Fatalf("save initial cache: %v", err)
	}
	calls := 0
	want := []provider.Episode{
		{Number: 2, Releases: []provider.Release{{Studio: "Студія", Kind: provider.KindDub}}},
		{Number: 4, Releases: []provider.Release{{Studio: "Студія", Kind: provider.KindDub}}},
	}
	engine := &Engine{
		Provider: episodesStub(want, nil, &calls),
		Store:    st,
	}

	got, err := engine.EpisodesFresh(t.Context(), ref)
	if err != nil {
		t.Fatalf("EpisodesFresh: %v", err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("episodes = %#v, want %#v", got, want)
	}
	cached, _, found := st.LoadEpisodes(ref)
	if !found || !reflect.DeepEqual(cached, want) {
		t.Fatalf("saved cache = (%#v, found=%v), want %#v", cached, found, want)
	}
}

func TestCatalogCachedStaleFallback(t *testing.T) {
	cached := []provider.TitleCard{
		{TitleRef: provider.TitleRef{Provider: "stub", Slug: "1-a", Name: "A"}},
		{TitleRef: provider.TitleRef{Provider: "stub", Slug: "2-b", Name: "B"}},
	}
	tests := []struct {
		name        string
		providerErr error
		wantCards   []provider.TitleCard
		wantOffline bool
		wantErr     error
	}{
		{
			name:        "offline returns stale cache",
			providerErr: fmt.Errorf("catalog: %w", errs.ErrOffline),
			wantCards:   cached,
			wantOffline: true,
		},
		{
			name:        "provider failure is not masked",
			providerErr: fmt.Errorf("catalog: %w", errs.ErrProvider),
			wantErr:     errs.ErrProvider,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			st, err := store.Open(dir)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			cache, err := json.Marshal(struct {
				FetchedAt time.Time            `json:"fetched_at"`
				Year      int                  `json:"year"`
				Cards     []provider.TitleCard `json:"cards"`
			}{
				FetchedAt: time.Now().Add(-13 * time.Hour),
				Year:      time.Now().Year(),
				Cards:     cached,
			})
			if err != nil {
				t.Fatalf("marshal stale cache: %v", err)
			}
			cachePath := filepath.Join(dir, "cache", "catalog-stub-top-season.json")
			if err := os.WriteFile(cachePath, cache, 0o644); err != nil {
				t.Fatalf("write stale cache: %v", err)
			}

			engine := &Engine{
				Provider: catalogStub(nil, tt.providerErr),
				Store:    st,
			}
			gotCards, gotOffline, gotErr := engine.CatalogCached(t.Context(), provider.CatalogTopSeason)

			if !reflect.DeepEqual(gotCards, tt.wantCards) {
				t.Errorf("cards = %#v, want %#v", gotCards, tt.wantCards)
			}
			if gotOffline != tt.wantOffline {
				t.Errorf("offline = %v, want %v", gotOffline, tt.wantOffline)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("error = %v, want classification %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestCatalogCachedFreshCacheSkipsNetwork(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cached := []provider.TitleCard{{TitleRef: provider.TitleRef{Provider: "stub", Slug: "1-a", Name: "A"}}}
	if err := st.SaveCatalog("stub", provider.CatalogFresh, cached); err != nil {
		t.Fatalf("save catalog: %v", err)
	}

	// провайдер із помилкою: якщо його смикнули — тест впаде
	engine := &Engine{
		Provider: catalogStub(nil, fmt.Errorf("catalog: %w", errs.ErrProvider)),
		Store:    st,
	}
	got, offline, err := engine.CatalogCached(t.Context(), provider.CatalogFresh)
	if err != nil || offline {
		t.Fatalf("CatalogCached = (offline=%v, err=%v), очікував свіжий кеш без мережі", offline, err)
	}
	if !reflect.DeepEqual(got, cached) {
		t.Fatalf("cards = %#v, want %#v", got, cached)
	}
}

func testEngine(sources []provider.Source, extractors []extractor.Extractor) *Engine {
	return &Engine{
		Provider:   sourcesStub(sources),
		Extractors: extractors,
		Lib:        &library.Library{},
	}
}

// ---- 2.2: hints/with, Begin/Run/Finish, memo, фільтр потоків ----

func TestResolveWithHintsMatchesResolve(t *testing.T) {
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Ref Name"}
	playableEmbed := "https://handled.invalid/embed"
	engine := testEngine(
		[]provider.Source{
			{Studio: "X", Kind: provider.KindDub, Embed: playableEmbed, Episode: 1},
			{Studio: "Y", Kind: provider.KindVoiceover, Embed: playableEmbed, Episode: 1},
		},
		[]extractor.Extractor{stubExtractor{
			handle:  playableEmbed,
			streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}},
		}},
	)
	title := engine.Lib.EnsureTitle(ref, func() string { return "title-id" })
	title.Name = "Local Name"
	engine.Lib.EntryFor(title.ID).StudioPin = "Y"
	engine.Lib.RecordPosition(title.ID, 1, 120, 1200, time.Now())

	want, err := engine.Resolve(t.Context(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, err := engine.ResolveWith(t.Context(), ref, 1, engine.ResolveHints(ref, 1), nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveWith = %+v, want %+v", got, want)
	}
	if want.StartSec != 120 || want.MediaTitle != "Local Name · 1" || want.Source.Studio != "Y" {
		t.Fatalf("hints not applied: %+v", want)
	}
}

func TestBeginWithoutPlayerLeavesLibraryUntouched(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	lib := &library.Library{}
	engine := &Engine{Store: st, Lib: lib}
	before := *lib

	_, _, err = engine.Begin(&Resolved{Ref: ref, Episode: 1})
	if !errors.Is(err, errs.ErrNoPlayer) {
		t.Fatalf("Begin error = %v, очікував ErrNoPlayer", err)
	}
	if !reflect.DeepEqual(before, *lib) {
		t.Fatalf("Begin змінив бібліотеку: %+v, було %+v", *lib, before)
	}
	if _, err := os.Stat(filepath.Join(dir, "library.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("library.json мав бути відсутній, Stat error = %v", err)
	}
}

func TestBeginRunFinishMatchesPlay(t *testing.T) {
	sequence, sequenceTitle := engineWithProgress(t, player.EndEOF)
	titleID, pinned, err := sequence.Begin(testResolved(sequenceTitle.Sources[0]))
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	reason, err := sequence.Run(t.Context(), testResolved(sequenceTitle.Sources[0]), titleID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := sequence.Finish(reason, titleID, 1)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	got.PinnedStudio = pinned

	whole, wholeTitle := engineWithProgress(t, player.EndEOF)
	want, err := whole.Play(t.Context(), testResolved(wholeTitle.Sources[0]))
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Begin/Run/Finish = %+v, Play = %+v", got, want)
	}
	if want.PinnedStudio != "Студія" || !want.Completed {
		t.Fatalf("Result = %+v, очікував пін студії і completed", want)
	}
}

func TestSourcesMemoSkipsSecondFetch(t *testing.T) {
	calls := 0
	engine := &Engine{
		Provider: perEpisodeSourcesStub(
			[]provider.Source{{Studio: "X", Kind: provider.KindDub, Embed: "https://video.invalid/embed", Episode: 1}},
			&calls,
		),
		Extractors: []extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: "https://video.invalid/stream.m3u8"}}}},
		Lib:        &library.Library{},
	}
	fresh := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Свіжа картка", URL: "https://site.invalid/1-title.html"}
	stored := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "З бібліотеки"}

	if _, err := engine.Resolve(t.Context(), fresh, 1, nil); err != nil {
		t.Fatalf("перший Resolve: %v", err)
	}
	if _, err := engine.Resolve(t.Context(), stored, 1, nil); err != nil {
		t.Fatalf("другий Resolve: %v", err)
	}
	if _, err := engine.StudioChoices(t.Context(), fresh, 1); err != nil {
		t.Fatalf("StudioChoices: %v", err)
	}
	if calls != 1 {
		t.Fatalf("звернень до провайдера = %d, want 1 (Name/URL не входять у ключ memo)", calls)
	}

	// інша серія — окремий ключ, memo не підмінює її кешем першої
	if _, err := engine.Resolve(t.Context(), fresh, 2, nil); !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Resolve серії 2 = %v, очікував ErrNoStream від провайдера", err)
	}
	if calls != 2 {
		t.Fatalf("звернень до провайдера після іншої серії = %d, want 2", calls)
	}

	// протермінований запис оновлюється мережею
	engine.sourcesMu.Lock()
	key := sourceKey{Provider: "stub", Slug: "1-title", Episode: 1}
	stale := engine.sourcesCache[key]
	stale.at = time.Now().Add(-sourcesTTL - time.Minute)
	engine.sourcesCache[key] = stale
	engine.sourcesMu.Unlock()

	if _, err := engine.Resolve(t.Context(), fresh, 1, nil); err != nil {
		t.Fatalf("Resolve після TTL: %v", err)
	}
	if calls != 3 {
		t.Fatalf("звернень до провайдера після TTL = %d, want 3", calls)
	}
}

func TestResolveRejectsInvalidStreamURL(t *testing.T) {
	engine := testEngine(
		[]provider.Source{{Studio: "Студія", Kind: provider.KindDub, Embed: "https://video.invalid/embed", Episode: 1}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: "http://127.0.0.1/x.m3u8"}}}},
	)
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title"}

	if _, err := engine.Resolve(t.Context(), ref, 1, nil); !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Resolve error = %v, очікував ErrNoStream", err)
	}
	if _, err := engine.ResolveAll(t.Context(), ref, 1); !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("ResolveAll error = %v, очікував ErrNoStream", err)
	}
}

func TestResolveAllPreservesOfflineClassification(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "video.invalid", IsNotFound: true}
	engine := testEngine(
		[]provider.Source{{Embed: "https://video.invalid/embed", Episode: 1}},
		[]extractor.Extractor{stubExtractor{err: fmt.Errorf("embed: %w", dnsErr)}},
	)

	candidates, err := engine.ResolveAll(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1)
	if len(candidates) != 0 || !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("ResolveAll = %v, error = %v; очікував ErrOffline", candidates, err)
	}
}

func TestResolveAllClassifiesUnsupportedHostAsNoStream(t *testing.T) {
	engine := testEngine([]provider.Source{{Embed: "https://unsupported.invalid/embed", Episode: 1}}, nil)

	candidates, err := engine.ResolveAll(t.Context(), provider.TitleRef{Provider: "stub", Slug: "1-title"}, 1)
	if len(candidates) != 0 || !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("ResolveAll = %v, error = %v; очікував ErrNoStream", candidates, err)
	}
}

// constantSession — плеєр із незмінною позицією: журнал має бути записаний
// один раз, а не на кожен тік.
type constantSession struct {
	mu       sync.Mutex
	calls    int
	end      chan player.EndReason
	onSample func(n int)
}

func (s *constantSession) TimePos() (float64, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if s.onSample != nil {
		s.onSample(n)
	}
	return 42, nil
}

func (s *constantSession) Duration() (float64, error)   { return 1200, nil }
func (s *constantSession) End() <-chan player.EndReason { return s.end }
func (s *constantSession) Wait() error                  { return nil }
func (s *constantSession) Close()                       {}

func TestRunSkipsJournalWhenPositionUnchanged(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	journalPath := filepath.Join(dir, "state", "current.json")

	sess := &constantSession{end: make(chan player.EndReason, 1)}
	wroteFirst := false
	sess.onSample = func(n int) {
		// На третьому семплі запис першого вже завершився: прибираємо файл,
		// щоб будь-який наступний запис став видимим.
		switch n {
		case 3:
			_, statErr := os.Stat(journalPath)
			wroteFirst = statErr == nil
			_ = os.Remove(journalPath)
		case 10:
			sess.end <- player.EndQuit
		}
	}
	engine := &Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{session: sess}, JournalInterval: 2 * time.Millisecond}

	reason, err := engine.Run(t.Context(), &Resolved{Episode: 1}, "title-id")
	if err != nil || reason != player.EndQuit {
		t.Fatalf("Run = (%v, %v), очікував EndQuit", reason, err)
	}
	if !wroteFirst {
		t.Fatal("перший семпл не потрапив у журнал")
	}
	if _, err := os.Stat(journalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("журнал перезаписано при незмінній позиції (Stat = %v)", err)
	}
}
