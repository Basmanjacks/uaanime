package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMPVSessionReadsEOFEventImmediately(t *testing.T) {
	sess, serverDone := startFakeIPC(t, func(conn net.Conn) {
		_, _ = fmt.Fprintln(conn, `{"event":"end-file","reason":"eof"}`)
	})
	defer sess.Close()

	select {
	case reason := <-sess.End():
		if reason != EndEOF {
			t.Fatalf("End = %q, очікував %q", reason, EndEOF)
		}
	case <-time.After(time.Second):
		t.Fatal("End не отримав подію EOF")
	}
	<-serverDone
}

func TestMPVSessionRequestReturnsWhenReaderCloses(t *testing.T) {
	sess, serverDone := startFakeIPC(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		scanner.Scan()
	})
	defer sess.Close()

	errCh := make(chan error, 1)
	go func() {
		_, err := sess.TimePos()
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("TimePos не повернув помилку після закриття сокета")
		}
	case <-time.After(time.Second):
		t.Fatal("TimePos завис після закриття сокета")
	}
	<-serverDone
}

func startFakeIPC(t *testing.T, serve func(net.Conn)) (*mpvSession, <-chan struct{}) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "mpv.sock")
	// Справжня дитина: сесія без процесу не існує — жнець живе в process.
	sess := newMPVSession(startShell(t, "sleep 30"), dir)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		if !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EPERM) {
			t.Fatalf("Listen: %v", err)
		}
		// Деякі ізольовані середовища забороняють Unix-сокети; net.Pipe зберігає
		// той самий потоковий IPC-протокол для перевірки читача.
		server, client := net.Pipe()
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			defer func() { _ = server.Close() }()
			serve(server)
		}()
		sess.attachIPC(client)
		return sess, serverDone
	}
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer func() { _ = listener.Close() }()
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		serve(conn)
	}()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		_ = listener.Close()
		<-serverDone
		sess.Close()
		t.Fatalf("Dial: %v", err)
	}
	sess.attachIPC(conn)
	return sess, serverDone
}

// Каталог сокета має бути приватним (0700) і зникати після Close: у спільному
// /tmp будь-який локальний користувач міг би слати mpv команди через IPC.
func TestMPVSocketDirectoryIsPrivateAndRemoved(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv не встановлено")
	}
	if !unixSocketsAvailable(t) {
		t.Skip("пісочниця забороняє Unix-сокети")
	}

	withMPVTestArgs(t)
	sess, err := startMPV(context.Background(), "av://lavfi:testsrc2=duration=60", "тест", nil, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	info, err := os.Stat(sess.dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("права каталогу сокета = %o, очікував 700", perm)
	}
	sess.Close()
	if _, err := os.Stat(sess.dir); !os.IsNotExist(err) {
		t.Fatalf("каталог сокета лишився після Close: %v", err)
	}
}

// withMPVTestArgs вмикає ізольований запуск mpv (без конфігу і без виводу)
// і повертає прапорець у попередній стан.
func withMPVTestArgs(t *testing.T) {
	t.Helper()
	old := mpvTestArgs
	mpvTestArgs = []string{"--no-config", "--vo=null", "--ao=null"}
	t.Cleanup(func() { mpvTestArgs = old })
}

func unixSocketsAvailable(t *testing.T) bool {
	t.Helper()
	probe, err := net.Listen("unix", filepath.Join(t.TempDir(), "probe.sock"))
	if err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EPERM) {
			return false
		}
		t.Fatalf("перевірка Unix-сокета: %v", err)
	}
	_ = probe.Close()
	return true
}

// Інтеграційний тест з реальним mpv на синтетичному джерелі (lavfi, без мережі).
// Пропускається, якщо mpv не встановлено (наприклад, у голому CI).
func TestSessionIPC(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv не встановлено")
	}
	if !unixSocketsAvailable(t) {
		t.Skip("пісочниця забороняє Unix-сокети")
	}

	withMPVTestArgs(t)
	sess, err := startMPV(context.Background(), "av://lavfi:testsrc2=duration=60", "тест", nil, 7)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	// --start=7 має бути застосований, а позиція — рухатися. Перше читання може
	// випередити сам seek (mpv віддає time-pos уже на першому кадрі), тому
	// чекаємо саме на позицію після старту, а не на будь-яку ненульову.
	deadline := time.Now().Add(15 * time.Second)
	var pos float64
	for time.Now().Before(deadline) {
		pos, err = sess.TimePos()
		if err == nil && pos >= 6.5 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TimePos: %v", err)
	}
	if pos < 6.5 {
		t.Errorf("--start=7 не застосовано: позиція %v", pos)
	}

	// lavfi не знає повної тривалості наперед — достатньо, що властивість читається
	dur, err := sess.Duration()
	if err != nil || dur <= 0 {
		t.Errorf("Duration = (%v, %v), очікував > 0", dur, err)
	}
}
