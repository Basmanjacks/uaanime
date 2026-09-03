package player

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Player будує команду та запускає підтримуваний зовнішній відеоплеєр.
type Player interface {
	ID() string
	Command(streamURL, mediaTitle string, headers map[string]string, startSec float64) *exec.Cmd
	// Start приймає ctx, бо піднімає керуючий сокет: очікування має
	// скасовуватися разом із відтворенням.
	Start(ctx context.Context, streamURL, mediaTitle string, headers map[string]string, startSec float64) (Session, error)
}

// Session дає доступ до стану запущеного відеоплеєра.
type Session interface {
	TimePos() (float64, error)
	Duration() (float64, error)
	// TogglePause перемикає паузу: у RC VLC є лише перемикач, тож обидва бекенди
	// поводяться однаково.
	TogglePause() error
	Paused() (bool, error)
	// Seek зсуває позицію відносно поточної; від'ємне — назад.
	Seek(deltaSec float64) error
	// SeekTo ставить абсолютну позицію: тап по смузі пульта знає лише
	// координату, а не зсув, тож перерахунок через Seek дав би зайвий
	// round-trip і гонку з відтворенням.
	SeekTo(posSec float64) error
	End() <-chan EndReason
	Wait() error
	Close()
}

var vlcDarwinBundlePaths = []string{
	"/Applications/VLC.app/Contents/MacOS/VLC",
	filepath.Join(os.Getenv("HOME"), "Applications", "VLC.app", "Contents", "MacOS", "VLC"),
}

// ByID повертає бекенд за ідентифікатором із конфігу, НЕ перевіряючи, чи він
// встановлений: --dry-run має друкувати команду й на машині без плеєрів.
// Нормалізація конфігу вже звузила значення до vlc|mpv, тому все, що не "mpv",
// — це vlc (плеєр за замовчуванням).
func ByID(id string) Player {
	if id == "mpv" {
		return MPV{}
	}
	return VLC{}
}

// Detect вибирає доступний бекенд. Відсутність плеєрів є штатним результатом:
// повідомлення для користувача формуватиме вищий шар.
func Detect(preferred string) (p Player, fallback bool, err error) {
	mpvFound := findMPVBinary() != ""
	vlcFound := findVLCBinary(runtime.GOOS) != ""

	if preferred != "mpv" && preferred != "" && preferred != "vlc" {
		preferred = "vlc"
	}
	switch preferred {
	case "vlc":
		if vlcFound {
			return ByID("vlc"), false, nil
		}
		if mpvFound {
			return ByID("mpv"), true, nil
		}
		return nil, false, nil
	case "mpv", "":
		if mpvFound {
			return ByID("mpv"), false, nil
		}
		if vlcFound {
			return ByID("vlc"), true, nil
		}
		return nil, false, nil
	}
	return nil, false, nil
}

// Found перевіряє доступність бекенда тим самим пошуком, що й Detect.
func Found(id string) bool {
	switch id {
	case "mpv":
		return findMPVBinary() != ""
	case "vlc":
		return findVLCBinary(runtime.GOOS) != ""
	default:
		return false
	}
}

func findMPVBinary() string {
	path, err := exec.LookPath("mpv")
	if err != nil {
		return ""
	}
	return path
}

func findVLCBinary(goos string) string {
	if path, err := exec.LookPath("vlc"); err == nil {
		return path
	}
	if goos != "darwin" {
		return ""
	}
	for _, path := range vlcDarwinBundlePaths {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return path
		}
	}
	return ""
}
