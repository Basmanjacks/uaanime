package playback

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func journalEngine(t *testing.T) (*Engine, *constantSession, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess := &constantSession{end: make(chan player.EndReason, 1)}
	return &Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{session: sess}, JournalInterval: time.Millisecond}, sess, filepath.Join(dir, "state", "current.json")
}

func TestRunRetriesFailedJournalAtUnchangedPosition(t *testing.T) {
	eng, sess, path := journalEngine(t)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	sess.onSample = func(n int) {
		if n == 3 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if n == 5 {
			sess.end <- player.EndQuit
		}
	}
	if _, err := eng.Run(t.Context(), &Resolved{Episode: 1}, "title-id"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unchanged position was never retried: %v", err)
	}
	var j store.Journal
	if err := json.Unmarshal(data, &j); err != nil || j.PositionSec != 42 {
		t.Fatalf("journal = %+v, error = %v", j, err)
	}
}

func TestRunReturnsUnresolvedJournalError(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "quit", true: "cancel"}[canceled], func(t *testing.T) {
			eng, sess, path := journalEngine(t)
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sess.onSample = func(n int) {
				if n == 3 {
					if canceled {
						cancel()
					} else {
						sess.end <- player.EndQuit
					}
				}
			}
			reason, err := eng.Run(ctx, &Resolved{Episode: 1}, "title-id")
			if reason != player.EndQuit || err == nil {
				t.Fatalf("Run = (%v, %v), want quit with journal error", reason, err)
			}
		})
	}
}

func TestRunPreservesPlayerFailureAlongsideJournalFailure(t *testing.T) {
	eng, sess, path := journalEngine(t)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	sess.onSample = func(n int) {
		if n == 3 {
			sess.end <- player.EndError
		}
	}
	reason, err := eng.Run(t.Context(), &Resolved{Episode: 1}, "title-id")
	var pathErr *os.PathError
	var linkErr *os.LinkError
	if reason != player.EndError || !errors.Is(err, errs.ErrPlayer) || (!errors.As(err, &pathErr) && !errors.As(err, &linkErr)) {
		t.Fatalf("Run = (%v, %v), want both player and journal failures", reason, err)
	}
}

func TestRunReportsOnlyJournalStateTransitions(t *testing.T) {
	eng, sess, path := journalEngine(t)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	sess.onSample = func(n int) {
		if n == 3 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if n == 5 {
			sess.end <- player.EndQuit
		}
	}
	var events []error
	_, err := eng.RunWithObserver(t.Context(), &Resolved{Episode: 1}, "title-id", func(err error) { events = append(events, err) })
	if err != nil || len(events) != 2 || events[0] == nil || events[1] != nil {
		t.Fatalf("events = %v, Run error = %v; want one failure and one recovery", events, err)
	}
}

func TestPlayObserverReportsJournalTransitions(t *testing.T) {
	eng, sess, path := journalEngine(t)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	sess.onSample = func(n int) {
		if n == 3 {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		if n == 5 {
			sess.end <- player.EndQuit
		}
	}
	var events []error
	res, err := eng.PlayWithObserver(t.Context(), testResolved(provider.TitleRef{Provider: "stub", Slug: "one"}), func(err error) { events = append(events, err) })
	if err != nil || res == nil || res.PositionSec != 42 || len(events) != 2 || events[0] == nil || events[1] != nil {
		t.Fatalf("result %+v, err %v events %v", res, err, events)
	}
}

func TestFinishWithoutSample(t *testing.T) {
	for _, reason := range []player.EndReason{player.EndEOF, player.EndQuit} {
		t.Run(string(reason), func(t *testing.T) {
			eng, _, _ := journalEngine(t)
			title := eng.Lib.EnsureTitle(provider.TitleRef{Provider: "stub", Slug: "1-title"}, func() string { return "title-id" })
			out, err := eng.Finish(reason, title.ID, 3)
			if err != nil {
				t.Fatal(err)
			}
			if reason == player.EndQuit {
				if out.Completed || eng.Lib.ProgressFor(title.ID, 3) != nil {
					t.Fatal("quit without sample created progress")
				}
				return
			}
			saved, err := eng.Store.LoadLibrary()
			if err != nil {
				t.Fatal(err)
			}
			p := saved.ProgressFor(title.ID, 3)
			if !out.Completed || p == nil || !p.Completed || saved.EntryFor(title.ID).LastEpisode != 3 {
				t.Fatalf("EOF completion not persisted: result = %+v, progress = %+v", out, p)
			}
		})
	}
}

func TestPlayFinalizesJournalWhenStartFails(t *testing.T) {
	eng, _, _ := journalEngine(t)
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title"}
	title := eng.Lib.EnsureTitle(ref, func() string { return "title-id" })
	if err := eng.Store.WriteJournal(&store.Journal{TitleID: title.ID, Episode: 1, PositionSec: 37, DurationSec: 1200, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	startErr := errors.New("player failed to start")
	eng.Player = fakePlayer{startErr: startErr}
	out, err := eng.Play(t.Context(), testResolved(ref))
	if !errors.Is(err, startErr) || out == nil || out.PositionSec != 37 || out.Reason != player.EndError {
		t.Fatalf("Play = (%+v, %v), want persisted journal and original start error", out, err)
	}
}
