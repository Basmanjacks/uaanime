package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Два записувачі не мають ділити одне ім'я tmp-файлу: інакше rename віддає
// суміш двох JSON. Тест має сенс під -race.
func TestSaveLibraryConcurrent(t *testing.T) {
	s := openTemp(t)
	lib, err := s.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	lib.EnsureTitle(provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Ім'я"}, NewID)

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// копія на горутину: паралелиться запис на диск, не домен
			own := &library.Library{Titles: lib.Titles}
			errs[i] = s.SaveLibrary(own)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("SaveLibrary[%d]: %v", i, err)
		}
	}

	data, err := os.ReadFile(s.libraryPath())
	if err != nil {
		t.Fatal(err)
	}
	var got library.Library
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("library.json після паралельного запису — не JSON: %v\n%s", err, data)
	}
	if len(got.Titles) != 1 {
		t.Fatalf("titles = %d, очікував 1", len(got.Titles))
	}
}

func TestSaveLibraryFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("права POSIX")
	}
	s := openTemp(t)
	lib, _ := s.LoadLibrary()
	if err := s.SaveLibrary(lib); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(s.libraryPath())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("library.json mode = %04o, очікував 0600", got)
	}
	entries, err := os.ReadDir(filepath.Dir(s.libraryPath()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("лишився tmp-файл: %s", e.Name())
		}
	}
}

// Наявна інсталяція створювалася з 0755/0644; Open має підтягнути права.
func TestOpenMigratesModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("права POSIX")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"state", "cache"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"library.json", "config.json", "library.json.bak", "state/health.json"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{".", "state", "cache"} {
		info, err := os.Stat(filepath.Join(dir, d))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("каталог %s mode = %04o, очікував 0700", d, got)
		}
	}
	for _, name := range files {
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("файл %s mode = %04o, очікував 0600", name, got)
		}
	}
}

func TestCacheKeyNeutralizesTraversal(t *testing.T) {
	key := cacheKey("anitube", "../x")
	if strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
		t.Fatalf("cacheKey = %q, очікував без роздільників і без «..»", key)
	}

	s := openTemp(t)
	path := s.episodesCachePath(provider.TitleRef{Provider: "anitube", Slug: "../../x"})
	cacheDir := filepath.Join(s.dir, "cache")
	if filepath.Dir(path) != cacheDir {
		t.Fatalf("episodesCachePath = %q, мав лишитися в %q", path, cacheDir)
	}
	// звичайний слаг імені файлу не міняє: старий кеш лишається читабельним
	plain := s.episodesCachePath(provider.TitleRef{Provider: "anitube", Slug: "4465-frren"})
	if filepath.Base(plain) != "episodes-anitube-4465-frren.json" {
		t.Errorf("episodesCachePath = %q", plain)
	}
}

func TestOpenSweepsStaleCache(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stale := s.episodesCachePath(provider.TitleRef{Provider: "anitube", Slug: "1-old"})
	fresh := s.episodesCachePath(provider.TitleRef{Provider: "anitube", Slug: "2-new"})
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-100 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("старий кеш мав зникнути: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("свіжий кеш мав лишитися: %v", err)
	}
}

func TestLoadConfigNormalizesPreferKind(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", "dub"},
		{"garbage", "dub"},
		{"multi", "dub"},
		{"dub", "dub"},
		{"voiceover", "voiceover"},
		{"sub", "sub"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			s := openTemp(t)
			if err := writeAtomic(s.configPath(), &Config{PreferKind: tt.in}); err != nil {
				t.Fatal(err)
			}
			cfg, err := s.LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.PreferKind != tt.want {
				t.Errorf("PreferKind = %q, очікував %q", cfg.PreferKind, tt.want)
			}
		})
	}
}

func TestImportRejectsOversized(t *testing.T) {
	s := openTemp(t)
	// валідний JSON, лише завеликий: помилка має бути про розмір, не про розбір
	pad := strings.Repeat("a", maxBackup)
	body := `{"library":{"titles":[]},"note":"` + pad + `"}`

	err := s.Import(strings.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), "завеликий") {
		t.Fatalf("Import = %v, очікував помилку про розмір", err)
	}
}

func TestImportRejectsDirtyBackupWithoutWriting(t *testing.T) {
	s := openTemp(t)
	before := []byte(`{"titles":[{"id":"keep","name":"Своє","sources":[{"provider":"anitube","slug":"1-x"}]}]}`)
	if err := os.WriteFile(s.libraryPath(), before, 0o600); err != nil {
		t.Fatal(err)
	}

	backups := map[string]string{
		"nil title": `{"library":{"titles":[null,{"id":"t1","name":"X","sources":[{"provider":"anitube","slug":"1-x"}]}]}}`,
		"bad slug":  `{"library":{"titles":[{"id":"t1","name":"X","sources":[{"provider":"anitube","slug":"../x"}]}]}}`,
	}
	for name, body := range backups {
		t.Run(name, func(t *testing.T) {
			err := s.Import(strings.NewReader(body))
			if err == nil || !strings.Contains(err.Error(), "невалідні записи") {
				t.Fatalf("Import = %v, очікував відмову", err)
			}
			after, err := os.ReadFile(s.libraryPath())
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("library.json змінився після відхиленого імпорту:\n%s", after)
			}
			if _, err := os.Stat(s.libraryPath() + ".bak"); !os.IsNotExist(err) {
				t.Fatalf(".bak створено при відхиленому імпорті: %v", err)
			}
		})
	}
}

