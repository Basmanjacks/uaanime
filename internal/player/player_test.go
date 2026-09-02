package player

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectBackends(t *testing.T) {
	old := vlcDarwinBundlePaths
	vlcDarwinBundlePaths = nil
	t.Cleanup(func() { vlcDarwinBundlePaths = old })

	tests := []struct {
		name         string
		preferred    string
		mpv          bool
		vlc          bool
		wantID       string
		wantFallback bool
	}{
		{name: "mpv бажаний і доступний", preferred: "mpv", mpv: true, wantID: "mpv"},
		{name: "vlc бажаний і доступний", preferred: "vlc", vlc: true, wantID: "vlc"},
		{name: "fallback з mpv на vlc", preferred: "mpv", vlc: true, wantID: "vlc", wantFallback: true},
		{name: "fallback з vlc на mpv", preferred: "vlc", mpv: true, wantID: "mpv", wantFallback: true},
		{name: "vlc перемагає за перевагою", preferred: "vlc", mpv: true, vlc: true, wantID: "vlc"},
		{name: "порожня перевага означає mpv", mpv: true, vlc: true, wantID: "mpv"},
		{name: "невідома перевага означає vlc", preferred: "інше", mpv: true, vlc: true, wantID: "vlc"},
		{name: "без плеєрів", preferred: "mpv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.mpv {
				writeExecutable(t, filepath.Join(dir, "mpv"))
			}
			if tt.vlc {
				writeExecutable(t, filepath.Join(dir, "vlc"))
			}
			t.Setenv("PATH", dir)

			p, fallback, err := Detect(tt.preferred)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if tt.wantID == "" {
				if p != nil || fallback {
					t.Fatalf("Detect = (%v, %v), очікував (nil, false)", p, fallback)
				}
				return
			}
			if p == nil || p.ID() != tt.wantID || fallback != tt.wantFallback {
				t.Fatalf("Detect = (%v, %v), очікував (%s, %v)", p, fallback, tt.wantID, tt.wantFallback)
			}
		})
	}
}

func TestFoundUsesBackendDiscovery(t *testing.T) {
	old := vlcDarwinBundlePaths
	vlcDarwinBundlePaths = nil
	t.Cleanup(func() { vlcDarwinBundlePaths = old })

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "mpv"))
	t.Setenv("PATH", dir)

	if !Found("mpv") {
		t.Fatal("Found(mpv) = false, очікував true")
	}
	if Found("vlc") {
		t.Fatal("Found(vlc) = true, очікував false")
	}
	if Found("невідомий") {
		t.Fatal("Found(невідомий) = true, очікував false")
	}
}

func TestFindVLCBinaryInPath(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "vlc")
	writeExecutable(t, want)
	t.Setenv("PATH", dir)

	if got := findVLCBinary("linux"); got != want {
		t.Fatalf("findVLCBinary = %q, очікував %q", got, want)
	}
}

func TestFindVLCBinaryInDarwinBundle(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "VLC.app", "Contents", "MacOS", "VLC")
	writeExecutable(t, want)
	t.Setenv("PATH", t.TempDir())

	old := vlcDarwinBundlePaths
	vlcDarwinBundlePaths = []string{want}
	t.Cleanup(func() { vlcDarwinBundlePaths = old })

	if got := findVLCBinary("darwin"); got != want {
		t.Fatalf("findVLCBinary = %q, очікував %q", got, want)
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestByID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id   string
		want string
	}{
		{id: "mpv", want: "mpv"},
		{id: "vlc", want: "vlc"},
		{id: "", want: "vlc"},
		{id: "smplayer", want: "vlc"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := ByID(tt.id).ID(); got != tt.want {
				t.Fatalf("ByID(%q).ID() = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}
