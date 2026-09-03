package player

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestVLCCommandWithoutStart(t *testing.T) {
	cmd := (VLC{}).Command(
		"https://x/i.m3u8",
		"Тайтл · 1",
		map[string]string{
			"Referer":    "https://x/",
			"User-Agent": "ua",
			"X-Ignored":  "значення",
		},
		0,
	)
	want := []string{
		"vlc",
		"--play-and-exit",
		"--fullscreen",
		"--quiet",
		"--meta-title=Тайтл · 1",
		"--http-referrer=https://x/",
		"--http-user-agent=ua",
		"https://x/i.m3u8",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}

func TestVLCCommandWithStart(t *testing.T) {
	cmd := (VLC{}).Command("u", "t", nil, 93.5)
	want := []string{
		"vlc",
		"--play-and-exit",
		"--fullscreen",
		"--quiet",
		"--meta-title=t",
		"--start-time=93.5",
		"u",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}

func TestVLCStartReportsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	old := vlcDarwinBundlePaths
	vlcDarwinBundlePaths = nil
	t.Cleanup(func() { vlcDarwinBundlePaths = old })

	_, err := (VLC{}).Start(context.Background(), "u", "t", nil, 0)
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Start error = %v, очікував обгортку exec.ErrNotFound", err)
	}
}

func TestVLCSessionParsesIntegerRepliesAfterNoise(t *testing.T) {
	sess, serverDone := startFakeVLCIPC(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			_, _ = fmt.Fprintln(conn, "VLC media player 3.0.17.3 Vetinari")
			_, _ = fmt.Fprintln(conn, "Command Line Interface initialized. Type `help' for help.")
			_, _ = fmt.Fprintln(conn, "status change: ( new input: file:///tmp/test )")
			switch scanner.Text() {
			case "get_time":
				_, _ = fmt.Fprintln(conn, "> 42")
			case "get_length":
				_, _ = fmt.Fprintln(conn, "> 120")
			}
		}
	})
	defer func() {
		sess.Close()
		<-serverDone
	}()

	pos, err := sess.TimePos()
	if err != nil || pos != 42 {
		t.Fatalf("TimePos = (%v, %v), очікував (42, nil)", pos, err)
	}
	dur, err := sess.Duration()
	if err != nil || dur != 120 {
		t.Fatalf("Duration = (%v, %v), очікував (120, nil)", dur, err)
	}
}

func TestVLCSessionFallsBackToCachedValues(t *testing.T) {
	sess, serverDone := startFakeVLCIPC(t, replyRC(17, 80))

	if _, err := sess.TimePos(); err != nil {
		t.Fatalf("TimePos: %v", err)
	}
	if _, err := sess.Duration(); err != nil {
		t.Fatalf("Duration: %v", err)
	}
	sess.Close() // RC закрито: далі відповідати нікому
	<-serverDone

	pos, err := sess.TimePos()
	if err != nil || pos != 17 {
		t.Fatalf("TimePos = (%v, %v), очікував кешоване (17, nil)", pos, err)
	}
	dur, err := sess.Duration()
	if err != nil || dur != 80 {
		t.Fatalf("Duration = (%v, %v), очікував кешоване (80, nil)", dur, err)
	}
}

func TestVLCSessionTogglePauseWritesOnceWithoutReply(t *testing.T) {
	commands := make(chan string, 1)
	sess, serverDone := startFakeVLCIPC(t, recordVLCCommands(commands, 1))
	defer func() {
		sess.Close()
		<-serverDone
	}()

	done := make(chan error, 1)
	go func() { done <- sess.TogglePause() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("TogglePause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TogglePause завис")
	}
	if got := <-commands; got != "pause" {
		t.Fatalf("команда = %q, очікував pause", got)
	}
}

func TestVLCSessionSeekWritesRoundedRelativeCommands(t *testing.T) {
	commands := make(chan string, 2)
	sess, serverDone := startFakeVLCIPC(t, recordVLCCommands(commands, 2))
	defer func() {
		sess.Close()
		<-serverDone
	}()

	done := make(chan error, 1)
	go func() {
		if err := sess.Seek(10); err != nil {
			done <- err
			return
		}
		done <- sess.Seek(-10)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Seek: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Seek завис")
	}

	if got := <-commands; got != "seek +10" {
		t.Fatalf("перша команда = %q, очікував seek +10", got)
	}
	if got := <-commands; got != "seek -10" {
		t.Fatalf("друга команда = %q, очікував seek -10", got)
	}
}

