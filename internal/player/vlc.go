// Package player — запуск зовнішнього VLC.
package player

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"time"
)

// VLC — бекенд зовнішнього плеєра VLC.
type VLC struct{}

func (VLC) ID() string { return "vlc" }

// Command збирає детерміновану команду VLC без пошуку бінарного файла.
func (VLC) Command(streamURL, mediaTitle string, headers map[string]string, startSec float64) *exec.Cmd {
	args := []string{
		"--play-and-exit",
		"--quiet",
		"--meta-title=" + mediaTitle,
	}
	// VLC CLI відкриває лише ці два HTTP-заголовки; ashdi віддає саме їх
	// (перевірено 2026-08-31), тому решту ключів навмисно ігноруємо.
	if referer := headers["Referer"]; referer != "" {
		args = append(args, "--http-referrer="+referer)
	}
	if userAgent := headers["User-Agent"]; userAgent != "" {
		args = append(args, "--http-user-agent="+userAgent)
	}
	if startSec > 0 {
		args = append(args, fmt.Sprintf("--start-time=%.1f", startSec))
	}
	args = append(args, streamURL)
	return exec.Command("vlc", args...)
}

func (p VLC) Start(streamURL, mediaTitle string, headers map[string]string, startSec float64) (Session, error) {
	binary := findVLCBinary(runtime.GOOS)
	if binary == "" {
		return nil, fmt.Errorf("vlc: бінарний файл не знайдено: %w", exec.ErrNotFound)
	}

	// RC TCP не має авторизації, тому слухаємо лише loopback. Перевірено
	// 2026-08-31: --rc-unix не працює в macOS-збірці VLC 3.0.17.3.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("VLC RC: резервування порту: %w", err)
	}
	address := listener.Addr().String()
	// Між звільненням порту та стартом VLC є мала прийнятна гонка.
	if err := listener.Close(); err != nil {
		return nil, fmt.Errorf("VLC RC: звільнення порту: %w", err)
	}

	base := p.Command(streamURL, mediaTitle, headers, startSec)
	args := append([]string{}, base.Args[1:len(base.Args)-1]...)
	args = append(args, "--extraintf", "rc", "--rc-host="+address, streamURL)
	cmd := exec.Command(binary, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("vlc: %w", err)
	}

	// VLC створює RC-сокет після старту; чекаємо до 10 с.
	var conn net.Conn
	var dialErr error
	for i := 0; i < 100; i++ {
		conn, dialErr = net.Dial("tcp", address)
		if dialErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dialErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("VLC RC: TCP не відповідає: %w", dialErr)
	}
	return newVLCSession(cmd, conn), nil
}
