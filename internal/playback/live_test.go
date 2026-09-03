package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func TestLiveNilIsSafe(t *testing.T) {
	var live *Live
	snap, err := live.Snapshot()
	if err != nil || snap != (Snapshot{}) {
		t.Fatalf("Snapshot(nil) = %+v, %v; очікував нуль без помилки", snap, err)
	}
	for name, call := range map[string]func() error{
		"TogglePause": live.TogglePause,
		"Seek":        func() error { return live.Seek(10) },
		"Next":        live.Next,
		"Stop":        live.Stop,
	} {
		if err := call(); !errors.Is(err, ErrNotPlaying) {
			t.Errorf("%s(nil) = %v, очікував ErrNotPlaying", name, err)
		}
	}
	live.set(nil, "", 0)
	live.clear()
	if got := live.takeIntent(); got != IntentNone {
		t.Fatalf("takeIntent(nil) = %v", got)
	}
}

// liveEngine — рушій із утримуваною сесією playertest: Run не завершиться,
// доки сесію не закриє пульт.
func liveEngine(t *testing.T) (*Engine, *playertest.Session, *Resolved, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	lib := &library.Library{}
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title", Name: "Title"}
	lib.EnsureTitle(ref, func() string { return "title-id" })
	sess := playertest.NewSession(player.EndQuit, []float64{60, 65, 70}, []float64{1200})
	sess.Hold = true
	eng := &Engine{
		Store:           st,
		Lib:             lib,
		Player:          &playertest.Player{Sessions: []*playertest.Session{sess}},
		Live:            &Live{},
		JournalInterval: 10 * time.Millisecond,
	}
	res := testResolved(ref)
	res.Name = "Title"
	return eng, sess, res, dir
}

// waitJournal чекає першого запису журналу: пульт закриває сесію без
// додаткового семплу, тож збережена позиція залежить від тіка Run.
func waitJournal(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "state", "current.json")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Run не записав журнал")
}

// waitPlaying чекає, поки Run відкриє вікно: set викликається вже ПІСЛЯ
// повернення Start, тому <-Started тут замало.
func waitPlaying(t *testing.T, live *Live) Snapshot {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := live.Snapshot()
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if snap.Playing {
			return snap
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Live так і не побачив сесію")
	return Snapshot{}
}

func TestLiveControlsAndIntent(t *testing.T) {
	for _, tt := range []struct {
		name string
		end  func(*Live) error
		want Intent
	}{
		{"next", (*Live).Next, IntentNext},
		{"stop", (*Live).Stop, IntentStop},
	} {
		t.Run(tt.name, func(t *testing.T) {
			eng, sess, res, dir := liveEngine(t)
			titleID, _, err := eng.Begin(res)
			if err != nil {
				t.Fatalf("Begin: %v", err)
			}
			done := make(chan player.EndReason, 1)
			go func() {
				reason, err := eng.Run(context.Background(), res, titleID)
				if err != nil {
					t.Errorf("Run: %v", err)
				}
				done <- reason
			}()

			snap := waitPlaying(t, eng.Live)
			if snap.Title != "Title" || snap.Episode != 1 || snap.DurationSec != 1200 {
				t.Fatalf("Snapshot = %+v", snap)
			}
			if err := eng.Live.TogglePause(); err != nil {
				t.Fatalf("TogglePause: %v", err)
			}
			if err := eng.Live.Seek(-10); err != nil {
				t.Fatalf("Seek: %v", err)
			}
			if snap, err := eng.Live.Snapshot(); err != nil || !snap.Paused {
				t.Fatalf("після TogglePause Snapshot = %+v, %v; очікував Paused", snap, err)
			}
			want := []playertest.Call{{Op: "pause"}, {Op: "seek", Delta: -10}}
			if got := sess.Calls(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Calls = %+v, очікував %+v", got, want)
			}

			waitJournal(t, dir)
			if err := tt.end(eng.Live); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			// одразу після команди вікно вже порожнє — HTTP відповідає 200 idle
			if snap, err := eng.Live.Snapshot(); err != nil || snap.Playing {
				t.Fatalf("Snapshot після %s = %+v, %v; очікував idle", tt.name, snap, err)
			}
			select {
			case reason := <-done:
				if reason != player.EndQuit {
					t.Fatalf("Run = %q, очікував quit", reason)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Run не завершився після закриття сесії пультом")
			}
			if err := eng.Live.Next(); !errors.Is(err, ErrNotPlaying) {
				t.Fatalf("Next після Run = %v, очікував ErrNotPlaying", err)
			}

			result, err := eng.Finish(player.EndQuit, titleID, 1)
			if err != nil {
				t.Fatalf("Finish: %v", err)
			}
			if result.Intent != tt.want || result.Completed {
				t.Fatalf("Result = %+v, очікував Intent=%v без Completed", result, tt.want)
			}
			if result.PositionSec == 0 {
				t.Fatalf("Result = %+v, очікував збережену позицію", result)
			}
			again, err := eng.Finish(player.EndQuit, titleID, 1)
			if err != nil {
				t.Fatalf("Finish x2: %v", err)
			}
			if again.Intent != IntentNone {
				t.Fatalf("намір спожито двічі: %v", again.Intent)
			}
		})
	}
}

func TestLiveSetResetsLeftoverIntent(t *testing.T) {
	live := &Live{intent: IntentNext}
	live.set(playertest.NewSession(player.EndQuit, nil, nil), "T", 2)
	if got := live.takeIntent(); got != IntentNone {
		t.Fatalf("залишковий намір протік у нову сесію: %v", got)
	}
}

func TestSnapshotSurfacesSessionError(t *testing.T) {
	live := &Live{}
	live.set(failingSession{}, "T", 1)
	if _, err := live.Snapshot(); !errors.Is(err, errBroken) {
		t.Fatalf("Snapshot = %v, очікував помилку сесії", err)
	}
}

var errBroken = errors.New("зламано")

type failingSession struct{}

var _ player.Session = failingSession{}

func (failingSession) TimePos() (float64, error)    { return 0, errBroken }
func (failingSession) Duration() (float64, error)   { return 0, errBroken }
func (failingSession) TogglePause() error           { return errBroken }
func (failingSession) Paused() (bool, error)        { return false, errBroken }
func (failingSession) Seek(float64) error           { return errBroken }
func (failingSession) SeekTo(float64) error         { return errBroken }
func (failingSession) Volume() (float64, error)     { return 0, errBroken }
func (failingSession) SetVolume(float64) error      { return errBroken }
func (failingSession) End() <-chan player.EndReason { return nil }
func (failingSession) Wait() error                  { return nil }
func (failingSession) Close()                       {}
