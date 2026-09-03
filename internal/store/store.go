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
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
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
		// Каталог даних — приватний: у library.json видно, що людина дивиться.
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o700); err != nil {
			return nil, err
		}
	}
	migrateModes(dir)
	sweepCache(dir)
	return &Store{dir: dir}, nil
}

// migrateModes доводить права наявної інсталяції до 0700/0600: MkdirAll не
// чіпає режим уже створеного каталогу, а writeAtomic не торкається файлів,
// які цього запуску не переписувалися. Помилки ігноруються — чужий власник
// або read-only ФС не привід не запускатися.
func migrateModes(dir string) {
	for _, d := range []string{dir, filepath.Join(dir, "state"), filepath.Join(dir, "cache")} {
		_ = os.Chmod(d, 0o700)
	}
	for _, name := range []string{"library.json", "config.json", "library.json.bak"} {
		_ = os.Chmod(filepath.Join(dir, name), 0o600)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "state"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			_ = os.Chmod(filepath.Join(dir, "state", e.Name()), 0o600)
		}
	}
}

// cacheMaxAge — після цього віку запис кешу вже нікому не потрібен навіть як
// офлайн-fallback. Без прибирання каталог ріс би вічно: файл на кожен тайтл,
// який колись відкривали.
const cacheMaxAge = 90 * 24 * time.Hour

func sweepCache(dir string) {
	cacheDir := filepath.Join(dir, "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-cacheMaxAge)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(cacheDir, e.Name()))
	}
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
func (s *Store) remotePath() string  { return filepath.Join(s.dir, "state", "remote.json") }

// writeAtomic: tmp у тому самому каталозі + rename — атомарно на одній ФС.
// Ім'я tmp унікальне (CreateTemp), інакше два одночасні записувачі писали б
// в один файл і rename віддав би суміш. CreateTemp одразу створює 0600 —
// саме той режим, який нам потрібен, тому Chmod не потрібен.
func writeAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("%s: тимчасовий файл: %w", path, err)
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: запис: %w", path, err)
	}
	// Sync до rename: інакше після зникнення живлення rename міг би показати
	// існуючий, але порожній файл.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: sync: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: закриття: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: rename: %w", path, err)
	}
	return nil
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
	// Best-effort: library.json старших версій ніс сирі рядки зі сторінки, а
	// ручне редагування лишає `null` у масивах. Читати далі важливіше за звіт,
	// але викинуте не має зникати мовчки: наступний SaveLibrary закріпив би
	// втрату, тому оригінал перед першим записом лягає в .bak.
	if lib.Normalize(provider.CleanText) > 0 {
		if raw, err := os.ReadFile(s.libraryPath()); err == nil {
			_ = os.WriteFile(s.libraryPath()+".bak", raw, 0o600)
		}
	}
	return lib, nil
}

func (s *Store) SaveLibrary(lib *library.Library) error {
	return writeAtomic(s.libraryPath(), lib)
}

// Config — користувацькі налаштування. Ціль брифу: ≤ 8 налаштувань.
// Невідомі ключі (як мертвий `providers` зі старих версій) encoding/json
// ігнорує, тому старий config.json читається; ключ зникає при першому SaveConfig.
type Config struct {
	FavoriteStudio string `json:"favorite_studio,omitempty"`
	PreferKind     string `json:"prefer_kind,omitempty"` // dub | voiceover | sub
	Player         string `json:"player,omitempty"`      // vlc | mpv
	Autoplay       string `json:"autoplay,omitempty"`    // always | never
	Remote         string `json:"remote,omitempty"`      // on | open | off
}

func (s *Store) LoadConfig() (*Config, error) {
	cfg := &Config{}
	if _, err := readJSON(s.configPath(), cfg); err != nil {
		return nil, err
	}
	normalizeConfig(cfg)
	return cfg, nil
}

// SaveConfig пише конфіг атомарно, попередньо нормалізувавши: на диску ніколи
// не лежить значення, якого LoadConfig не повернув би.
func (s *Store) SaveConfig(cfg *Config) error {
	normalizeConfig(cfg)
	return writeAtomic(s.configPath(), cfg)
}

// DefaultConfig — конфіг з усіма дефолтами; те, що LoadConfig повертає без файла.
func DefaultConfig() *Config {
	cfg := &Config{}
	normalizeConfig(cfg)
	return cfg
}

// normalizeConfig тихо замінює невалідні значення дефолтами. Спільне для
// читання з диска і для імпорту бекапа: чужий config.json довіри не має більше,
// ніж свій.
func normalizeConfig(cfg *Config) {
	// Назва студії потрапляє в термінал (екран налаштувань), а чужий бекап
	// довіри не має — чистимо так само, як library чистить назви й піни.
	cfg.FavoriteStudio = provider.CleanText(cfg.FavoriteStudio)
	// multi як налаштування безглуздий: користувач обирає, чого хоче, а не
	// «невідомо що».
	switch provider.Kind(cfg.PreferKind) {
	case provider.KindDub, provider.KindVoiceover, provider.KindSub:
	default:
		cfg.PreferKind = string(provider.KindDub)
	}
	if cfg.Player != "vlc" && cfg.Player != "mpv" {
		cfg.Player = "vlc"
	}
	// «ask» ніколи не персистився кодом і більше не підтримується;
	// лишаються always | never.
	if cfg.Autoplay != "always" && cfg.Autoplay != "never" {
		cfg.Autoplay = "always"
	}
	// open — пульт без токена в корені; свідомий вибір, тому лише явним значенням.
	switch cfg.Remote {
	case "off", "open":
	default:
		cfg.Remote = "on"
	}
}

// RemoteIdentity — постійні порт і токен веб-пульта: закладка на телефоні має
// пережити перезапуск, тому обидва значення генеруються один раз.
type RemoteIdentity struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

func (s *Store) LoadRemoteIdentity() (RemoteIdentity, error) {
	var id RemoteIdentity
	found, err := readJSON(s.remotePath(), &id)
	if err != nil {
		var syntaxErr *json.SyntaxError
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
			return RemoteIdentity{}, err
		}
		found = false
	}
	if !found {
		return RemoteIdentity{Token: newRemoteToken()}, nil
	}
	if !validRemoteToken(id.Token) {
		id.Token = newRemoteToken()
	}
	if id.Port != 0 && (id.Port < 1024 || id.Port > 65535) {
		id.Port = 0
	}
	return id, nil
}

func (s *Store) SaveRemoteIdentity(id RemoteIdentity) error {
	return writeAtomic(s.remotePath(), id)
}

func newRemoteToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func validRemoteToken(token string) bool {
	if len(token) != 32 || token != strings.ToLower(token) {
		return false
	}
	_, err := hex.DecodeString(token)
	return err == nil
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
