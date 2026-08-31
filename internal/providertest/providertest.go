// Package providertest — спільні contract-тести для всіх провайдерів.
// Саме вони роблять додавання сайту дешевим: однакові інваріанти для кожного,
// без саморобних тестів різної якості.
package providertest

import (
	"context"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Cases — канонічні входи провайдера, вже підключеного до фікстур.
type Cases struct {
	SearchQuery   string
	MultiStudio   provider.TitleRef // сторінка з >1 студією
	SingleRelease provider.TitleRef // одна студія
	Ongoing       provider.TitleRef // онгоїнг з частковими релізами
	Episode       int               // серія, що існує в усіх трьох тайтлах
}

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
		refs, err := p.Search(ctx, c.SearchQuery)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(refs) == 0 {
			t.Fatal("Search: порожній результат на канонічний запит")
		}
		for _, r := range refs {
			if r.Slug == "" || r.Name == "" || r.URL == "" {
				t.Errorf("Search: неповний TitleRef: %+v", r)
			}
			if r.Provider != p.ID() {
				t.Errorf("Search: Provider=%q, очікував %q", r.Provider, p.ID())
			}
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
		}
	})
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
