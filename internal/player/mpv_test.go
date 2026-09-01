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
	want := []string{"mpv", "--no-terminal", "--force-media-title=t", "--start=93.5", "u"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("Command.Args = %#v, очікував %#v", cmd.Args, want)
	}
}
