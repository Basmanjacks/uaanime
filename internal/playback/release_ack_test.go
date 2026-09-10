package playback

import (
	"context"
	"errors"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestSingleMarkAcknowledgesOnlyItsReleases(t *testing.T) {
	e, _, ref := actionEngine(t)
	title := e.Lib.EnsureTitle(ref, func() string { return "t" })
	e.Lib.EntryFor(title.ID)
	old := actionEpisodes()
	e.Lib.SeedReleaseBaseline(title.ID, old)
	fresh := slices.Clone(old)
	fresh = append(fresh, provider.Episode{Number: 8, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}, provider.Episode{Number: 9, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}})
	if err := e.Store.SaveEpisodes(ref, fresh); err != nil {
		t.Fatal(err)
	}
	if err := e.SetWatched(ref, 8, true); err != nil {
		t.Fatal(err)
	}
	// Removing the manual flag does not make an acknowledged release new again.
	if err := e.SetWatched(ref, 8, false); err != nil {
		t.Fatal(err)
	}
	e.Lib.EntryLookup(title.ID).StudioPin = "B"
	if _, n := e.Lib.PreferredFresh(title.ID, fresh, e.Prefs); n != 1 {
		t.Fatalf("fresh=%d want1", n)
	}
}
func TestFailedSingleMarkDoesNotPublish(t *testing.T) {
	e, dir, ref := actionEngine(t)
	before := e.Lib.Clone()
	if err := os.MkdirAll(filepath.Join(dir, "library.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.SetWatched(ref, 1, true); err == nil {
		t.Fatal("expected save failure")
	}
	if !reflect.DeepEqual(e.Lib, before) {
		t.Fatal("failed save published mutation")
	}
}
func TestFinishAcknowledgesWatchedRelease(t *testing.T) {
	e, _, ref := actionEngine(t)
	title := e.Lib.EnsureTitle(ref, func() string { return "t" })
	e.Lib.EntryFor(title.ID)
	e.Lib.SeedReleaseBaseline(title.ID, nil)
	eps := []provider.Episode{{Number: 1, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}, {Number: 2, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}}
	if err := e.Store.SaveEpisodes(ref, eps); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Finish(player.EndEOF, title.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := e.SetWatched(ref, 1, false); err != nil {
		t.Fatal(err)
	}
	e.Lib.EntryLookup(title.ID).StudioPin = "B"
	if _, n := e.Lib.PreferredFresh(title.ID, eps, e.Prefs); n != 1 {
		t.Fatalf("fresh=%d want1", n)
	}
}

func TestFailedStartDoesNotAcknowledgeExistingProgress(t *testing.T) {
	e, _, ref := actionEngine(t)
	title := e.Lib.EnsureTitle(ref, func() string { return "t" })
	entry := e.Lib.EntryFor(title.ID)
	entry.StudioPin = "B"
	e.Lib.SeedReleaseBaseline(title.ID, nil)
	e.Lib.RecordPosition(title.ID, 1, 10, 100, time.Now())
	eps := []provider.Episode{{Number: 1, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}}
	if err := e.Store.SaveEpisodes(ref, eps); err != nil {
		t.Fatal(err)
	}
	e.Player = fakePlayer{startErr: errors.New("cannot start")}
	res := &Resolved{Ref: ref, Episode: 1, Source: provider.Source{Studio: "B", Kind: provider.KindDub}}
	if _, err := e.Play(context.Background(), res); err == nil {
		t.Fatal("expected start failure")
	}
	if _, n := e.Lib.PreferredFresh(title.ID, eps, e.Prefs); n != 1 {
		t.Fatalf("failed start acknowledged release: fresh=%d", n)
	}
}
