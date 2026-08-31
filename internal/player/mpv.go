// Package player — запуск зовнішнього mpv.
package player

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// MPVCommand збирає команду mpv для потоку з обов'язковими заголовками.
// startSec > 0 додає --start (resume, Phase 2). Заголовки сортуються,
// щоб команда була детермінованою (важливо для --dry-run і тестів).
func MPVCommand(streamURL, mediaTitle string, headers map[string]string, startSec float64) *exec.Cmd {
	args := []string{
		"--no-terminal",
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
	args = append(args, streamURL)
	return exec.Command("mpv", args...)
}
