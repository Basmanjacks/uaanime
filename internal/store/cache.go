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

// Кеш блоків каталогу (топ сезону, свіжі). Оновлюється рідше за серії:
// добірка на головній міняється раз на день, не раз на годину.
const catalogTTL = 12 * time.Hour

type catalogCache struct {
	FetchedAt time.Time            `json:"fetched_at"`
	Year      int                  `json:"year"`
	Cards     []provider.TitleCard `json:"cards"`
}

func (s *Store) catalogCachePath(providerID string, kind provider.CatalogKind) string {
	return filepath.Join(s.dir, "cache", fmt.Sprintf("catalog-%s-%s.json", providerID, kind))
}

func (s *Store) SaveCatalog(providerID string, kind provider.CatalogKind, cards []provider.TitleCard) error {
	return writeAtomic(s.catalogCachePath(providerID, kind), &catalogCache{
		FetchedAt: time.Now(),
		Year:      time.Now().Year(),
		Cards:     cards,
	})
}

// LoadCatalog повертає кешований блок каталогу; fresh=false — TTL сплив,
// дані годяться лише як офлайн-fallback.
func (s *Store) LoadCatalog(providerID string, kind provider.CatalogKind) (cards []provider.TitleCard, fresh bool, found bool) {
	var c catalogCache
	ok, err := readJSON(s.catalogCachePath(providerID, kind), &c)
	if err != nil || !ok {
		return nil, false, false
	}
	// «Топ сезону» минулого року — не застарілі дані, а неправильні: після
	// Нового року вони показали б торішній топ як поточний. TTL тут не рятує
	// (офлайн-fallback віддає кеш будь-якої давності), тому такий запис
	// вважаємо відсутнім, а не просто несвіжим.
	if kind == provider.CatalogTopSeason && c.Year != time.Now().Year() {
		return nil, false, false
	}
	return c.Cards, time.Since(c.FetchedAt) < catalogTTL, true
}
