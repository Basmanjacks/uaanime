package download

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/errs"
)

// waitFor крутить Snapshot, доки умова не стане правдою. Читати Events тут
// навмисно не можна: стан менеджера має бути повним і без споживача каналу.
func waitFor(t *testing.T, m *Manager, what string, cond func([]Progress) bool) []Progress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap := m.Snapshot()
		if cond(snap) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("не дочекалися: %s; знімок = %+v", what, snap)
		}
		time.Sleep(time.Millisecond)
	}
}

func stateOf(snap []Progress, id int64) State {
	for _, p := range snap {
		if p.ID == id {
			return p.State
		}
	}
	return StateQueued
}

func TestManagerRunsJobsOneAtATime(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	t.Cleanup(m.Close)

	first, err := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	second, err := m.Enqueue(jobFor(dir, refFrieren, 7, 720, s.URL()))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	waitFor(t, m, "перше завдання пішло в роботу", func(snap []Progress) bool {
		return stateOf(snap, first) == StateRunning
	})
	if got := stateOf(m.Snapshot(), second); got != StateQueued {
		t.Fatalf("друге завдання = %v, мало чекати черги", got)
	}

	close(release)
	waitFor(t, m, "обидва завершилися", func(snap []Progress) bool {
		return stateOf(snap, first) == StateDone && stateOf(snap, second) == StateDone
	})
}

func TestManagerDuplicateEnqueueReturnsSameID(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	t.Cleanup(m.Close)

	first, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	again, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	if first != again {
		t.Fatalf("id = %d і %d — друге натискання D не має створювати друге завдання", first, again)
	}
	if got := len(m.Snapshot()); got != 1 {
		t.Fatalf("у знімку %d записів", got)
	}
}

func TestManagerCancelQueuedNeverStartsIt(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	busy := downloadtest.NewHLS(t, downloadtest.HLSOptions{Host: "busy.test", Segments: 8, Referer: streamHeaders["Referer"]})
	idle := downloadtest.NewHLS(t, downloadtest.HLSOptions{Host: "idle.test", Segments: 4, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	busy.BlockSegment(5, release)

	rewrites := busy.Rewrites()
	for k, v := range idle.Rewrites() {
		rewrites[k] = v
	}
	m := NewManager(fetcherFor(downloadtest.Transport(rewrites)))
	t.Cleanup(m.Close)

	first, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, busy.URL()))
	second, _ := m.Enqueue(jobFor(dir, refFrieren, 7, 720, idle.URL()))
	waitFor(t, m, "перше завдання пішло в роботу", func(snap []Progress) bool {
		return stateOf(snap, first) == StateRunning
	})

	if !m.Cancel(second) {
		t.Fatal("Cancel завдання з черги повернув false")
	}
	close(release)
	waitFor(t, m, "перше завершилося", func(snap []Progress) bool {
		return stateOf(snap, first) == StateDone
	})
	// Дати планувальнику шанс помилково взяти скасоване завдання.
	time.Sleep(50 * time.Millisecond)
	if got := stateOf(m.Snapshot(), second); got != StateCancelled {
		t.Fatalf("скасоване завдання = %v", got)
	}
	if n := idle.RequestCount(); n != 0 {
		t.Fatalf("скасоване завдання зробило %d запитів", n)
	}
}

func TestManagerForgetOnlyInactive(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	t.Cleanup(m.Close)

	first, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	second, _ := m.Enqueue(jobFor(dir, refFrieren, 7, 720, s.URL()))
	waitFor(t, m, "перше завдання пішло в роботу", func(snap []Progress) bool {
		return stateOf(snap, first) == StateRunning
	})

	if m.Forget(first) {
		t.Error("Forget прибрав активне завдання")
	}
	if !m.Forget(second) {
		t.Error("Forget не прибрав завдання з черги")
	}
	close(release)
	waitFor(t, m, "перше завершилося", func(snap []Progress) bool {
		return stateOf(snap, first) == StateDone
	})
	if !m.Forget(first) {
		t.Error("Forget не прибрав завершене завдання")
	}
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("знімок = %+v, очікували порожній", got)
	}
	if m.Forget(first) {
		t.Error("Forget неіснуючого завдання повернув true")
	}
}

