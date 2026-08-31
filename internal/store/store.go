// Package store — диск: library.json, config.json, журнал прогресу.
// Усі записи атомарні (tmp + rename), щоб kill -9 не лишав битих файлів.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
)

// DataDir — каталог даних застосунку. UAANIME_DATA_DIR — службовий override
// для тестів і розробки, не користувацьке налаштування.
func DataDir() (string, error) {
	if d := os.Getenv("UAANIME_DATA_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("каталог конфігурації: %w", err)
	}
	return filepath.Join(base, "uaanime"), nil
}

// NewID — стабільний локальний ID (ADR-001): unix-мілісекунди + випадковий hex.
// Сортовність за часом — бонус, не гарантія.
func NewID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%013d-%s", time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

type Store struct {
	dir string
}

func Open(dir string) (*Store, error) {
	for _, sub := range []string{"state", "cache"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

// Health — результат останньої перевірки doctor (час останньої успішної
// відповіді кожного провайдера).
type Health struct {
	Providers map[string]time.Time `json:"providers"`
}

func (s *Store) LoadHealth() *Health {
	h := &Health{Providers: map[string]time.Time{}}
	_, _ = readJSON(filepath.Join(s.dir, "state", "health.json"), h)
	if h.Providers == nil {
		h.Providers = map[string]time.Time{}
	}
	return h
}

func (s *Store) SaveHealth(h *Health) error {
	return writeAtomic(filepath.Join(s.dir, "state", "health.json"), h)
}

func (s *Store) libraryPath() string { return filepath.Join(s.dir, "library.json") }
func (s *Store) configPath() string  { return filepath.Join(s.dir, "config.json") }
func (s *Store) journalPath() string { return filepath.Join(s.dir, "state", "current.json") }

// writeAtomic: tmp у тому самому каталозі + rename — атомарно на одній ФС.
func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) (found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("%s: битий JSON: %w", path, err)
	}
	return true, nil
}

func (s *Store) LoadLibrary() (*library.Library, error) {
	lib := &library.Library{}
	if _, err := readJSON(s.libraryPath(), lib); err != nil {
		return nil, err
	}
	return lib, nil
}

func (s *Store) SaveLibrary(lib *library.Library) error {
	return writeAtomic(s.libraryPath(), lib)
}

// Config — користувацькі налаштування. Ціль брифу: ≤ 8 налаштувань.
type Config struct {
	FavoriteStudio string   `json:"favorite_studio,omitempty"`
	PreferKind     string   `json:"prefer_kind,omitempty"` // dub | voiceover | sub
	Autoplay       string   `json:"autoplay,omitempty"`    // ask | always | never
	Providers      []string `json:"providers,omitempty"`
}

func (s *Store) LoadConfig() (*Config, error) {
	cfg := &Config{}
	if _, err := readJSON(s.configPath(), cfg); err != nil {
		return nil, err
	}
	if cfg.PreferKind == "" {
		cfg.PreferKind = "dub"
	}
	if cfg.Autoplay == "" {
		cfg.Autoplay = "ask"
	}
	return cfg, nil
}

// Journal — крихітний файл-журнал поточного перегляду (~200 байт).
// Переписується атомарно кожні кілька секунд; kill -9 коштує ≤ 10 с прогресу.
type Journal struct {
	TitleID     string    `json:"title_id"`
	Episode     int       `json:"episode"`
	PositionSec float64   `json:"position_sec"`
	DurationSec float64   `json:"duration_sec"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Store) WriteJournal(j *Journal) error {
	return writeAtomic(s.journalPath(), j)
}

func (s *Store) removeJournal() error {
	err := os.Remove(s.journalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// RecoverJournal зливає вцілілий журнал у бібліотеку (виклик на старті та
// після завершення перегляду) і видаляє його. Повертає true, якщо було що зливати.
func (s *Store) RecoverJournal(lib *library.Library) (bool, error) {
	var j Journal
	found, err := readJSON(s.journalPath(), &j)
	if err != nil {
		// битий журнал — втрачаємо ≤10 с прогресу, не роботу застосунку
		return false, s.removeJournal()
	}
	if !found {
		return false, nil
	}
	lib.RecordPosition(j.TitleID, j.Episode, j.PositionSec, j.DurationSec, j.UpdatedAt)
	if err := s.SaveLibrary(lib); err != nil {
		return false, err
	}
	return true, s.removeJournal()
}
