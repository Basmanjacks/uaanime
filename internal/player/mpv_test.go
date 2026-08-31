package player

import (
	"strings"
	"testing"
)

func TestMPVCommandDeterministic(t *testing.T) {
	h := map[string]string{"User-Agent": "ua", "Referer": "https://x/"}
	a := MPVCommand("https://x/i.m3u8", "Тайтл · 1", h, 0)
	b := MPVCommand("https://x/i.m3u8", "Тайтл · 1", h, 0)
	if strings.Join(a.Args, " ") != strings.Join(b.Args, " ") {
		t.Error("команда недетермінована")
	}
	joined := strings.Join(a.Args, " ")
	if !strings.Contains(joined, "Referer: https://x/,User-Agent: ua") {
		t.Errorf("заголовки не відсортовані або втрачені: %s", joined)
	}
	if strings.Contains(joined, "--start=") {
		t.Errorf("start=0 не має додавати --start: %s", joined)
	}
	c := MPVCommand("u", "t", nil, 93.5)
	if !strings.Contains(strings.Join(c.Args, " "), "--start=93.5") {
		t.Errorf("--start відсутній: %v", c.Args)
	}
}
