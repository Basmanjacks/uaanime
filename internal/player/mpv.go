// Package player — запуск зовнішнього mpv.
package player

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// MPV — бекенд зовнішнього плеєра mpv.
type MPV struct{}

func (MPV) ID() string { return "mpv" }

// mpvArgs — аргументи mpv без бінарника і без URL (mpv очікує URL останнім).
// startSec > 0 додає --start (resume). Заголовки сортуються, щоб команда була
// детермінованою (важливо для --dry-run і тестів).
func mpvArgs(mediaTitle string, headers map[string]string, startSec float64) []string {
	args := []string{
		"--no-terminal",
		"--fs",
		"--force-media-title=" + mediaTitle,
	}
	if len(headers) > 0 {
		keys := make([]string, 0, len(headers))
		for k := range headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make([]string, 0, len(keys))
		for _, k := range keys {
			fields = append(fields, k+": "+headers[k])
		}
		args = append(args, "--http-header-fields="+strings.Join(fields, ","))
	}
	if startSec > 0 {
		args = append(args, fmt.Sprintf("--start=%.1f", startSec))
	}
	return args
}

// Command збирає команду mpv для потоку з обов'язковими заголовками.
func (MPV) Command(streamURL, mediaTitle string, headers map[string]string, startSec float64) *exec.Cmd {
	return exec.Command("mpv", append(mpvArgs(mediaTitle, headers, startSec), streamURL)...)
}

func (MPV) Start(ctx context.Context, streamURL, mediaTitle string, headers map[string]string, startSec float64) (Session, error) {
	return startMPV(ctx, streamURL, mediaTitle, headers, startSec)
}
