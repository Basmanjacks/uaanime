package player

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// classifyChild — мінімальний exit-контракт: власне закриття → EndQuit,
// самовільне падіння → EndError, чистий вихід → EndQuit.
func classifyChild(waitErr error, closing bool) EndReason {
	switch {
	case closing:
		return EndQuit
	case waitErr != nil:
		return EndError
	default:
		return EndQuit
	}
}

// startShell дає справжню дитину: process — єдиний жнець, тому тести не
// викликають cmd.Wait самі (інакше гонка за статус виходу).
func startShell(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("тест покладається на POSIX-оболонку")
	}
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill() // без Wait: жнець живе в process
		}
	})
	return cmd
}

func waitEnd(t *testing.T, p *process) EndReason {
	t.Helper()
	select {
	case reason := <-p.End():
		return reason
	case <-time.After(5 * time.Second):
		t.Fatal("End не отримав причину завершення")
		return ""
	}
}

func assertNoSecondEnd(t *testing.T, p *process) {
	t.Helper()
	select {
	case extra := <-p.End():
		t.Fatalf("друга публікація EndReason: %q", extra)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestProcessCloseReportsQuitNotError(t *testing.T) {
	p := newProcess(startShell(t, "sleep 5"), classifyChild)

	waitDone := make(chan error, 1)
	go func() { waitDone <- p.Wait() }()

	// Close із двох горутин одночасно: жодної паніки і рівно одна публікація.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Close()
		}()
	}
	wg.Wait()
	p.Close()

	if reason := waitEnd(t, p); reason != EndQuit {
		t.Fatalf("End = %q, очікував %q", reason, EndQuit)
	}
	assertNoSecondEnd(t, p)

	select {
	case err := <-waitDone:
		if err == nil {
			t.Fatal("Wait повернув nil після Kill, очікував помилку сигналу")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait не повернувся після Close")
	}
}

func TestProcessCrashReportsError(t *testing.T) {
	p := newProcess(startShell(t, "exit 1"), classifyChild)
	if reason := waitEnd(t, p); reason != EndError {
		t.Fatalf("End = %q, очікував %q", reason, EndError)
	}
	if err := p.Wait(); err == nil {
		t.Fatal("Wait повернув nil для exit 1")
	}
	assertNoSecondEnd(t, p)
}

func TestProcessCleanExitReportsQuit(t *testing.T) {
	p := newProcess(startShell(t, "exit 0"), classifyChild)
	if reason := waitEnd(t, p); reason != EndQuit {
		t.Fatalf("End = %q, очікував %q", reason, EndQuit)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait = %v, очікував nil", err)
	}
	assertNoSecondEnd(t, p)
}

// Публікація сесії (для mpv — подія end-file з IPC) має пріоритет над
// класифікацією виходу, і разом вони дають рівно одну причину.
func TestProcessSessionPublishWinsOverExit(t *testing.T) {
	p := newProcess(startShell(t, "sleep 0.2; exit 1"), classifyChild)
	p.publish(EndEOF)

	if reason := waitEnd(t, p); reason != EndEOF {
		t.Fatalf("End = %q, очікував %q", reason, EndEOF)
	}
	_ = p.Wait()
	assertNoSecondEnd(t, p)
}

func TestDialRetryStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := dialRetry(ctx, nil, "unix", filepath.Join(t.TempDir(), "absent.sock"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("dialRetry error = %v, очікував context.Canceled", err)
	}
	if !errors.Is(err, errs.ErrPlayer) {
		t.Fatalf("dialRetry error = %v, очікував обгортку ErrPlayer", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dialRetry чекав %v після скасування", elapsed)
	}
}

func TestDialRetryWaitsForLateListener(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "late.sock")
	probe, err := net.Listen("unix", filepath.Join(t.TempDir(), "probe.sock"))
	if err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EPERM) {
			t.Skip("пісочниця забороняє Unix-сокети")
		}
		t.Fatalf("перевірка Unix-сокета: %v", err)
	}
	_ = probe.Close()

	listening := make(chan net.Listener, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		listener, err := net.Listen("unix", sock)
		if err != nil {
			close(listening)
			return
		}
		listening <- listener
	}()

	conn, err := dialRetry(context.Background(), nil, "unix", sock)
	if err != nil {
		t.Fatalf("dialRetry: %v", err)
	}
	_ = conn.Close()
	if listener := <-listening; listener != nil {
		_ = listener.Close()
	}
}

func TestDialRetryStopsWhenPlayerDies(t *testing.T) {
	dead := make(chan struct{})
	close(dead)
	start := time.Now()
	_, err := dialRetry(context.Background(), dead, "unix", filepath.Join(t.TempDir(), "absent.sock"))
	if err == nil || !errors.Is(err, errs.ErrPlayer) {
		t.Fatalf("очікував помилку ErrPlayer, отримав %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("мертвий плеєр має уривати очікування одразу, минуло %v", time.Since(start))
	}
}
