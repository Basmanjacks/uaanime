package anitube

import (
	"testing"

	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
)

func fixtureClient() *Client {
	return New(httpx.NewClient(FixtureTransport("testdata")))
}

func TestContract(t *testing.T) {
	providertest.Run(t, fixtureClient(), providertest.Cases{
		SearchQuery:   searchFixtureQuery,
		MultiStudio:   CanonicalRef("title-multi-studio"),
		SingleRelease: CanonicalRef("title-single-release"),
		Ongoing:       CanonicalRef("title-ongoing"),
		Episode:       1,
	})
}

// Судзуме: ієрархія студія→тип→плеєр і тип ДУБЛЯЖ — layout, який зламав би
// парсер, що вірить у фіксований порядок рівнів.
func TestDubLayout(t *testing.T) {
	c := fixtureClient()
	sources, err := c.Sources(t.Context(), CanonicalRef("title-dub-layout"), 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	var hasDub bool
	for _, s := range sources {
		if s.Kind == provider.KindDub {
			hasDub = true
			if s.Studio == "" {
				t.Errorf("дубляж без студії: %+v", s)
			}
		}
		if s.Kind == provider.KindSub && s.Studio == "" {
			t.Errorf("sub без студії: %+v", s)
		}
	}
	if !hasDub {
		t.Error("очікував KindDub у фікстурі з розділом ДУБЛЯЖ")
	}
}

// Субтитри в single-release фікстурі мають розпізнаватися як sub, а не вгадуватись.
func TestSingleReleaseIsSub(t *testing.T) {
	c := fixtureClient()
	sources, err := c.Sources(t.Context(), CanonicalRef("title-single-release"), 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	for _, s := range sources {
		if s.Kind != provider.KindSub {
			t.Errorf("очікував sub, отримав %q: %+v", s.Kind, s)
		}
	}
}

func TestStudioAndKind(t *testing.T) {
	cases := []struct {
		l1, l2 string
		studio string
		kind   provider.Kind
	}{
		{"ОЗВУЧЕННЯ", "FANVOXUA", "FANVOXUA", provider.KindVoiceover}, // тип→студія
		{"СУБТИТРИ", "Glass Moon", "Glass Moon", provider.KindSub},
		{"Unimay", "ДУБЛЯЖ", "Unimay", provider.KindDub}, // студія→тип
		{"Робота Голосом", "ОЗВУЧЕННЯ", "Робота Голосом", provider.KindVoiceover},
		{"Студія X", "", "Студія X", provider.KindMulti}, // тип невідомий — не вгадуємо sub
		{"", "", "", provider.KindMulti},
	}
	for _, tc := range cases {
		studio, kind := studioAndKind(tc.l1, tc.l2)
		if studio != tc.studio || kind != tc.kind {
			t.Errorf("studioAndKind(%q, %q) = (%q, %q), очікував (%q, %q)",
				tc.l1, tc.l2, studio, kind, tc.studio, tc.kind)
		}
	}
}

func TestEpisodeNumber(t *testing.T) {
	cases := []struct {
		in string
		n  int
		ok bool
	}{
		{"1 серія", 1, true},
		{"25 серія", 25, true},
		{"Фільм", 1, true}, // фільм без номера — серія 1
		{"", 0, false},
	}
	for _, tc := range cases {
		n, ok := episodeNumber(tc.in)
		if n != tc.n || ok != tc.ok {
			t.Errorf("episodeNumber(%q) = (%d, %v), очікував (%d, %v)", tc.in, n, ok, tc.n, tc.ok)
		}
	}
}

// Помилковий HTML не має викликати паніку — лише помилку.
func TestMalformedInputNoPanic(t *testing.T) {
	for _, html := range []string{"", "<li", "<li data-file=\"\">x</li>", "не html взагалі"} {
		if _, err := parsePlaylists(html, "https://example.invalid"); err == nil {
			t.Errorf("parsePlaylists(%q): очікував помилку", html)
		}
	}
	if _, err := parseSearch([]byte("<div>сміття")); err != nil {
		t.Errorf("parseSearch на сміתті має віддати порожній список без помилки, отримав: %v", err)
	}
}
