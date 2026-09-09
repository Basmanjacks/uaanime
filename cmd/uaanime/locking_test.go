package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func TestReadCommandsDoNotConsumeJournal(t *testing.T) {
	dir := cliEnv(t)
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{}
	title := lib.EnsureTitle(provider.TitleRef{Provider: "anitube", Slug: "4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon"}, store.NewID)
	if err := s.SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJournal(&store.Journal{TitleID: title.ID, Episode: 1, PositionSec: 42, DurationSec: 1440, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	lock, err := store.LockWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	paths := []string{filepath.Join(dir, "library.json"), filepath.Join(dir, "state", "current.json")}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"doctor", "--json"}, {"export"}, {"search", "фрірен", "--json"}, {"episodes", fixtureTitleID, "--json"}, {"resolve", fixtureTitleID, "1", "--json"}, {"play", fixtureTitleID, "1", "--dry-run"}} {
		code, _, errOut := runCLI(t, args...)
		if code != 0 {
			t.Fatalf("%v: code %d: %s", args, code, errOut)
		}
		for i, path := range paths {
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before[i], after) {
				t.Fatalf("%v changed %s: %v", args, path, err)
			}
		}
	}
	code, _, _ := runCLI(t, "play", fixtureTitleID, "1")
	if code != 1 {
		t.Fatalf("second writer exit: %d", code)
	}
}

func TestDebugFlagControlsStartupDiagnostics(t *testing.T) {
	dir := cliEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "library.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, normal := runCLI(t, "export")
	if code != 1 || strings.Contains(normal, "library.json") {
		t.Fatalf("normal leaked diagnostic: %d %s", code, normal)
	}
	code, _, debug := runCLI(t, "export", "--debug")
	if code != 1 || !strings.Contains(debug, "library.json") {
		t.Fatalf("debug missing diagnostic: %d %s", code, debug)
	}
}
