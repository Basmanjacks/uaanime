package playback

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func actionEngine(t *testing.T) (*Engine, string, provider.TitleRef) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Store: s, Lib: &library.Library{}}, dir, provider.TitleRef{Provider: "p", Slug: "1-title", Name: "Title"}
}
func actionEpisodes() []provider.Episode {
	var eps []provider.Episode
	for _, n := range []int{7, 0, 3, 3, 100} {
		eps = append(eps, provider.Episode{Number: n, Releases: []provider.Release{{Studio: "A", Kind: provider.KindDub}}})
	}
	return eps
}
func TestSetWatchedBeforeSparseAndUndoNew(t *testing.T) {
	e, _, ref := actionEngine(t)
	ptr := e.Lib
	before := e.Lib.Clone()
	n, undo, err := e.SetWatchedBefore(ref, 7, actionEpisodes())
	if err != nil || n != 2 || undo == nil {
		t.Fatalf("%d %v %v", n, undo, err)
	}
	if e.Lib != ptr {
		t.Fatal("library pointer replaced")
	}
	title := e.Lib.TitleByRef(ref)
	for _, n := range []int{0, 3} {
		if p := e.Lib.ProgressFor(title.ID, n); p == nil || !p.Completed {
			t.Fatalf("episode %d: %+v", n, p)
		}
	}
	if len(e.Lib.Progress) != 2 {
		t.Fatal("fabricated missing episodes")
	}
	if !e.WatchedUndoAvailable(undo) {
		t.Fatal("undo unavailable")
	}
	if err = e.UndoWatched(undo); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Lib, before) {
		t.Fatalf("undo did not restore absent state: %+v", e.Lib)
	}
	if e.WatchedUndoAvailable(undo) {
		t.Fatal("token reusable after success")
	}
}
func TestBulkPreservesCompletedAndHiddenStateAndUnrelatedChanges(t *testing.T) {
	e, _, ref := actionEngine(t)
	title := e.Lib.EnsureTitle(ref, func() string { return "t" })
	entry := e.Lib.EntryFor(title.ID)
	entry.StudioPin = "A"
	entry.Hidden = true
	e.Lib.Progress = append(e.Lib.Progress, &library.Progress{TitleID: "t", Episode: 0, Completed: true, PositionSec: 9, DurationSec: 10, WatchedAt: time.Unix(12, 0)})
	e.Lib.SeedReleaseBaseline("t", []provider.Episode{actionEpisodes()[0]})
	before := e.Lib.Clone()
	n, undo, err := e.SetWatchedBefore(ref, 7, actionEpisodes())
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if !reflect.DeepEqual(before.Progress[0], e.Lib.Progress[0]) {
		t.Fatal("completed record rewritten")
	}
	if e.Lib.EntryLookup("t").Hidden {
		t.Fatal("hidden entry not restored")
	}
	unrelated := provider.TitleRef{Provider: "p", Slug: "2-other"}
	e.Lib.EnsureTitle(unrelated, func() string { return "other" })
	e.Lib.EntryFor("other").StudioPin = "B"
	if !e.WatchedUndoAvailable(undo) {
		t.Fatal("unrelated mutation invalidated undo")
	}
	if err = e.UndoWatched(undo); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Entries[0], e.Lib.EntryLookup("t")) || !reflect.DeepEqual(before.Progress, e.Lib.Progress) {
		t.Fatal("target not restored exactly")
	}
	if e.Lib.EntryLookup("other").StudioPin != "B" {
		t.Fatal("unrelated changes lost")
	}
}
func TestWatchedUndoGuardsEveryTargetField(t *testing.T) {
	for _, mutation := range []string{"pin", "source", "progress", "baseline", "hidden"} {
		t.Run(mutation, func(t *testing.T) {
			e, _, ref := actionEngine(t)
			_, undo, err := e.SetWatchedBefore(ref, 7, actionEpisodes())
			if err != nil {
				t.Fatal(err)
			}
			title := e.Lib.TitleByRef(ref)
			entry := e.Lib.EntryLookup(title.ID)
			switch mutation {
			case "pin":
				entry.StudioPin = "B"
			case "source":
				title.Sources = append(title.Sources, provider.TitleRef{Provider: "p", Slug: "3-alt"})
			case "progress":
				e.Lib.Progress[0].PositionSec++
			case "baseline":
				entry.ReleaseBaseline.Groups[0].Episodes = "99"
			case "hidden":
				entry.Hidden = true
			}
			before := e.Lib.Clone()
			if e.WatchedUndoAvailable(undo) {
				t.Fatal("guard missed target mutation")
			}
			if err = e.UndoWatched(undo); !errors.Is(err, errs.ErrUndoConflict) {
				t.Fatalf("got %v", err)
			}
			if !reflect.DeepEqual(before, e.Lib) {
				t.Fatal("conflicting undo mutated library")
			}
		})
	}
}
func TestBulkSaveFailureNoPublishAndUndoRetry(t *testing.T) {
	e, dir, ref := actionEngine(t)
	ptr := e.Lib
	before := e.Lib.Clone()
	block := filepath.Join(dir, "library.json")
	if err := os.Mkdir(block, 0700); err != nil {
		t.Fatal(err)
	}
	if n, u, err := e.SetWatchedBefore(ref, 7, actionEpisodes()); err == nil || n != 0 || u != nil {
		t.Fatalf("%d %v %v", n, u, err)
	}
	if e.Lib != ptr || !reflect.DeepEqual(before, e.Lib) {
		t.Fatal("failed save published")
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	_, undo, err := e.SetWatchedBefore(ref, 7, actionEpisodes())
	if err != nil {
		t.Fatal(err)
	}
	after := e.Lib.Clone()
	if err = os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(block, 0700); err != nil {
		t.Fatal(err)
	}
	if err = e.UndoWatched(undo); err == nil {
		t.Fatal("expected save failure")
	}
	if !e.WatchedUndoAvailable(undo) || !reflect.DeepEqual(after, e.Lib) {
		t.Fatal("failed undo consumed token or changed memory")
	}
	if err = os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err = e.UndoWatched(undo); err != nil {
		t.Fatal(err)
	}
	if e.Lib != ptr {
		t.Fatal("undo replaced pointer")
	}
}
func TestBulkNoopDoesNotSave(t *testing.T) {
	e, dir, ref := actionEngine(t)
	if err := os.Mkdir(filepath.Join(dir, "library.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if n, u, err := e.SetWatchedBefore(ref, 0, actionEpisodes()); n != 0 || u != nil || err != nil {
		t.Fatalf("%d %v %v", n, u, err)
	}
	if len(e.Lib.Titles) != 0 {
		t.Fatal("noop created title")
	}
}
func TestReleaseBaselineTransactions(t *testing.T) {
	e, dir, ref := actionEngine(t)
	e.Lib.EnsureTitle(ref, func() string { return "t" })
	e.Lib.EntryFor("t")
	ptr := e.Lib
	before := e.Lib.Clone()
	block := filepath.Join(dir, "library.json")
	if err := os.Mkdir(block, 0700); err != nil {
		t.Fatal(err)
	}
	seeds := []ReleaseSeed{{Ref: ref, Episodes: actionEpisodes()}}
	if err := e.InitializeReleaseBaselines(seeds); err == nil {
		t.Fatal("expected save failure")
	}
	if !reflect.DeepEqual(before, e.Lib) || e.Lib != ptr {
		t.Fatal("seed failure mutated library")
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err := e.InitializeReleaseBaselines(seeds); err != nil {
		t.Fatal(err)
	}
	// Initialization must clone the current state on each call.
	e.Lib.EntryLookup("t").StudioPin = "B"
	if err := e.AcknowledgeReleases(ref, []provider.Episode{{Number: 9, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}}); err != nil {
		t.Fatal(err)
	}
	if e.Lib.EntryLookup("t").StudioPin != "B" || e.Lib != ptr {
		t.Fatal("ack overwrote latest state")
	}
	after := e.Lib.Clone()
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(block, 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.AcknowledgeReleases(ref, []provider.Episode{{Number: 11, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}}); err == nil {
		t.Fatal("expected ack save failure")
	}
	if !reflect.DeepEqual(after, e.Lib) {
		t.Fatal("ack failure mutated memory")
	}
}

func TestBulkSeedsFullBaselineBeforeAcknowledgingSubset(t *testing.T) {
	e, _, ref := actionEngine(t)
	if _, _, err := e.SetWatchedBefore(ref, 7, actionEpisodes()); err != nil {
		t.Fatal(err)
	}
	title := e.Lib.TitleByRef(ref)
	if studio, n := e.Lib.PreferredFresh(title.ID, actionEpisodes(), library.Prefs{FavoriteStudio: "A"}); studio != "A" || n != 0 {
		t.Fatalf("old unwatched releases became new: %s/%d", studio, n)
	}
}

func TestWatchedUndoInvalidatedByPlaybackEpoch(t *testing.T) {
	e, _, ref := actionEngine(t)
	_, undo, err := e.SetWatchedBefore(ref, 7, actionEpisodes())
	if err != nil {
		t.Fatal(err)
	}
	after := e.Lib.Clone()
	e.InvalidateWatchedUndo()
	if e.WatchedUndoAvailable(undo) {
		t.Fatal("playback left old undo available")
	}
	if err = e.UndoWatched(undo); !errors.Is(err, errs.ErrUndoConflict) {
		t.Fatalf("got %v", err)
	}
	if !reflect.DeepEqual(after, e.Lib) {
		t.Fatal("invalidation mutated library")
	}
}
