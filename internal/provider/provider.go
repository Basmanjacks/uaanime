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
	Search, Updates, Ongoing, Descriptions, Subtitles bool
}

type Provider interface {
	ID() string   // стабільний, потрапляє в дані користувача
	Name() string // показуємо як є, не перекладаємо
	Caps() Caps

	Search(ctx context.Context, q string) ([]TitleRef, error)
	Episodes(ctx context.Context, ref TitleRef) ([]Episode, error)
	Sources(ctx context.Context, ref TitleRef, episode int) ([]Source, error)
}
