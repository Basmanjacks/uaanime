package playback

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// ReleaseSeed is detached episode metadata fetched before the sync commit.
type ReleaseSeed struct {
	Ref      provider.TitleRef
	Episodes []provider.Episode
}

// InitializeReleaseBaselines seeds unknown entries in one durable write.
// sync: clones the latest Lib and publishes only after SaveLibrary succeeds.
func (e *Engine) InitializeReleaseBaselines(seeds []ReleaseSeed) error {
	prepared := e.Lib.Clone()
	changed := false
	for _, seed := range seeds {
		if title := prepared.TitleByRef(seed.Ref); title != nil {
			changed = prepared.SeedReleaseBaseline(title.ID, seed.Episodes) || changed
		}
	}
	if !changed {
		return nil
	}
	return e.publishLibrary(prepared)
}

// InitializeReleaseBaseline is the single-title form. sync: touches Lib.
func (e *Engine) InitializeReleaseBaseline(ref provider.TitleRef, episodes []provider.Episode) error {
	return e.InitializeReleaseBaselines([]ReleaseSeed{{Ref: ref, Episodes: episodes}})
}

// AcknowledgeReleases unions supplied metadata without hiding/unhiding entries.
// sync: clones the latest Lib and publishes only after SaveLibrary succeeds.
func (e *Engine) AcknowledgeReleases(ref provider.TitleRef, episodes []provider.Episode) error {
	prepared := e.Lib.Clone()
	title := prepared.TitleByRef(ref)
	if title == nil || !prepared.AcknowledgeReleases(title.ID, episodes) {
		return nil
	}
	return e.publishLibrary(prepared)
}

func (e *Engine) publishLibrary(prepared *library.Library) error {
	if err := e.Store.SaveLibrary(prepared); err != nil {
		return err
	}
	// UI models retain the Library pointer. Replacing it would split ownership.
	*e.Lib = *prepared
	return nil
}

type titleSnapshot struct {
	records                                           *library.Library
	titlePositions, entryPositions, progressPositions []int
	titlesNil, entriesNil, progressNil                bool
}

func snapshotTitle(lib *library.Library, id string) titleSnapshot {
	s := titleSnapshot{records: &library.Library{}, titlesNil: lib.Titles == nil, entriesNil: lib.Entries == nil, progressNil: lib.Progress == nil}
	for i, t := range lib.Titles {
		if t != nil && t.ID == id {
			s.records.Titles = append(s.records.Titles, t)
			s.titlePositions = append(s.titlePositions, i)
		}
	}
	for i, entry := range lib.Entries {
		if entry != nil && entry.TitleID == id {
			s.records.Entries = append(s.records.Entries, entry)
			s.entryPositions = append(s.entryPositions, i)
		}
	}
	for i, p := range lib.Progress {
		if p != nil && p.TitleID == id {
			s.records.Progress = append(s.records.Progress, p)
			s.progressPositions = append(s.progressPositions, i)
		}
	}
	s.records = s.records.Clone()
	return s
}

// WatchedUndo is a single-use, title-scoped token. Callers discard it on playback
// and invalidate it when WatchedUndoAvailable reports a conflicting mutation.
type WatchedUndo struct {
	epoch         uint64
	titleID       string
	ref           provider.TitleRef
	before, after titleSnapshot
	used          bool
}

// SetWatchedBefore marks only real unfinished episode numbers below selected.
// fullEpisodes must be the complete list, independent of a UI display filter.
// sync: no mutation is published and no token returned when persistence fails.
func (e *Engine) SetWatchedBefore(ref provider.TitleRef, selected int, fullEpisodes []provider.Episode) (int, *WatchedUndo, error) {
	currentTitle := e.Lib.TitleByRef(ref)
	completed := map[int]bool{}
	if currentTitle != nil {
		for _, p := range e.Lib.Progress {
			if p != nil && p.TitleID == currentTitle.ID && p.Completed {
				completed[p.Episode] = true
			}
		}
	}
	numbers := map[int]bool{}
	var acknowledged []provider.Episode
	for _, ep := range fullEpisodes {
		if ep.Number >= 0 && ep.Number < selected && !completed[ep.Number] {
			numbers[ep.Number] = true
			acknowledged = append(acknowledged, ep)
		}
	}
	if len(numbers) == 0 {
		return 0, nil, nil
	}
	prepared := e.Lib.Clone()
	// EnsureTitle uses the same identity generator and unhide semantics as the
	// single mark action, while mutations remain private until persistence.
	title := prepared.EnsureTitle(ref, store.NewID)
	token := &WatchedUndo{epoch: e.watchedUndoEpoch, titleID: title.ID, ref: ref, before: snapshotTitle(e.Lib, title.ID)}
	ordered := make([]int, 0, len(numbers))
	for number := range numbers {
		ordered = append(ordered, number)
	}
	sort.Ints(ordered)
	at := time.Now()
	for _, number := range ordered {
		prepared.SetWatched(title.ID, number, true, at)
	}
	prepared.SeedReleaseBaseline(title.ID, fullEpisodes)
	prepared.AcknowledgeReleases(title.ID, acknowledged)
	token.after = snapshotTitle(prepared, title.ID)
	if err := e.publishLibrary(prepared); err != nil {
		return 0, nil, err
	}
	return len(numbers), token, nil
}

// WatchedUndoAvailable ignores unrelated title changes. sync: reads Lib.
func (e *Engine) WatchedUndoAvailable(token *WatchedUndo) bool {
	if token == nil || token.used || token.epoch != e.watchedUndoEpoch {
		return false
	}
	title := e.Lib.TitleByRef(token.ref)
	if title == nil || title.ID != token.titleID {
		return false
	}
	return reflect.DeepEqual(snapshotTitle(e.Lib, token.titleID).records, token.after.records)
}

// UndoWatched restores only the target title into the latest library. A failed
// save retains the token for retry. sync: touches Lib.
func (e *Engine) UndoWatched(token *WatchedUndo) error {
	if !e.WatchedUndoAvailable(token) {
		return fmt.Errorf("undo watched: %w", errs.ErrUndoConflict)
	}
	prepared := e.Lib.Clone()
	before := token.before
	records := before.records.Clone()
	prepared.Titles = restoreTitleRecords(prepared.Titles, records.Titles, before.titlePositions, before.titlesNil, func(t *library.LocalTitle) bool { return t != nil && t.ID == token.titleID })
	prepared.Entries = restoreTitleRecords(prepared.Entries, records.Entries, before.entryPositions, before.entriesNil, func(entry *library.Entry) bool { return entry != nil && entry.TitleID == token.titleID })
	prepared.Progress = restoreTitleRecords(prepared.Progress, records.Progress, before.progressPositions, before.progressNil, func(p *library.Progress) bool { return p != nil && p.TitleID == token.titleID })
	if err := e.publishLibrary(prepared); err != nil {
		return err
	}
	token.used = true
	return nil
}

func restoreTitleRecords[T any](current, before []T, positions []int, wasNil bool, matches func(T) bool) []T {
	result := slices.DeleteFunc(current, matches)
	for i, record := range before {
		result = slices.Insert(result, min(positions[i], len(result)), record)
	}
	if len(result) == 0 && wasNil {
		return nil
	}
	return result
}

// InvalidateWatchedUndo expires earlier actions when a player session begins,
// even if that session leaves persisted title fields unchanged. sync: Engine state.
func (e *Engine) InvalidateWatchedUndo() { e.watchedUndoEpoch++ }