// Знак у RC означає відносний seek, тож абсолютна ціль мусить іти без нього:
// "seek +42" перемотало б на 42 с ВПЕРЕД замість позиції 42.
func TestVLCSessionSeekToWritesAbsoluteCommand(t *testing.T) {
	commands := make(chan string, 2)
	sess, serverDone := startFakeVLCIPC(t, recordVLCCommands(commands, 2))
	defer func() {
		sess.Close()
		<-serverDone
	}()

	done := make(chan error, 1)
	go func() {
		if err := sess.SeekTo(42.4); err != nil {
			done <- err
			return
		}
		done <- sess.Seek(30)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SeekTo: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SeekTo завис")
	}

	if got := <-commands; got != "seek 42" {
		t.Fatalf("перша команда = %q, очікував seek 42", got)
	}
	if got := <-commands; got != "seek +30" {
		t.Fatalf("друга команда = %q, очікував seek +30", got)
	}
}

func TestVLCSessionPausedReadsStatus(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  bool
	}{
		{name: "на паузі", state: "paused", want: true},
		{name: "відтворюється", state: "playing", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, serverDone := startFakeVLCIPC(t, func(conn net.Conn) {
				scanner := bufio.NewScanner(conn)
				for scanner.Scan() {
					if scanner.Text() == "status" {
						_, _ = fmt.Fprintf(conn, "> ( new input: file:///x )\r\n( audio volume: 0 )\r\n( state %s )\r\n", tt.state)
					}
				}
			})
			defer func() {
				sess.Close()
				<-serverDone
			}()

			paused, err := sess.Paused()
			if err != nil || paused != tt.want {
				t.Fatalf("Paused = (%v, %v), очікував (%v, nil)", paused, err, tt.want)
			}
		})
	}
}

func TestVLCSessionPausedTimesOutWithoutState(t *testing.T) {
	oldRequestTimeout, oldAttemptTimeout := vlcRequestTimeout, vlcAttemptTimeout
	vlcRequestTimeout = 150 * time.Millisecond
	vlcAttemptTimeout = 30 * time.Millisecond
	t.Cleanup(func() {
		vlcRequestTimeout = oldRequestTimeout
		vlcAttemptTimeout = oldAttemptTimeout
	})

	sess, serverDone := startFakeVLCIPC(t, func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			_, _ = fmt.Fprint(conn, "> ")
		}
	})
	defer func() {
		sess.Close()
		<-serverDone
	}()

	_, err := sess.Paused()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Paused error = %v, очікував context.DeadlineExceeded", err)
	}
}

func TestVLCSessionControlsFailAfterClose(t *testing.T) {
	sess, serverDone := startFakeVLCIPC(t, func(conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
	})
	sess.Close()
	<-serverDone

	if err := sess.TogglePause(); !errors.Is(err, errs.ErrPlayer) {
		t.Errorf("TogglePause error = %v, очікував ErrPlayer", err)
	}
	if _, err := sess.Paused(); !errors.Is(err, errs.ErrPlayer) {
		t.Errorf("Paused error = %v, очікував ErrPlayer", err)
	}
	if err := sess.Seek(1); !errors.Is(err, errs.ErrPlayer) {
		t.Errorf("Seek error = %v, очікував ErrPlayer", err)
	}
}

func recordVLCCommands(commands chan<- string, count int) func(net.Conn) {
	return func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for i := 0; i < count && scanner.Scan(); i++ {
			commands <- scanner.Text()
			_, _ = fmt.Fprint(conn, "> ")
		}
		_, _ = io.Copy(io.Discard, conn)
	}
}

