package player

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"
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
