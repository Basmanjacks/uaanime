package player

import (
	"bufio"
	"errors"
	"fmt"
	"net"
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
	sock := filepath.Join(t.TempDir(), "mpv.sock")
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
		return newMPVSession(nil, sock, client), serverDone
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
		t.Fatalf("Dial: %v", err)
	}
	return newMPVSession(nil, sock, conn), serverDone
}

// Інтеграційний тест з реальним mpv на синтетичному джерелі (lavfi, без мережі).
// Пропускається, якщо mpv не встановлено (наприклад, у голому CI).
func TestSessionIPC(t *testing.T) {
	if _, err := exec.LookPath("mpv"); err != nil {
		t.Skip("mpv не встановлено")
	}
	probe, err := net.Listen("unix", filepath.Join(t.TempDir(), "probe.sock"))
	if err != nil {
		if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EPERM) {
			t.Skip("пісочниця забороняє Unix-сокети")
		}
		t.Fatalf("перевірка Unix-сокета: %v", err)
	}
	_ = probe.Close()

	sess, err := startMPV("av://lavfi:testsrc2=duration=60", "тест", nil, 7,
		"--no-config", "--vo=null", "--ao=null")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()
	go func() { _ = sess.Wait() }()

	// --start=7 має бути застосований, а позиція — рухатися
	deadline := time.Now().Add(15 * time.Second)
	var pos float64
	for time.Now().Before(deadline) {
		pos, err = sess.TimePos()
		if err == nil && pos > 0 {
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
