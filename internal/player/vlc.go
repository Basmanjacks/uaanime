// Package player — запуск зовнішнього VLC.
package player

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// VLC — бекенд зовнішнього плеєра VLC.
type VLC struct{}

func (VLC) ID() string { return "vlc" }

// vlcArgs — аргументи VLC без бінарника і без URL (URL іде останнім).
func vlcArgs(mediaTitle string, headers map[string]string, startSec float64) []string {
	args := []string{
		"--play-and-exit",
		"--fullscreen",
		"--quiet",
		"--meta-title=" + mediaTitle,
	}
	// VLC CLI відкриває лише ці два HTTP-заголовки (ashdi віддає саме їх); решту,
	// зокрема Accept-Language, він відкидає — але Accept-Language надсилає сам,
	// чого moonanime і вимагає (перевірено VLC 3.0.17.3, 2026-09-02).
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
	return args
}

// Command збирає детерміновану команду VLC без пошуку бінарного файла.
func (VLC) Command(streamURL, mediaTitle string, headers map[string]string, startSec float64) *exec.Cmd {
	return exec.Command("vlc", append(vlcArgs(mediaTitle, headers, startSec), streamURL)...)
}

func (VLC) Start(ctx context.Context, streamURL, mediaTitle string, headers map[string]string, startSec float64) (Session, error) {
	binary := findVLCBinary(runtime.GOOS)
	if binary == "" {
		return nil, fmt.Errorf("vlc: бінарний файл не знайдено: %w: %w", exec.ErrNotFound, errs.ErrPlayer)
	}

	// RC TCP не має авторизації, тому слухаємо лише loopback. Перевірено
	// 2026-08-31: --rc-unix не працює в macOS-збірці VLC 3.0.17.3.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("VLC RC: резервування порту: %w: %w", err, errs.ErrPlayer)
	}
	address := listener.Addr().String()
	// Між звільненням порту та стартом VLC є мала прийнятна гонка.
	if err := listener.Close(); err != nil {
		return nil, fmt.Errorf("VLC RC: звільнення порту: %w: %w", err, errs.ErrPlayer)
	}

	args := append(vlcArgs(mediaTitle, headers, startSec), "--extraintf", "rc", "--rc-host="+address, streamURL)
	cmd := exec.Command(binary, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("vlc: %w: %w", err, errs.ErrPlayer)
	}

	// Сесія (а з нею і жнець процесу) існує до першої спроби з'єднання:
	// інакше невдалий dial не мав би кому віддати вбитий процес.
	s := newVLCSession(cmd)
	conn, err := dialRetry(ctx, s.done, "tcp", address)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("VLC RC: TCP не відповідає: %w", err)
	}
	s.attachRC(conn)
	return s, nil
}
