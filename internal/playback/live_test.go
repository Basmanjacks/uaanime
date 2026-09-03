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
	if err != nil || snap != idleSnapshot() {
		t.Fatalf("Snapshot(nil) = %+v, %v; очікував простій без помилки", snap, err)
	}
	for name, call := range map[string]func() error{
		"TogglePause": live.TogglePause,
		"Seek":        func() error { return live.Seek(10) },
		"SeekTo":      func() error { return live.SeekTo(10) },
		"AddVolume":   func() error { return live.AddVolume(5) },
		"Next":        live.Next,
		"Stop":        live.Stop,
	} {
		if err := call(); !errors.Is(err, ErrNotPlaying) {
			t.Errorf("%s(nil) = %v, очікував ErrNotPlaying", name, err)
		}
	}
	live.set(nil, "", 0)
	live.clear()
	live.SetStopAfter(true)
	if live.StopAfter() || live.takeStopAfter() {
		t.Fatal("StopAfter(nil) = true, очікував false")
	}
	if intent, req := live.takeIntent(); intent != IntentNone || req != (PlayRequest{}) {
		t.Fatalf("takeIntent(nil) = (%v, %+v)", intent, req)
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
	live := &Live{intent: IntentNext, stopAfter: true, requested: PlayRequest{Gen: 1, Episode: 3}}
	live.set(playertest.NewSession(player.EndQuit, nil, nil), "T", 2)
	if got, req := live.takeIntent(); got != IntentNone || req != (PlayRequest{}) {
		t.Fatalf("залишковий намір протік у нову сесію: %v %+v", got, req)
	}
	if live.StopAfter() {
		t.Fatal("залишковий StopAfter протік у нову сесію")
	}
}

// Прапорець «досидіти й зупинитись» стосується однієї серії: Finish забирає
// його рівно раз, інакше автоплей мовчки помер би й на наступній.
func TestLiveStopAfterIsConsumedOnce(t *testing.T) {
	eng, _, res, _ := liveEngine(t)
	titleID, _, err := eng.Begin(res)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	eng.Live.SetStopAfter(true)
	if !eng.Live.StopAfter() {
		t.Fatal("StopAfter = false одразу після SetStopAfter(true)")
	}

	result, err := eng.Finish(player.EndEOF, titleID, 1)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !result.StopAfter {
		t.Fatalf("Result = %+v, очікував StopAfter", result)
	}
	again, err := eng.Finish(player.EndEOF, titleID, 1)
	if err != nil {
		t.Fatalf("Finish x2: %v", err)
	}
	if again.StopAfter {
		t.Fatalf("StopAfter спожито двічі: %+v", again)
	}
}

// IntentPlay без цілі нічого не означає: намір і Requested мусять дійти до
// Result разом.
func TestLiveEndPlaySurfacesRequest(t *testing.T) {
	eng, _, res, _ := liveEngine(t)
	titleID, _, err := eng.Begin(res)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	want := PlayRequest{Ref: res.Ref, Gen: 2, Episode: 3}
	eng.Live.set(playertest.NewSession(player.EndQuit, nil, nil), "Title", 1)
	eng.Live.mu.Lock()
	eng.Live.requested = want
	eng.Live.mu.Unlock()
	if err := eng.Live.end(IntentPlay); err != nil {
		t.Fatalf("end(IntentPlay): %v", err)
	}

	result, err := eng.Finish(player.EndQuit, titleID, 1)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if result.Intent != IntentPlay || result.Requested != want {
		t.Fatalf("Result = %+v, очікував IntentPlay із %+v", result, want)
	}
	again, err := eng.Finish(player.EndQuit, titleID, 1)
	if err != nil {
		t.Fatalf("Finish x2: %v", err)
	}
	if again.Intent != IntentNone || again.Requested != (PlayRequest{}) {
		t.Fatalf("ціль спожито двічі: %+v", again)
	}
}

// Плейлист: покоління рахується з одиниці, очищення повертає «списку немає», а
// лічильник не відкочується — інакше нове покоління збіглося б зі старим,
// відкритим у браузері.
func TestLivePlaylistGenerations(t *testing.T) {
	live := &Live{}
	ref := provider.TitleRef{Provider: "test", Slug: "a"}
	if live.CurrentGen() != 0 || live.Playlist().Gen != 0 {
		t.Fatalf("порожній Live має покоління 0: %+v", live.Playlist())
	}
	rows := []EpisodeInfo{{Number: 1, Watched: true}, {Number: 2, Current: true}}
	if gen := live.SetPlaylist(ref, "A", rows); gen != 1 {
		t.Fatalf("перша публікація = %d, want 1", gen)
	}
	// Копія, а не покажчик: модель UI перебудовує свій слайс на місці.
	rows[0].Watched = false
	pl := live.Playlist()
	if pl.Gen != 1 || pl.Title != "A" || !pl.Ref.Same(ref) || len(pl.Episodes) != 2 || !pl.Episodes[0].Watched {
		t.Fatalf("Playlist = %+v", pl)
	}
	if gen := live.SetPlaylist(ref, "A", rows); gen != 2 || live.CurrentGen() != 2 {
		t.Fatalf("друга публікація = %d (CurrentGen %d), want 2", gen, live.CurrentGen())
	}
	live.ClearPlaylist()
	if live.CurrentGen() != 0 {
		t.Fatalf("після ClearPlaylist CurrentGen = %d, want 0", live.CurrentGen())
	}
	if gen := live.SetPlaylist(ref, "A", rows); gen != 3 {
		t.Fatalf("публікація після очищення = %d, want 3 (лічильник не відкочується)", gen)
	}
}

