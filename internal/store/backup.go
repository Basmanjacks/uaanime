package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Basmanjacks/uaanime/internal/library"
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

// Import замінює бібліотеку вмістом бекапа; попередня зберігається як .bak.
func (s *Store) Import(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	var b Backup
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("бекап: битий JSON: %w", err)
	}
	if b.Library == nil {
		return fmt.Errorf("бекап: немає розділу library")
	}
	if prev, err := os.ReadFile(s.libraryPath()); err == nil {
		if err := os.WriteFile(s.libraryPath()+".bak", prev, 0o644); err != nil {
			return err
		}
	}
	if err := writeAtomic(s.libraryPath(), b.Library); err != nil {
		return err
	}
	if b.Config != nil {
		if err := writeAtomic(s.configPath(), b.Config); err != nil {
			return err
		}
	}
	return nil
}
