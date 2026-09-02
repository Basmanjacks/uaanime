// Package providertest — спільні contract-тести для всіх провайдерів.
// Саме вони роблять додавання сайту дешевим: однакові інваріанти для кожного,
// без саморобних тестів різної якості.
package providertest

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Cases — канонічні входи провайдера, вже підключеного до фікстур.
// PagedQuery і Catalogs необов'язкові: порожнє значення вимикає відповідні
// підтести, тому провайдер без таких фікстур усе одно проходить контракт.
type Cases struct {
	SearchQuery   string
	PagedQuery    string                 // запит із щонайменше двома сторінками результатів
	Catalogs      []provider.CatalogKind // добірки, для яких є фікстури
	MultiStudio   provider.TitleRef      // сторінка з >1 студією
	SingleRelease provider.TitleRef      // одна студія
	Ongoing       provider.TitleRef      // онгоїнг з частковими релізами
	Episode       int                    // серія, що існує в усіх трьох тайтлах
}

const (
	// pageSize — скільки результатів має вміщати повна сторінка пошуку.
	pageSize = 10
	// catalogMax — стеля добірки: каталог показує топ, а не весь сайт.
	catalogMax = 20
)

var validKinds = map[provider.Kind]bool{
	provider.KindDub:       true,
	provider.KindVoiceover: true,
	provider.KindSub:       true,
	provider.KindMulti:     true,
}

func Run(t *testing.T, p provider.Provider, c Cases) {
	t.Helper()
	ctx := context.Background()

	t.Run("search", func(t *testing.T) {
		page, err := p.Search(ctx, c.SearchQuery, 1)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Titles) == 0 {
			t.Fatal("Search: порожній результат на канонічний запит")
		}
		mustRefs(t, p, "Search", page.Titles)
	})

	// Метадані картки — те, чим сторінка пошуку відрізняється від голого списку
	// посилань. Половина як поріг: окремі тайтли справді бувають без року чи оцінки.
	t.Run("card-metadata", func(t *testing.T) {
		if !p.Caps().Search {
			t.Skip("провайдер не вміє шукати")
		}
		page, err := p.Search(ctx, c.SearchQuery, 1)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(page.Titles) == 0 {
			t.Fatal("Search: порожній результат на канонічний запит")
		}
		withMeta := 0
		for _, card := range page.Titles {
			if card.Year > 0 && card.Rating > 0 {
				withMeta++
			}
		}
		if withMeta*2 < len(page.Titles) {
			t.Errorf("card-metadata: рік+рейтинг лише в %d з %d карток, очікував щонайменше половину",
				withMeta, len(page.Titles))
		}
	})

	t.Run("search-page", func(t *testing.T) {
		if c.PagedQuery == "" {
			t.Skip("немає фікстури другої сторінки")
		}
		if !p.Caps().Search {
			t.Skip("провайдер не вміє шукати")
		}
		first, err := p.Search(ctx, c.PagedQuery, 1)
		if err != nil {
			t.Fatalf("Search(page 1): %v", err)
		}
		if len(first.Titles) < pageSize {
			t.Fatalf("Search(page 1): %d результатів, очікував щонайменше %d", len(first.Titles), pageSize)
		}
		if !first.HasMore {
			t.Error("Search(page 1): HasMore=false на повній сторінці")
		}
		mustRefs(t, p, "Search(page 1)", first.Titles)

		second, err := p.Search(ctx, c.PagedQuery, 2)
		if err != nil {
			t.Fatalf("Search(page 2): %v", err)
		}
		if len(second.Titles) == 0 {
			t.Fatal("Search(page 2): порожньо")
		}
		mustRefs(t, p, "Search(page 2)", second.Titles)

		// Сторінки не перетинаються — інакше пагінація насправді не працює
		// і користувач гортає той самий список.
		seen := map[string]bool{}
		for _, card := range first.Titles {
			seen[card.Slug] = true
		}
		for _, card := range second.Titles {
			if seen[card.Slug] {
				t.Errorf("Search: слаг %q є і на першій, і на другій сторінці", card.Slug)
			}
		}
	})

	t.Run("catalog", func(t *testing.T) {
		if len(c.Catalogs) == 0 {
			t.Skip("немає фікстур каталогу")
		}
		if !p.Caps().Catalog {
			t.Skip("провайдер не має каталогу")
		}
		for _, kind := range c.Catalogs {
			t.Run(string(kind), func(t *testing.T) {
				cards, err := p.Catalog(ctx, kind)
				if err != nil {
					t.Fatalf("Catalog(%s): %v", kind, err)
				}
				if len(cards) == 0 {
					t.Fatalf("Catalog(%s): порожньо", kind)
				}
				if len(cards) > catalogMax {
					t.Errorf("Catalog(%s): %d карток, ліміт %d", kind, len(cards), catalogMax)
				}
				mustRefs(t, p, "Catalog("+string(kind)+")", cards)
			})
		}
	})

	t.Run("multi-studio", func(t *testing.T) {
		sources := mustSources(t, p, c.MultiStudio, c.Episode)
		studios := map[string]bool{}
		for _, s := range sources {
			studios[s.Studio] = true
		}
		if len(studios) < 2 {
			t.Errorf("multi-studio: очікував >1 студії, отримав %v", keys(studios))
		}
	})

	t.Run("single-release", func(t *testing.T) {
		mustSources(t, p, c.SingleRelease, c.Episode)
	})

	t.Run("ongoing", func(t *testing.T) {
		eps, err := p.Episodes(ctx, c.Ongoing)
		if err != nil {
			t.Fatalf("Episodes(ongoing): %v", err)
		}
		if len(eps) == 0 {
			t.Fatal("Episodes(ongoing): порожньо")
		}
		seen := map[int]bool{}
		for _, e := range eps {
			if e.Number < 1 {
				t.Errorf("Episodes: недодатний номер %d", e.Number)
			}
			if seen[e.Number] {
				t.Errorf("Episodes: дубль номера %d", e.Number)
			}
			seen[e.Number] = true
			if len(e.Releases) == 0 {
				t.Errorf("Episodes: серія %d без релізів", e.Number)
			}
			for _, r := range e.Releases {
				mustNoControls(t, "Release.Studio", r.Studio)
			}
		}
	})
}

