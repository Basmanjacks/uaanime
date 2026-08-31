package store

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Кеш метаданих (списки серій) з TTL. URL потоків сюди не потрапляють ніколи —
// вони протухають. Ключ — (provider, slug): спільний кеш за назвою отруював би
// дані одного сайту іншим.
const episodesTTL = 6 * time.Hour

type episodesCache struct {
	FetchedAt time.Time          `json:"fetched_at"`
	Episodes  []provider.Episode `json:"episodes"`
}

func (s *Store) episodesCachePath(ref provider.TitleRef) string {
	return filepath.Join(s.dir, "cache", fmt.Sprintf("episodes-%s-%s.json", ref.Provider, ref.Slug))
}

func (s *Store) SaveEpisodes(ref provider.TitleRef, eps []provider.Episode) error {
	return writeAtomic(s.episodesCachePath(ref), &episodesCache{
		FetchedAt: time.Now(),
		Episodes:  eps,
	})
}

// LoadEpisodes повертає кешовані серії; fresh=false означає, що TTL сплив
// і дані годяться лише як офлайн-fallback.
func (s *Store) LoadEpisodes(ref provider.TitleRef) (eps []provider.Episode, fresh bool, found bool) {
	var c episodesCache
	ok, err := readJSON(s.episodesCachePath(ref), &c)
	if err != nil || !ok {
		return nil, false, false
	}
	return c.Episodes, time.Since(c.FetchedAt) < episodesTTL, true
}
