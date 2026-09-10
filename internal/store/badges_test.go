package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestBadgeCursorIsOptionalCacheMetadata(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.LoadBadgeCursor() != "" {
		t.Fatal("missing cursor")
	}
	if err = s.SaveBadgeCursor("p/1-title"); err != nil {
		t.Fatal(err)
	}
	if s.LoadBadgeCursor() != "p/1-title" {
		t.Fatal("cursor did not persist")
	}
	var backup bytes.Buffer
	if err = s.Export(&backup); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(backup.String(), "cursor") {
		t.Fatal("metadata leaked into export")
	}
	if err = os.WriteFile(filepath.Join(s.dir, "cache", "badge-cursor.json"), []byte("bad JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if s.LoadBadgeCursor() != "" {
		t.Fatal("corrupt cursor must be absent")
	}
}
func TestImportCorruptBaselineRetainsUserRecords(t *testing.T) {
	for _, raw := range []string{`{}`, `{"groups":[null]}`, `42`, `{"groups":[{"studio":"A","kind":"dub","episodes":"0-9999999999999999999999999"}]}`, `{"groups":[{"studio":" A ","kind":"dub","episodes":"0-3"},{"studio":"A","kind":"dub","episodes":"2-4"}]}`} {
		t.Run(raw, func(t *testing.T) {
			s, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			data := `{"library":{"titles":[{"id":"t","name":"Title","sources":[{"provider":"p","slug":"1-title"}]}],"entries":[{"title_id":"t","release_baseline":` + raw + `}],"progress":[{"title_id":"t","episode":1,"completed":true}]}}`
			if err = s.Import(strings.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			lib, err := s.LoadLibrary()
			if err != nil {
				t.Fatal(err)
			}
			if len(lib.Progress) != 1 || !lib.Progress[0].Completed || len(lib.Entries) != 1 {
				t.Fatal("lost user data")
			}
			b := lib.Entries[0].ReleaseBaseline
			if strings.Contains(raw, "0-3") {
				if b == nil || len(b.Groups) != 1 || b.Groups[0].Episodes != "0-4" {
					t.Fatalf("bad normalized baseline %+v", b)
				}
			} else if b != nil {
				t.Fatal("corrupt baseline retained")
			}
		})
	}
}
func TestBaselineNilAndKnownEmptyStoreRoundTrip(t *testing.T) {
	for _, baseline := range []*library.ReleaseBaseline{nil, {Groups: []library.ReleaseGroup{}}} {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		lib := &library.Library{Titles: []*library.LocalTitle{{ID: "t"}}, Entries: []*library.Entry{{TitleID: "t", ReleaseBaseline: baseline}}}
		// JSON roundtrip is direct here: title identity validation is covered above.
		if err = s.SaveLibrary(lib); err != nil {
			t.Fatal(err)
		}
		var restored library.Library
		if _, err = readJSON(s.libraryPath(), &restored); err != nil {
			t.Fatal(err)
		}
		got := restored.Entries[0].ReleaseBaseline
		if (got == nil) != (baseline == nil) {
			t.Fatal("nil/empty collapsed")
		}
		b, err := json.Marshal(restored.Entries[0])
		if err != nil {
			t.Fatal(err)
		}
		if (baseline == nil) == strings.Contains(string(b), "release_baseline") {
			t.Fatal("omitempty wrong")
		}
	}
}

// largeBadgeFixture exercises alternating ranges that cannot compress into one
// contiguous interval, plus representative user progress and studio pins.
func largeBadgeFixture() *library.Library {
	lib := &library.Library{}
	for title := range 50 {
		id := fmt.Sprintf("fixture-title-%02d", title)
		lib.Titles = append(lib.Titles, &library.LocalTitle{ID: id, Name: "Fixture title", Sources: []provider.TitleRef{{Provider: "fixture", Slug: fmt.Sprintf("%d-title", title+1)}}})
		lib.Entries = append(lib.Entries, &library.Entry{TitleID: id, StudioPin: "Studio 0", KindPin: provider.KindDub})
		lib.Progress = append(lib.Progress, &library.Progress{TitleID: id, Episode: 3, PositionSec: 600, DurationSec: 1400, WatchedAt: time.Unix(1_700_000_000, 0).UTC()})
		var episodes []provider.Episode
		for number := range 1500 {
			ep := provider.Episode{Number: number}
			for group := range 6 {
				if number%2 == group%2 {
					ep.Releases = append(ep.Releases, provider.Release{Studio: fmt.Sprintf("Studio %d", group), Kind: provider.KindDub})
				}
			}
			episodes = append(episodes, ep)
		}
		lib.SeedReleaseBaseline(id, episodes)
	}
	return lib
}

func TestLargeReleaseBaselineFullExportFitsImportLimit(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveLibrary(largeBadgeFixture()); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	var exported bytes.Buffer
	if err = s.Export(&exported); err != nil {
		t.Fatal(err)
	}
	if exported.Len() >= maxBackup {
		t.Fatalf("full export %d bytes exceeds %d-byte import limit", exported.Len(), maxBackup)
	}
	t.Logf("full export, 50 titles × 1500 episode numbers × 6 alternating groups: %d bytes", exported.Len())
	restored, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = restored.Import(bytes.NewReader(exported.Bytes())); err != nil {
		t.Fatal(err)
	}
	lib, err := restored.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Titles) != 50 || len(lib.Entries) != 50 || len(lib.Progress) != 50 {
		t.Fatal("full export roundtrip lost user records")
	}
	for _, entry := range lib.Entries {
		if entry.ReleaseBaseline == nil || len(entry.ReleaseBaseline.Groups) != 6 {
			t.Fatalf("roundtrip lost groups for %s", entry.TitleID)
		}
	}
}

func BenchmarkSaveLargeReleaseLibrary(b *testing.B) {
	lib := largeBadgeFixture()
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := s.SaveLibrary(lib); err != nil {
			b.Fatal(err)
		}
	}
}
