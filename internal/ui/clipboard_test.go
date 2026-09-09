package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Start a fresh process so the clipboard package discovers only our fixture
// tools at init time. Never read or overwrite the user's actual clipboard.
func TestSearchAsyncClipboard(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("release platforms only")
	}
	if os.Getenv("UAANIME_CLIPBOARD_HELPER") == "1" {
		m := openTestSearch(t, newTestModel(t))
		m, cmd := updateTestModel(t, m, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
		if cmd == nil {
			t.Fatal("input clipboard command was discarded")
		}
		m, _ = updateTestModel(t, m, cmd())
		if got := m.input.Value(); got != "clipboard fixture" {
			t.Fatalf("async paste lost: %q", got)
		}
		return
	}
	dir := t.TempDir()
	for _, name := range []string{"pbpaste", "xclip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf 'clipboard fixture'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSearchAsyncClipboard$", "-test.timeout=15s")
	cmd.Env = append(os.Environ(), "PATH="+dir, "WAYLAND_DISPLAY=", "UAANIME_CLIPBOARD_HELPER=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clipboard helper: %v\n%s", err, out)
	}
}
