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