// mustRefs перевіряє інваріанти ідентичності будь-якого списку карток:
// без них тайтл неможливо ні відкрити, ні зберегти.
func mustRefs(t *testing.T, p provider.Provider, where string, cards []provider.TitleCard) {
	t.Helper()
	for _, card := range cards {
		if card.Slug == "" || card.Name == "" || card.URL == "" {
			t.Errorf("%s: неповний TitleRef: %+v", where, card.TitleRef)
		}
		if card.Provider != p.ID() {
			t.Errorf("%s: Provider=%q, очікував %q", where, card.Provider, p.ID())
		}
		// Слаг — ідентичність тайтлу: він потрапляє в library.json, в імена
		// файлів кешу і в CLI-аргумент, тому мусить бути в канонічній формі.
		if !provider.ValidSlug(card.Slug) {
			t.Errorf("%s: невалідний слаг %q", where, card.Slug)
		}
		// Свіжа картка несе URL, побудований провайдером, а не взятий із розмітки:
		// http:// або чужий хост тут означали б, що провайдер довіряє href.
		if !strings.HasPrefix(card.URL, "https://") {
			t.Errorf("%s: URL=%q не починається з https://", where, card.URL)
		}
		mustNoControls(t, where+": Name", card.Name)
		for _, g := range card.Genres {
			mustNoControls(t, where+": Genre", g)
		}
		for _, s := range card.Studios {
			mustNoControls(t, where+": Studio", s)
		}
	}
}

// mustNoControls — межа між недовіреним HTML і терміналом користувача:
// керуючий символ у назві чи студії перемальовує чужий екран.
func mustNoControls(t *testing.T, where, s string) {
	t.Helper()
	for _, r := range s {
		if unicode.IsControl(r) {
			t.Errorf("%s: керуючий символ %U у %q", where, r, s)
			return
		}
	}
}

// mustSources перевіряє спільні інваріанти будь-якого списку джерел.
func mustSources(t *testing.T, p provider.Provider, ref provider.TitleRef, ep int) []provider.Source {
	t.Helper()
	sources, err := p.Sources(context.Background(), ref, ep)
	if err != nil {
		t.Fatalf("Sources(%s, %d): %v", ref.Slug, ep, err)
	}
	if len(sources) == 0 {
		t.Fatalf("Sources(%s, %d): порожньо", ref.Slug, ep)
	}
	for _, s := range sources {
		if s.Embed == "" {
			t.Errorf("Source без Embed: %+v", s)
		}
		if s.Studio == "" {
			t.Errorf("Source без Studio: %+v", s)
		}
		if !validKinds[s.Kind] {
			t.Errorf("Source з невідомим Kind %q: %+v", s.Kind, s)
		}
		mustNoControls(t, "Source.Studio", s.Studio)
	}
	return sources
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
