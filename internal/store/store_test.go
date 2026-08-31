package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLibraryRoundtrip(t *testing.T) {
	s := openTemp(t)
	lib, err := s.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	title := lib.EnsureTitle(provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Ім'я"}, NewID)
	lib.RecordPosition(title.ID, 1, 60, 1440, time.Now())
	if err := s.SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleByRef(provider.TitleRef{Provider: "anitube", Slug: "1-x"}) == nil {
		t.Fatal("тайтл не пережив збереження")
	}
	if got.ProgressFor(title.ID, 1) == nil {
		t.Fatal("прогрес не пережив збереження")
	}
}

func TestJournalRecovery(t *testing.T) {
	s := openTemp(t)
	lib, _ := s.LoadLibrary()

	// симуляція kill -9: журнал є, чистого завершення не було
	err := s.WriteJournal(&Journal{
		TitleID: "t1", Episode: 3, PositionSec: 872, DurationSec: 1440, UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := s.RecoverJournal(lib)
	if err != nil || !merged {
		t.Fatalf("RecoverJournal = (%v, %v)", merged, err)
	}
	if p := lib.ProgressFor("t1", 3); p == nil || p.PositionSec != 872 {
		t.Fatalf("журнал не злився: %+v", p)
	}
	// журнал видалено, повторне злиття — no-op
	if merged, _ := s.RecoverJournal(lib); merged {
		t.Fatal("журнал мав зникнути після злиття")
	}
	// бібліотека на диску вже містить результат злиття
	got, _ := s.LoadLibrary()
	if got.ProgressFor("t1", 3) == nil {
		t.Fatal("злиття не збережено на диск")
	}
}

func TestCorruptJournalDoesNotKill(t *testing.T) {
	s := openTemp(t)
	lib, _ := s.LoadLibrary()
	if err := os.WriteFile(s.journalPath(), []byte("{битий"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := s.RecoverJournal(lib)
	if err != nil || merged {
		t.Fatalf("битий журнал має тихо зникнути: (%v, %v)", merged, err)
	}
}

func TestAtomicWriteLeavesNoTmp(t *testing.T) {
	s := openTemp(t)
	lib, _ := s.LoadLibrary()
	if err := s.SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(s.libraryPath()))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("лишився tmp-файл: %s", e.Name())
		}
	}
}

func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := NewID()
		if seen[id] {
			t.Fatal("колізія ID")
		}
		seen[id] = true
	}
}
