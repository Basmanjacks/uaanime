// Package provider — доменна модель джерел і контракт провайдера (сайту-каталогу).
// Провайдер повертає посилання на плеєри відеохостів; діставати з них потоки —
// робота internal/extractor. Ці шари не змішуються.
package provider

import (
	"context"
	"regexp"
	"strings"
	"unicode"
)

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

// Same — та сама ідентичність тайтлу: провайдер і слаг. Name та URL — це
// супровід, який у різних джерелах (картка, бібліотека після Normalize)
// відрізняється, тож порівнювати структуру цілком не можна.
func (r TitleRef) Same(o TitleRef) bool {
	return r.Provider == o.Provider && r.Slug == o.Slug
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
	Search, Catalog bool
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

// --- Санітизація недовірених рядків ---
//
// Усе, що приходить зі сторінки сайту або з диска (кеш, library.json), потрапляє
// в термінал. Керуючі символи в такому рядку — не косметика: ESC-послідовність
// перемальовує чужий екран, а C1 у 8-бітному режимі робить те саме без ESC.
// Тому текст чиститься на межі домену, а не в місці показу.

// CleanText прибирає керуючі символи і стискає пробіли до одного.
// Разом з ESC викидається і вся послідовність, яку він відкриває (CSI/OSC):
// самого лише ESC достатньо для знешкодження, але хвіст «[2J» лишався б у назві
// сміттям. Один прохід, без регулярок — викликається на кожен рядок кожної картки.
func CleanText(s string) string {
	const (
		text  = iota // звичайний текст
		esc          // щойно був ESC — вирішуємо, яка це форма
		csi          // тіло CSI, закінчується руном 0x40–0x7e
		osc          // тіло OSC, закінчується BEL або ST
		oscST        // усередині OSC трапився ESC — чекаємо '\'
	)
	var b strings.Builder
	b.Grow(len(s))
	state, space := text, false
	for _, r := range s {
		switch state {
		case esc:
			switch r {
			case '[':
				state = csi
			case ']':
				state = osc
			default:
				state = text // двосимвольні форми (ESC c тощо)
			}
			continue
		case csi:
			if r >= 0x40 && r <= 0x7e {
				state = text
			}
			continue
		case osc:
			switch r {
			case 0x07, 0x9c:
				state = text
			case 0x1b:
				state = oscST
			}
			continue
		case oscST:
			state = text
			continue
		}
		switch {
		case r == 0x1b:
			state = esc
		case r == 0x9b:
			state = csi
		case r == 0x9d:
			state = osc
		case unicode.IsSpace(r):
			// NBSP (U+00A0) і NEL (U+0085) теж IsSpace — на сайті вони трапляються
			// всередині назв і мають ставати звичайним пробілом.
			space = b.Len() > 0
		case unicode.IsControl(r), isBidiControl(r):
			// C0, DEL, решта C1 — просто зникають; bidi-керування теж: RLO чи
			// ізолятор із чужої назви переставляє символи всього рядка списку.
		default:
			if space {
				b.WriteRune(' ')
				space = false
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isBidiControl — явні керівники напряму письма (LRM/RLM, LRE…RLO, PDF,
// ізолятори LRI…PDI). Інші Cf (ZWJ, м'який перенос) лишаються: вони не
// шкодять, а у назвах трапляються.
func isBidiControl(r rune) bool {
	return r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

// reValidSlug — DLE-слаг: числовий id новини, дефіс, ascii-слово. Той самий
// шаблон валідує CLI-аргумент, кеш і library.json, тому живе тут, а не в провайдері.
var reValidSlug = regexp.MustCompile(`^\d+-[A-Za-z0-9_-]+$`)

func ValidSlug(slug string) bool { return reValidSlug.MatchString(slug) }

func ValidKind(k Kind) bool {
	switch k {
	case KindDub, KindVoiceover, KindSub, KindMulti:
		return true
	}
	return false
}

// CleanCard чистить людські поля картки і відкидає її, якщо після чистки нема
// ідентичності. URL обнуляється свідомо: коректний URL уміє побудувати лише
// провайдер зі слага (див. anitube.titleURL), а цей пакет не може імпортувати
// провайдера — цикл імпорту. Поза провайдером URL ніхто не читає: свіжі картки
// Search/Catalog несуть його для --json, а відтворення будує URL зі слага.
func CleanCard(c TitleCard) (TitleCard, bool) {
	c.Name = CleanText(c.Name)
	c.Episodes = CleanText(c.Episodes)
	c.Genres = cleanStrings(c.Genres)
	c.Studios = cleanStrings(c.Studios)
	c.URL = ""
	if !ValidSlug(c.Slug) || c.Name == "" || c.Provider == "" {
		return TitleCard{}, false
	}
	return c, true
}

func CleanCards(cards []TitleCard) []TitleCard {
	out := make([]TitleCard, 0, len(cards))
	for _, c := range cards {
		if clean, ok := CleanCard(c); ok {
			out = append(out, clean)
		}
	}
	return out
}

// CleanEpisode: реліз без студії показувати нема сенсу (це рядок вибору озвучки),
// а невідомий Kind стає multi — вгадувати sub заборонено правилом продукту.
func CleanEpisode(e Episode) Episode {
	releases := make([]Release, 0, len(e.Releases))
	for _, r := range e.Releases {
		r.Studio = CleanText(r.Studio)
		if r.Studio == "" {
			continue
		}
		if !ValidKind(r.Kind) {
			r.Kind = KindMulti
		}
		releases = append(releases, r)
	}
	e.Releases = releases
	return e
}

func CleanEpisodes(eps []Episode) []Episode {
	out := make([]Episode, 0, len(eps))
	// Номер не фільтрується: парсер віддає «0 серія» як 0, і кеш має
	// показувати той самий список, що й живий сайт.
	for _, e := range eps {
		out = append(out, CleanEpisode(e))
	}
	return out
}

func cleanStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v = CleanText(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
