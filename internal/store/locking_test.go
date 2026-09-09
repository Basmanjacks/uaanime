//go:build darwin || linux

package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestWriterLock(t *testing.T) {
	dir := t.TempDir()
	first, err := LockWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	if second, err := LockWriter(dir); !errors.Is(err, errs.ErrStoreBusy) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second writer: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := LockWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = third.Close() }()
}

func TestWriterLockProcess(t *testing.T) {
	if dir := os.Getenv("UAANIME_LOCK_HELPER"); dir != "" {
		lock, err := LockWriter(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lock.Close() }()
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadByte()
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriterLockProcess$")
	cmd.Env = append(os.Environ(), "UAANIME_LOCK_HELPER="+dir)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if line, err := bufio.NewReader(out).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("helper: %q %v", line, err)
	}
	if lock, err := LockWriter(dir); !errors.Is(err, errs.ErrStoreBusy) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Fatalf("concurrent process: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	lock, err := LockWriter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
}

func TestReadOnlyExportIncludesJournalWithoutWriting(t *testing.T) {
	s := openTemp(t)
	lib := &library.Library{}
	title := lib.EnsureTitle(provider.TitleRef{Provider: "stub", Slug: "1-one"}, NewID)
	if err := s.SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJournal(&Journal{TitleID: title.ID, Episode: 1, PositionSec: 42, DurationSec: 100, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(s.libraryPath())
	journal, _ := os.ReadFile(s.journalPath())
	ro := OpenReadOnly(s.dir)
	var b bytes.Buffer
	if err := ro.Export(&b); err != nil {
		t.Fatal(err)
	}
	var backup Backup
	if err := json.Unmarshal(b.Bytes(), &backup); err != nil {
		t.Fatal(err)
	}
	p := backup.Library.ProgressFor(title.ID, 1)
	if p == nil || p.PositionSec != 42 {
		t.Fatalf("export progress: %+v", p)
	}
	if err := openTemp(t).Import(bytes.NewReader(b.Bytes())); err != nil {
		t.Fatalf("export is not importable: %v", err)
	}
	after, _ := os.ReadFile(s.libraryPath())
	afterJournal, _ := os.ReadFile(s.journalPath())
	if !bytes.Equal(before, after) || !bytes.Equal(journal, afterJournal) {
		t.Fatal("read-only export changed state")
	}
}

func TestReadOnlyLoadDoesNotCreateBackup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "library.json"), []byte(`{"titles":[null]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenReadOnly(dir).LoadLibrary(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("read-only created files: %v", entries)
	}
}

func TestExportNewerJournalCannotCreateOrphans(t *testing.T) {
	s := openTemp(t)
	// The journal may come from a later snapshot than the library, or from
	// a title removed by import. Never emit a backup that import must reject.
	if err := s.SaveLibrary(&library.Library{}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteJournal(&Journal{TitleID: "later-title", Episode: 1, PositionSec: 42, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := s.Export(&b); err != nil {
		t.Fatal(err)
	}
	destination := openTemp(t)
	if err := destination.Import(bytes.NewReader(b.Bytes())); err != nil {
		t.Fatalf("export produced invalid backup: %v", err)
	}
	if _, err := os.Stat(s.journalPath()); err != nil {
		t.Fatalf("export consumed journal: %v", err)
	}
}