func TestManagerCloseCancelsActiveAndCleansUp(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	id, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	waitFor(t, m, "завдання пішло в роботу", func(snap []Progress) bool {
		return stateOf(snap, id) == StateRunning
	})

	m.Close()
	m.Close() // ідемпотентність: Close кличуть і модель, і defer у Run

	snap := m.Snapshot()
	if got := stateOf(snap, id); got != StateCancelled {
		t.Fatalf("стан після Close = %v", got)
	}
	if !errors.Is(snap[0].Err, errs.ErrCancelled) {
		t.Errorf("Err = %v, очікували ErrCancelled", snap[0].Err)
	}
	// У каналі ємності 1 може лежати останній сигнал — його читаємо, далі має
	// бути саме закриття, а не блокування.
	closed := false
	for range 2 {
		select {
		case _, ok := <-m.Events():
			if !ok {
				closed = true
			}
		case <-time.After(time.Second):
			t.Fatal("канал подій не закрито: читання заблокувалося")
		}
		if closed {
			break
		}
	}
	if !closed {
		t.Error("канал подій має бути закритий")
	}

	folder := filepath.Dir(snap[0].Path)
	for _, name := range entries(t, folder) {
		if strings.HasSuffix(name, partSuffix) || !strings.HasPrefix(name, ".") {
			t.Errorf("після Close лишилося %q", name)
		}
	}
	if _, err := m.Enqueue(jobFor(dir, refFrieren, 7, 720, s.URL())); err == nil {
		t.Error("Enqueue після Close має відмовляти")
	}
}

func TestManagerCloseWithoutJobs(t *testing.T) {
	m := NewManager(fetcherFor(downloadtest.Transport(nil)))
	m.Close()
	m.Close()
	if got := m.Snapshot(); len(got) != 0 {
		t.Fatalf("знімок = %+v", got)
	}
}

// Сигнали коалесуються: канал ємності 1 не накопичує чергу подій, бо стан
// читають зі Snapshot, а не з каналу.
func TestManagerEventsCoalesce(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	t.Cleanup(m.Close)

	for ep := 6; ep < 12; ep++ {
		if _, err := m.Enqueue(jobFor(dir, refFrieren, ep, 720, s.URL())); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	waitFor(t, m, "перше завдання пішло в роботу", func(snap []Progress) bool {
		return snap[0].State == StateRunning
	})
	if got := len(m.Events()); got > 1 {
		t.Fatalf("у каналі %d сигналів — вони мають коалесуватися", got)
	}
	<-m.Events()
	select {
	case <-m.Events():
		t.Fatal("після читання в каналі лишився ще один сигнал")
	default:
	}
}

func TestManagerRunsFailedJobAndKeepsItInSnapshot(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	s.FailSegment(1, 99, http.StatusInternalServerError)

	m := NewManager(fetcherFor(downloadtest.Transport(s.Rewrites())))
	t.Cleanup(m.Close)

	id, _ := m.Enqueue(Job{Ref: refFrieren, Episode: 6, Height: 720, Dir: dir, Resolve: jobFor(dir, refFrieren, 6, 720, s.URL()).Resolve})
	snap := waitFor(t, m, "завдання провалилося", func(snap []Progress) bool {
		return stateOf(snap, id) == StateFailed
	})
	if snap[0].Err == nil {
		t.Error("провалене завдання без помилки")
	}
	// Після провалу ключ звільняється: повторне D має створити нове завдання.
	again, _ := m.Enqueue(jobFor(dir, refFrieren, 6, 720, s.URL()))
	if again == id {
		t.Error("повторна постановка після провалу віддала старий id")
	}
}
