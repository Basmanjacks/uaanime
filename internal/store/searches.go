package store

import (
	"path/filepath"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// maxSearches — стеля історії пошуку: список показується на екрані пошуку
// поруч із полем вводу, тож довший за екран сенсу не має.
const maxSearches = 5

// maxSearchLen — стеля довжини запиту в рунах. Запит малюється одним рядком
// списку; довший рядок — це не запит, а вставлений сміттєвий текст.
const maxSearchLen = 64

func (s *Store) searchesPath() string { return filepath.Join(s.dir, "state", "searches.json") }

// searches — формат state/searches.json. Об'єкт, а не голий масив: файл того ж
// класу, що health.json і remote.json, і колись може отримати ще поле.
type searches struct {
	Queries []string `json:"queries"`
}

// LoadSearches повертає історію пошуку, готову до показу. Помилки читання й
// битий JSON дають порожній список: історія — зручність, а не дані, через які
// варто ламати екран пошуку.
func (s *Store) LoadSearches() []string {
	var f searches
	if _, err := readJSON(s.searchesPath(), &f); err != nil {
		return nil
	}
	// Нормалізація застосовується і до прочитаного: файл могли відредагувати
	// руками, а в термінал з нього потрапляє сирий текст.
	return normalizeSearches(f.Queries)
}

// AddSearch піднімає запит на початок історії і повертає новий список.
func (s *Store) AddSearch(q string) ([]string, error) {
	list := pushRecent(s.LoadSearches(), q)
	return list, writeAtomic(s.searchesPath(), searches{Queries: list})
}

// RemoveSearch прибирає запит з історії і повертає новий список.
func (s *Store) RemoveSearch(q string) ([]string, error) {
	q = provider.CleanText(q)
	list := s.LoadSearches()
	out := make([]string, 0, len(list))
	for _, item := range list {
		if !strings.EqualFold(item, q) {
			out = append(out, item)
		}
	}
	out = normalizeSearches(out)
	return out, writeAtomic(s.searchesPath(), searches{Queries: out})
}

// pushRecent ставить q першим і нормалізує решту — дедуплікація в
// normalizeSearches лишає перше входження, тож підняття виходить безкоштовно.
func pushRecent(list []string, q string) []string {
	return normalizeSearches(append([]string{q}, list...))
}

// normalizeSearches — чиста функція: чистить текст, обрізає до maxSearchLen рун,
// викидає порожні, дедуплікує без урахування регістру, лишає перші maxSearches.
func normalizeSearches(list []string) []string {
	out := make([]string, 0, maxSearches)
	for _, q := range list {
		q = provider.CleanText(q)
		if r := []rune(q); len(r) > maxSearchLen {
			q = provider.CleanText(string(r[:maxSearchLen]))
		}
		if q == "" {
			continue
		}
		dup := false
		for _, seen := range out {
			if strings.EqualFold(seen, q) {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		out = append(out, q)
		if len(out) == maxSearches {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
