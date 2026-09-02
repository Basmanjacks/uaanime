package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Backup — формат export/import: увесь стан користувача одним файлом.
type Backup struct {
	ExportedAt time.Time        `json:"exported_at"`
	Library    *library.Library `json:"library"`
	Config     *Config          `json:"config,omitempty"`
}

func (s *Store) Export(w io.Writer) error {
	lib, err := s.LoadLibrary()
	if err != nil {
		return err
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(&Backup{ExportedAt: time.Now(), Library: lib, Config: cfg})
}

// maxBackup — стеля розміру бекапа. Реальний бекап — десятки кілобайт;
// усе більше означає або не той файл, або спробу з'їсти пам'ять.
const maxBackup = 16 << 20

// Import замінює бібліотеку вмістом бекапа; попередня зберігається як .bak.
func (s *Store) Import(r io.Reader) error {
	// +1 байт, щоб відрізнити «рівно стеля» від «більше за стелю»:
	// тихо обрізаний бекап дав би битий JSON замість зрозумілої помилки.
	data, err := io.ReadAll(io.LimitReader(r, maxBackup+1))
	if err != nil {
		return err
	}
	if len(data) > maxBackup {
		return fmt.Errorf("бекап завеликий: понад %d МіБ", maxBackup>>20)
	}
	var b Backup
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("бекап: битий JSON: %w", err)
	}
	if b.Library == nil {
		return fmt.Errorf("бекап: немає розділу library")
	}
	// Нормалізація в пам'яті ДО будь-якого запису: імпорт заміняє весь стан,
	// тому мовчазна втрата записів гірша за відмову. Нічого не пишемо взагалі —
	// ні .bak, ні бібліотеки, ні конфіга.
	if dropped := b.Library.Normalize(provider.CleanText); dropped > 0 {
		return fmt.Errorf("бекап містить невалідні записи (%d)", dropped)
	}
	if prev, err := os.ReadFile(s.libraryPath()); err == nil {
		if err := os.WriteFile(s.libraryPath()+".bak", prev, 0o600); err != nil {
			return err
		}
	}
	if err := writeAtomic(s.libraryPath(), b.Library); err != nil {
		return err
	}
	if b.Config != nil {
		normalizeConfig(b.Config)
		if err := writeAtomic(s.configPath(), b.Config); err != nil {
			return err
		}
	}
	return nil
}