// Запит із чужим поколінням не має ні закривати сесію, ні лягати у скриньку.
func TestLiveRequestPlayRejectsStaleGen(t *testing.T) {
	live := &Live{}
	ref := provider.TitleRef{Provider: "test", Slug: "a"}
	gen := live.SetPlaylist(ref, "A", []EpisodeInfo{{Number: 1}})
	for name, arg := range map[string]int{"нуль": 0, "старе": gen - 1, "майбутнє": gen + 1} {
		if err := live.RequestPlay(arg, 2); !errors.Is(err, ErrStalePlaylist) {
			t.Errorf("RequestPlay(%s=%d) = %v, want ErrStalePlaylist", name, arg, err)
		}
	}
	live.ClearPlaylist()
	if err := live.RequestPlay(gen, 2); !errors.Is(err, ErrStalePlaylist) {
		t.Errorf("RequestPlay після очищення = %v, want ErrStalePlaylist", err)
	}
	select {
	case req := <-live.Requests():
		t.Fatalf("застарілий запит потрапив у скриньку: %+v", req)
	default:
	}
}

// Під час гри адресний запит закриває сесію з наміром IntentPlay: далі працює
// звичайний ланцюжок Finish → Result.Requested, спільний для TUI і headless.
func TestLiveRequestPlayEndsSessionWithIntent(t *testing.T) {
	eng, _, res, _ := liveEngine(t)
	titleID, _, err := eng.Begin(res)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	gen := eng.Live.SetPlaylist(res.Ref, "Title", []EpisodeInfo{{Number: 1, Current: true}, {Number: 3}})
	done := make(chan player.EndReason, 1)
	go func() {
		reason, err := eng.Run(context.Background(), res, titleID)
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- reason
	}()
	waitPlaying(t, eng.Live)

	if err := eng.Live.RequestPlay(gen, 3); err != nil {
		t.Fatalf("RequestPlay: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RequestPlay не закрив сесію")
	}
	result, err := eng.Finish(player.EndQuit, titleID, 1)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	want := PlayRequest{Ref: res.Ref, Gen: gen, Episode: 3}
	if result.Intent != IntentPlay || result.Requested != want {
		t.Fatalf("Result = %+v, want IntentPlay із %+v", result, want)
	}
	// Під час гри скринька не використовується: запит пішов через сесію.
	select {
	case req := <-eng.Live.Requests():
		t.Fatalf("запит продубльовано у скриньку: %+v", req)
	default:
	}
}

// У простої запит лягає у скриньку, і переповнення її не блокує: значення має
// лише останній тап.
func TestLiveRequestPlayMailboxKeepsLatest(t *testing.T) {
	live := &Live{}
	ref := provider.TitleRef{Provider: "test", Slug: "a"}
	gen := live.SetPlaylist(ref, "A", []EpisodeInfo{{Number: 1}})
	for _, ep := range []int{2, 5, 7} {
		if err := live.RequestPlay(gen, ep); err != nil {
			t.Fatalf("RequestPlay(%d): %v", ep, err)
		}
	}
	select {
	case req := <-live.Requests():
		if req != (PlayRequest{Ref: ref, Gen: gen, Episode: 7}) {
			t.Fatalf("зі скриньки = %+v, want останній тап (серія 7)", req)
		}
	case <-time.After(time.Second):
		t.Fatal("скринька порожня")
	}
	select {
	case req := <-live.Requests():
		t.Fatalf("у скриньці лишився ще один запит: %+v", req)
	default:
	}
}

func TestLivePlaylistNilIsSafe(t *testing.T) {
	var live *Live
	live.ClearPlaylist()
	if live.CurrentGen() != 0 || live.Playlist().Gen != 0 || live.SetPlaylist(provider.TitleRef{}, "", nil) != 0 {
		t.Fatal("nil-Live має поводитися як порожній")
	}
	if err := live.RequestPlay(1, 1); !errors.Is(err, ErrStalePlaylist) {
		t.Fatalf("RequestPlay(nil) = %v, want ErrStalePlaylist", err)
	}
	if live.Requests() != nil {
		t.Fatal("Requests(nil) має бути nil-каналом")
	}
}

// Гучність: крок читає поточну й ставить абсолютну, обрізану до 0..100.
func TestLiveAddVolumeClamps(t *testing.T) {
	for _, tt := range []struct {
		name  string
		start float64
		delta float64
		want  float64
	}{
		{name: "звичайний крок", start: 60, delta: 5, want: 65},
		{name: "стеля", start: 98, delta: 5, want: 100},
		{name: "підлога", start: 3, delta: -5, want: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sess := playertest.NewSession(player.EndQuit, nil, nil)
			sess.VolumePct = tt.start
			live := &Live{}
			live.set(sess, "T", 1)

			if err := live.AddVolume(tt.delta); err != nil {
				t.Fatalf("AddVolume: %v", err)
			}
			want := []playertest.Call{{Op: "volume", Delta: tt.want}}
			if got := sess.Calls(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Calls = %+v, очікував %+v", got, want)
			}
		})
	}
}

// Помилка гучності не має валити снапшот: пульт без повзунка кращий за пульт
// без позиції та кнопок.
func TestSnapshotSurvivesVolumeError(t *testing.T) {
	live := &Live{}
	live.set(mutedSession{playertest.NewSession(player.EndQuit, []float64{12}, []float64{100})}, "T", 1)
	live.SetStopAfter(true)

	snap, err := live.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.VolumePct != VolumeUnknown || !snap.StopAfter || snap.PositionSec != 12 {
		t.Fatalf("Snapshot = %+v, очікував невідому гучність і StopAfter", snap)
	}
}

// mutedSession — сесія, у якої зламана лише гучність.
type mutedSession struct{ *playertest.Session }

func (mutedSession) Volume() (float64, error) { return 0, errBroken }

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