func TestImportAcceptsValidBackup(t *testing.T) {
	s := openTemp(t)
	body := `{"library":{"titles":[{"id":"t1","name":"Фрірен","sources":[{"provider":"anitube","slug":"4465-frren"}]}],
	           "entries":[{"title_id":"t1","state":"watching"}]},
	          "config":{"prefer_kind":"garbage","player":"nonsense","autoplay":"ask"}}`

	if err := s.Import(strings.NewReader(body)); err != nil {
		t.Fatalf("Import: %v", err)
	}
	lib, err := s.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Titles) != 1 || lib.Titles[0].Name != "Фрірен" {
		t.Fatalf("бібліотека не імпортована: %+v", lib.Titles)
	}
	// конфіг із бекапа теж недовірений — має нормалізуватися ще при записі
	var onDisk Config
	if _, err := readJSON(s.configPath(), &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.PreferKind != "dub" || onDisk.Player != "vlc" || onDisk.Autoplay != "always" {
		t.Fatalf("конфіг записано без нормалізації: %+v", onDisk)
	}
}

func TestLoadEpisodesCleansOldCache(t *testing.T) {
	s := openTemp(t)
	ref := provider.TitleRef{Provider: "anitube", Slug: "1-x"}
	if err := writeAtomic(s.episodesCachePath(ref), &episodesCache{
		FetchedAt: time.Now(),
		Episodes: []provider.Episode{{Number: 1, Releases: []provider.Release{
			{Studio: "FanVox\x1b[2J", Kind: provider.Kind("\x1b")},
		}}},
	}); err != nil {
		t.Fatal(err)
	}

	eps, _, found := s.LoadEpisodes(ref)
	if !found || len(eps) != 1 || len(eps[0].Releases) != 1 {
		t.Fatalf("LoadEpisodes = (%+v, found=%v)", eps, found)
	}
	r := eps[0].Releases[0]
	if r.Studio != "FanVox" {
		t.Errorf("Studio = %q", r.Studio)
	}
	// невідомий тип — multi; вгадувати sub заборонено правилом продукту
	if r.Kind != provider.KindMulti {
		t.Errorf("Kind = %q, очікував multi", r.Kind)
	}
}

func TestLoadCatalogCleansOldCache(t *testing.T) {
	s := openTemp(t)
	if err := writeAtomic(s.catalogCachePath("anitube", provider.CatalogFresh), &catalogCache{
		FetchedAt: time.Now(),
		Year:      time.Now().Year(),
		Cards: []provider.TitleCard{
			{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "Фрірен\u009b2J"}},
			{TitleRef: provider.TitleRef{Provider: "anitube", Slug: "../x", Name: "Погана"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	cards, _, found := s.LoadCatalog("anitube", provider.CatalogFresh)
	if !found || len(cards) != 1 {
		t.Fatalf("LoadCatalog = (%+v, found=%v), очікував одну картку", cards, found)
	}
	if cards[0].Name != "Фрірен" {
		t.Errorf("Name = %q", cards[0].Name)
	}
}

func TestLoadLibraryNormalizesOnDisk(t *testing.T) {
	s := openTemp(t)
	const raw = `{"titles":[null,{"id":"t1","name":"Фрірен\u001b[2J","sources":[{"provider":"anitube","slug":"4465-frren","url":"https://evil.invalid/x"}]}],
	              "entries":[null,{"title_id":"t1","state":"watching"}]}`
	if err := os.WriteFile(s.libraryPath(), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	lib, err := s.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Titles) != 1 || lib.Titles[0] == nil {
		t.Fatalf("nil-тайтл пережив читання: %+v", lib.Titles)
	}
	if lib.Titles[0].Name != "Фрірен" {
		t.Errorf("Name = %q", lib.Titles[0].Name)
	}
	if lib.Titles[0].Sources[0].URL != "" {
		t.Errorf("URL = %q, збережений URL має обнулятися", lib.Titles[0].Sources[0].URL)
	}
	if len(lib.Entries) != 1 || lib.Entries[0] == nil {
		t.Fatalf("nil-запис пережив читання: %+v", lib.Entries)
	}
}

func TestLoadLibraryBacksUpBeforePruning(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"titles":[null,{"id":"t1","name":"X","sources":[{"provider":"anitube","slug":"1-x","name":"X"}]}],"entries":[],"progress":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "library.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Titles) != 1 {
		t.Fatalf("titles = %d, очікував 1", len(lib.Titles))
	}
	bak, err := os.ReadFile(filepath.Join(dir, "library.json.bak"))
	if err != nil {
		t.Fatalf("оригінал перед обрізанням має лягти в .bak: %v", err)
	}
	if string(bak) != string(raw) {
		t.Fatal(".bak не збігається з оригіналом")
	}
	// Чиста бібліотека .bak не переписує.
	clean := t.TempDir()
	if err := os.WriteFile(filepath.Join(clean, "library.json"), []byte(`{"titles":[],"entries":[],"progress":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ = Open(clean)
	if _, err := st.LoadLibrary(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(clean, "library.json.bak")); !os.IsNotExist(err) {
		t.Fatal("для чистої бібліотеки .bak створюватися не має")
	}
}
