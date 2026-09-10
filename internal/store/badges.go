package store

import (
	"os"
	"path/filepath"
)

type badgeCursor struct {
	Cursor string `json:"cursor"`
}

// LoadBadgeCursor returns the optional provider/slug scheduling cursor. Missing
// or corrupt metadata restarts rotation and never affects durable user state.
func (s *Store) LoadBadgeCursor() string {
	var c badgeCursor
	if found, err := readJSON(filepath.Join(s.dir, "cache", "badge-cursor.json"), &c); err != nil || !found {
		return ""
	}
	return c.Cursor
}

// SaveBadgeCursor persists scheduling metadata outside config and exports.
func (s *Store) SaveBadgeCursor(cursor string) error {
	if err := os.MkdirAll(filepath.Join(s.dir, "cache"), 0700); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "cache", "badge-cursor.json"), &badgeCursor{Cursor: cursor})
}
