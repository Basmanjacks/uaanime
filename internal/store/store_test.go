package store

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestLoadConfigNormalizesPlayerAndAutoplay(t *testing.T) {
	tests := []struct {
		name         string
		config       *Config
		wantPlayer   string
		wantAutoplay string
	}{
		{name: "empty config", wantPlayer: "vlc", wantAutoplay: "always"},
		{name: "unknown player", config: &Config{Player: "unknown", Autoplay: "always"}, wantPlayer: "vlc", wantAutoplay: "always"},
		{name: "legacy ask", config: &Config{Player: "mpv", Autoplay: "ask"}, wantPlayer: "mpv", wantAutoplay: "always"},
		{name: "empty autoplay", config: &Config{Player: "vlc"}, wantPlayer: "vlc", wantAutoplay: "always"},
		{name: "never preserved", config: &Config{Player: "mpv", Autoplay: "never"}, wantPlayer: "mpv", wantAutoplay: "never"},
		{name: "unknown autoplay", config: &Config{Player: "vlc", Autoplay: "sometimes"}, wantPlayer: "vlc", wantAutoplay: "always"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTemp(t)
			if tt.config != nil {
				if err := writeAtomic(s.configPath(), tt.config); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}

			got, err := s.LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got.Player != tt.wantPlayer {
				t.Errorf("Player = %q, очікував %q", got.Player, tt.wantPlayer)
			}
			if got.Autoplay != tt.wantAutoplay {
				t.Errorf("Autoplay = %q, очікував %q", got.Autoplay, tt.wantAutoplay)
			}
		})
	}
}

func TestJournalRecovery(t *testing.T) {
	s := openTemp(t)
	lib, _ := s.LoadLibrary()
	// тайтл має існувати: прогрес без свого тайтлу LoadLibrary викидає як сироту
	title := lib.EnsureTitle(provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Ім'я"}, NewID)

	// симуляція kill -9: журнал є, чистого завершення не було
	err := s.WriteJournal(&Journal{
		TitleID: title.ID, Episode: 3, PositionSec: 872, DurationSec: 1440, UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	merged, err := s.RecoverJournal(lib)
	if err != nil || !merged {
		t.Fatalf("RecoverJournal = (%v, %v)", merged, err)
	}
	if p := lib.ProgressFor(title.ID, 3); p == nil || p.PositionSec != 872 {
		t.Fatalf("журнал не злився: %+v", p)
	}
	// журнал видалено, повторне злиття — no-op
	if merged, _ := s.RecoverJournal(lib); merged {
		t.Fatal("журнал мав зникнути після злиття")
	}
	// бібліотека на диску вже містить результат злиття
	got, _ := s.LoadLibrary()
	if got.ProgressFor(title.ID, 3) == nil {
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

func TestCatalogCacheRoundTrip(t *testing.T) {
	s := openTemp(t)
	cards := []provider.TitleCard{
		{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Ім'я"}, Year: 2026},
		{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "2-y", Name: "Інше"}, Rating: 8.5},
	}
	if err := s.SaveCatalog("anitube", provider.CatalogTopSeason, cards); err != nil {
		t.Fatal(err)
	}

	got, fresh, found := s.LoadCatalog("anitube", provider.CatalogTopSeason)
	if !found || !fresh {
		t.Fatalf("LoadCatalog = (fresh=%v, found=%v), очікував свіжий кеш", fresh, found)
	}
	if !reflect.DeepEqual(got, cards) {
		t.Fatalf("картки не пережили збереження: %#v", got)
	}
}

func TestCatalogCacheMissesOtherKind(t *testing.T) {
	s := openTemp(t)
	if err := s.SaveCatalog("anitube", provider.CatalogTopSeason, []provider.TitleCard{{}}); err != nil {
		t.Fatal(err)
	}
	if _, _, found := s.LoadCatalog("anitube", provider.CatalogFresh); found {
		t.Fatal("блоки каталогу не мають ділити кеш")
	}
}

func TestCatalogCacheStaleAfterTTL(t *testing.T) {
	s := openTemp(t)
	// назва обов'язкова: картку без ідентичності читання кешу відкидає
	cards := []provider.TitleCard{{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Ім'я"}}}
	writeCatalogCache(t, s, provider.CatalogFresh, catalogCache{
		FetchedAt: time.Now().Add(-catalogTTL - time.Minute),
		Year:      time.Now().Year(),
		Cards:     cards,
	})

	got, fresh, found := s.LoadCatalog("anitube", provider.CatalogFresh)
	if !found || fresh {
		t.Fatalf("LoadCatalog = (fresh=%v, found=%v), очікував знайдений але несвіжий", fresh, found)
	}
	if !reflect.DeepEqual(got, cards) {
		t.Fatalf("несвіжий кеш має віддавати картки: %#v", got)
	}
}

// Торішній «топ сезону» — не несвіжий, а неправильний: офлайн-fallback віддав би
// його як поточний. Такий запис має читатися як відсутній.
func TestCatalogCacheOldYear(t *testing.T) {
	s := openTemp(t)
	writeCatalogCache(t, s, provider.CatalogTopSeason, catalogCache{
		FetchedAt: time.Now(),
		Year:      time.Now().Year() - 1,
		Cards:     []provider.TitleCard{{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "1-x"}}},
	})

	if _, _, found := s.LoadCatalog("anitube", provider.CatalogTopSeason); found {
		t.Fatal("торішній топ сезону мав читатися як відсутній")
	}
	// правило стосується лише топу сезону: «свіже» роком не обмежене
	writeCatalogCache(t, s, provider.CatalogFresh, catalogCache{
		FetchedAt: time.Now(),
		Year:      time.Now().Year() - 1,
		Cards:     []provider.TitleCard{{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "1-x"}}},
	})
	if _, _, found := s.LoadCatalog("anitube", provider.CatalogFresh); !found {
		t.Fatal("торішній блок «свіже» мав лишитися придатним для офлайн-fallback")
	}
}

func writeCatalogCache(t *testing.T, s *Store, kind provider.CatalogKind, c catalogCache) {
	t.Helper()
	if err := writeAtomic(s.catalogCachePath("anitube", kind), &c); err != nil {
		t.Fatal(err)
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