func TestVLCSessionCleanExitReason(t *testing.T) {
	tests := []struct {
		name    string
		pos     int
		dur     int
		sampled bool
		want    EndReason
	}{
		{name: "на межі EOF", pos: 95, dur: 100, sampled: true, want: EndEOF},
		{name: "нижче межі", pos: 94, dur: 100, sampled: true, want: EndQuit},
		{name: "без вимірів", want: EndQuit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess, serverDone := startFakeVLCIPC(t, replyRC(tt.pos, tt.dur))
			defer func() {
				sess.Close()
				<-serverDone
			}()
			if tt.sampled {
				if _, err := sess.TimePos(); err != nil {
					t.Fatalf("TimePos: %v", err)
				}
				if _, err := sess.Duration(); err != nil {
					t.Fatalf("Duration: %v", err)
				}
			}
			if got := sess.endReasonOnCleanExit(); got != tt.want {
				t.Fatalf("endReasonOnCleanExit = %q, очікував %q", got, tt.want)
			}
		})
	}
}

func startFakeVLCIPC(t *testing.T, serve func(net.Conn)) (*vlcSession, <-chan struct{}) {
	t.Helper()
	// Справжня дитина: сесія без процесу не існує — жнець живе в process.
	sess := newVLCSession(startShell(t, "sleep 30"))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
			t.Fatalf("Listen: %v", err)
		}
		// Деякі пісочниці забороняють навіть loopback; net.Pipe зберігає той
		// самий потоковий RC-протокол, а звичайний шлях тесту лишається TCP.
		server, client := net.Pipe()
		serverDone := make(chan struct{})
		go func() {
			defer close(serverDone)
			defer func() { _ = server.Close() }()
			serve(server)
		}()
		sess.attachRC(client)
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
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		_ = listener.Close()
		<-serverDone
		sess.Close()
		t.Fatalf("Dial: %v", err)
	}
	sess.attachRC(conn)
	return sess, serverDone
}

// replyRC — фейковий RC-сервер, що віддає задані pos/dur після типового шуму.
func replyRC(pos, dur int) func(net.Conn) {
	return func(conn net.Conn) {
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			switch scanner.Text() {
			case "get_time":
				_, _ = fmt.Fprintf(conn, "> %d\n", pos)
			case "get_length":
				_, _ = fmt.Fprintf(conn, "> %d\n", dur)
			}
		}
	}
}

// Інтеграційний тест з реальним VLC на локальному синтетичному WAV без мережі.
// Пропускається, якщо VLC не встановлено (наприклад, у голому CI).
func TestVLCSessionIPC(t *testing.T) {
	if findVLCBinary(runtime.GOOS) == "" {
		t.Skip("VLC не встановлено")
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("пісочниця забороняє TCP loopback")
		}
		t.Fatalf("перевірка TCP loopback: %v", err)
	}
	_ = probe.Close()

	mediaPath := filepath.Join(t.TempDir(), "silence.wav")
	// 10 с тиші: з --play-and-exit коротший файл встигає закінчитися до
	// першого запиту, і VLC закриває TCP — тест ставав флейкі.
	writeSilentWAV(t, mediaPath, 10)
	sess, err := (VLC{}).Start(context.Background(), mediaPath, "тест", nil, 0)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sess.Close()

	if _, err := sess.TimePos(); err != nil {
		t.Fatalf("TimePos: %v", err)
	}
	if _, err := sess.Duration(); err != nil {
		t.Fatalf("Duration: %v", err)
	}
}

func writeSilentWAV(t *testing.T, path string, seconds int) {
	t.Helper()
	const sampleRate = 8000
	const bytesPerSample = 2
	dataSize := sampleRate * seconds * bytesPerSample

	var wav bytes.Buffer
	wav.WriteString("RIFF")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(36+dataSize))
	wav.WriteString("WAVEfmt ")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(16))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(1))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&wav, binary.LittleEndian, uint32(sampleRate*bytesPerSample))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(bytesPerSample))
	_ = binary.Write(&wav, binary.LittleEndian, uint16(8*bytesPerSample))
	wav.WriteString("data")
	_ = binary.Write(&wav, binary.LittleEndian, uint32(dataSize))
	wav.Write(make([]byte, dataSize))
	if err := os.WriteFile(path, wav.Bytes(), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
