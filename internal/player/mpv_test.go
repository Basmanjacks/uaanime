package player

import (
	"reflect"
	"testing"
)

func TestMPVBuildsCommandWithoutStart(t *testing.T) {
	cmd := (MPV{}).Command(
		"https://x/i.m3u8",
		"Тайтл · 1",
		map[string]string{"User-Agent": "ua", "Referer": "https://x/"},
		0,
	)
	want := []string{
		"mpv",
		"--no-terminal",
		"--fs",
		"--force-media-title=Тайтл · 1",
		"--http-header-fields=Referer: https://x/,User-Agent: ua",
		"https://x/i.m3u8",
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}

func TestMPVBuildsCommandWithStart(t *testing.T) {
	cmd := (MPV{}).Command("u", "t", nil, 93.5)
	want := []string{"mpv", "--no-terminal", "--fs", "--force-media-title=t", "--start=93.5", "u"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}

func TestMPVPassesAllHeadersIncludingAcceptLanguage(t *testing.T) {
	cmd := (MPV{}).Command(
		"https://x/i.m3u8",
		"t",
		map[string]string{"User-Agent": "ua", "Referer": "https://x/", "Accept-Language": "uk-UA"},
		0,
	)
	want := "--http-header-fields=Accept-Language: uk-UA,Referer: https://x/,User-Agent: ua"
	if cmd.Args[4] != want {
		t.Fatalf("Args[4] = %q, очікував %q", cmd.Args[4], want)
	}
}

// Локальний файл: шлях іде останнім аргументом, а заголовків немає взагалі —
// --http-header-fields для файла з диска не має сенсу (і зламав би шлях із
// комою всередині).
func TestMPVLocalFileHasNoHeaderArgs(t *testing.T) {
	const path = "/Users/me/Movies/uaanime/Фрірен/Фрірен - 05 - FanVoxUA [Озв, 1080p].ts"
	for name, headers := range map[string]map[string]string{
		"nil":   nil,
		"empty": {},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := (MPV{}).Command(path, "Фрірен · 5", headers, 0)
			want := []string{"mpv", "--no-terminal", "--fs", "--force-media-title=Фрірен · 5", path}
			if !reflect.DeepEqual(cmd.Args, want) {
				t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
			}
		})
	}
}

// Resume зі збереженого файла: --start лишається, шлях — усе одно останній.
func TestMPVLocalFileWithStart(t *testing.T) {
	const path = "/tmp/uaanime/Тайтл/Тайтл - 01 - Студія [Озв, 720p].ts"
	cmd := (MPV{}).Command(path, "Тайтл · 1", nil, 42)
	want := []string{"mpv", "--no-terminal", "--fs", "--force-media-title=Тайтл · 1", "--start=42.0", path}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}
