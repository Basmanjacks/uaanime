// Package provider — доменна модель джерел і контракт провайдера (сайту-каталогу).
// Провайдер повертає посилання на плеєри відеохостів; діставати з них потоки —
// робота internal/extractor. Ці шари не змішуються.
package provider

import "context"

// Kind — тип релізу (ADR-001). Дубляж і закадрове — різні речі:
// правило «не деградувати до сабів» стосується обох озвучених типів.
type Kind string

const (
	KindDub       Kind = "dub"
	KindVoiceover Kind = "voiceover"
	KindSub       Kind = "sub"
	// KindMulti — сторінка не дає однозначних доказів типу. Ніколи не вгадуємо sub:
	// помилковий sub мовчки порушує правило незниження до субтитрів.
	KindMulti Kind = "multi"
)

// TitleRef — шлях до тайтлу на конкретному провайдері. Прогрес користувача
// прив'язується НЕ до нього, а до локального ID (Phase 2).
type TitleRef struct {
	Provider string `json:"provider"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

// TitleCard — картка тайтлу зі сторінки пошуку/каталогу. Вбудовує TitleRef
// (ідентичність), решта полів — метадані для відображення; вони НЕ персистяться.
type TitleCard struct {
	TitleRef
	Year     int      `json:"year,omitempty"`
	Episodes string   `json:"episodes,omitempty"`
	EpAired  int      `json:"episodes_aired,omitempty"`
	EpTotal  int      `json:"episodes_total,omitempty"`
	Rating   float64  `json:"rating,omitempty"`
	Votes    int      `json:"votes,omitempty"`
	Genres   []string `json:"genres,omitempty"`
	Studios  []string `json:"studios,omitempty"`
	HasDub   bool     `json:"has_dub,omitempty"`
	HasSub   bool     `json:"has_sub,omitempty"`
}

type Page struct {
	Titles  []TitleCard `json:"titles"`
	HasMore bool        `json:"has_more"`
}

type CatalogKind string

const (
	CatalogTopSeason CatalogKind = "top-season"
	CatalogFresh     CatalogKind = "fresh"
)

// Release — пара (студія, тип). Головна фіча продукту: одна серія має кілька
// варіантів озвучення від різних студій плюс субтитри.
type Release struct {
	Studio string `json:"studio"`
	Kind   Kind   `json:"kind"`
}

type Episode struct {
	Number   int       `json:"number"`
	Releases []Release `json:"releases"`
}

// Source — конкретний варіант перегляду серії: реліз + embed плеєра.
type Source struct {
	Studio  string `json:"studio"`
	Kind    Kind   `json:"kind"`
	Embed   string `json:"embed"`
	Referer string `json:"referer"`
	Episode int    `json:"episode"`
}

// Caps дозволяє інтерфейсу деградувати без перевірок на ID провайдера.
type Caps struct {
	Search, Catalog, Updates, Ongoing, Descriptions, Subtitles bool
}

type Provider interface {
	ID() string   // стабільний, потрапляє в дані користувача
	Name() string // показуємо як є, не перекладаємо
	Caps() Caps

	Search(ctx context.Context, q string, page int) (Page, error)
	Catalog(ctx context.Context, kind CatalogKind) ([]TitleCard, error)
	Episodes(ctx context.Context, ref TitleRef) ([]Episode, error)
	Sources(ctx context.Context, ref TitleRef, episode int) ([]Source, error)
}
