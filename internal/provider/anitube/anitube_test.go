package anitube

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
)

// CanonicalRef повертає TitleRef канонічної фікстури за її ім'ям файлу.
// Живе в тестах: продакшн-код фікстури за іменем файлу не адресує.
// Слаги в canonicalTitles валідні за побудовою, тому помилка тут неможлива.
func CanonicalRef(file string) provider.TitleRef {
	for _, t := range canonicalTitles {
		if t.File == file {
			ref, err := RefFromSlug(t.Slug)
			if err != nil {
				return provider.TitleRef{}
			}
			return ref
		}
	}
	return provider.TitleRef{}
}

func fixtureClient() *Client {
	return New(httpx.NewClient(FixtureTransport("testdata")))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFixtureTransportRoutesNewFixtures(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		request  string
		form     url.Values
		wantFile string
	}{
		{
			name:    "друга сторінка пошуку",
			method:  http.MethodPost,
			request: baseURL + "/index.php?do=search",
			form: url.Values{
				"story":        {searchPagedQuery},
				"search_start": {"2"},
			},
			wantFile: "search-paged-2.html",
		},
		{
			name:     "головна — джерело обох добірок",
			method:   http.MethodGet,
			request:  baseURL + "/",
			wantFile: "catalog-fresh.html",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.form != nil {
				body = strings.NewReader(tc.form.Encode())
			}
			req, err := http.NewRequest(tc.method, tc.request, body)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			res, err := FixtureTransport(t.TempDir()).RoundTrip(req)
			if res != nil {
				_ = res.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantFile) {
				t.Fatalf("RoundTrip error = %v, очікував назву %q", err, tc.wantFile)
			}
		})
	}
}

func TestSearchPaginationForm(t *testing.T) {
	fixture, err := os.ReadFile("testdata/search.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	tests := []struct {
		name        string
		page        int
		searchStart string
		resultFrom  string
	}{
		{name: "перша сторінка", page: 1, searchStart: "0", resultFrom: "1"},
		{name: "друга сторінка", page: 2, searchStart: "2", resultFrom: "11"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body url.Values
			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				rawBody, readErr := io.ReadAll(req.Body)
				if readErr != nil {
					t.Fatalf("ReadAll(request body): %v", readErr)
				}
				body, readErr = url.ParseQuery(string(rawBody))
				if readErr != nil {
					t.Fatalf("ParseQuery(request body): %v", readErr)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(fixture)),
					Header:     make(http.Header),
				}, nil
			})

			const query = "фрірен & друзі"
			if _, err := New(httpx.NewClient(transport)).Search(t.Context(), query, tc.page); err != nil {
				t.Fatalf("Search: %v", err)
			}
			want := url.Values{
				"do":           {"search"},
				"subaction":    {"search"},
				"search_start": {tc.searchStart},
				"full_search":  {"0"},
				"result_from":  {tc.resultFrom},
				"story":        {query},
			}
			if body.Encode() != want.Encode() {
				t.Errorf("POST body = %q, очікував %q", body.Encode(), want.Encode())
			}
		})
	}
}

func TestSourcesMissingEpisodeIsNoStream(t *testing.T) {
	_, err := fixtureClient().Sources(t.Context(), CanonicalRef("title-single-release"), 999)
	if !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Sources error = %v, очікував ErrNoStream", err)
	}
}

func TestContract(t *testing.T) {
	providertest.Run(t, fixtureClient(), providertest.Cases{
		SearchQuery:   searchFixtureQuery,
		PagedQuery:    searchPagedQuery,
		Catalogs:      []provider.CatalogKind{provider.CatalogTopSeason, provider.CatalogFresh},
		MultiStudio:   CanonicalRef("title-multi-studio"),
		SingleRelease: CanonicalRef("title-single-release"),
		Ongoing:       CanonicalRef("title-ongoing"),
		Episode:       1,
	})
}

func TestParseCards(t *testing.T) {
	body, err := os.ReadFile("testdata/search.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	cards, err := parseCards(body)
	if err != nil {
		t.Fatalf("parseCards: %v", err)
	}
	if len(cards) != 3 {
		t.Fatalf("len(cards) = %d, очікував 3", len(cards))
	}

	first := cards[0]
	if first.Name != "Фрірен, що проводжає в останню путь (1 сезон)" {
		t.Errorf("Name = %q", first.Name)
	}
	if first.Year != 2023 {
		t.Errorf("Year = %d, очікував 2023", first.Year)
	}
	if first.Episodes != "28 з 28" || first.EpAired != 28 || first.EpTotal != 28 {
		t.Errorf("серії = %q (%d/%d), очікував 28 з 28 (28/28)", first.Episodes, first.EpAired, first.EpTotal)
	}
	if first.Rating != 9.5 || first.Votes != 3340 {
		t.Errorf("рейтинг = %v (%d голосів), очікував 9.5 (3340 голосів)", first.Rating, first.Votes)
	}
	if !first.HasDub || !first.HasSub {
		t.Errorf("HasDub/HasSub = %v/%v, очікував true/true", first.HasDub, first.HasSub)
	}
	if len(first.Studios) < 5 {
		t.Errorf("Studios = %v, очікував щонайменше 5", first.Studios)
	}
	if len(first.Genres) < 3 {
		t.Errorf("Genres = %v, очікував щонайменше 3", first.Genres)
	}
}

// Обидві добірки читаються з однієї головної сторінки — фікстура catalog-fresh.html.
func assertCatalogCards(t *testing.T, cards []provider.TitleCard) {
	t.Helper()
	if len(cards) == 0 {
		t.Fatal("каталог порожній")
	}
	if len(cards) > catalogLimit {
		t.Fatalf("len(cards) = %d, ліміт %d", len(cards), catalogLimit)
	}
	for i, card := range cards {
		if card.Provider != providerID {
			t.Errorf("cards[%d].Provider = %q, очікував %q", i, card.Provider, providerID)
		}
		if card.Slug == "" || card.Name == "" || card.URL == "" {
			t.Errorf("cards[%d] неповна: %+v", i, card)
		}
	}
}

func TestCatalogTopSeason(t *testing.T) {
	cards, err := fixtureClient().Catalog(t.Context(), provider.CatalogTopSeason)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	assertCatalogCards(t, cards)

	const wantFirst = "Реінкарнація безробітного: В інший світ на повному серйозі 3 сезон"
	if cards[0].Name != wantFirst {
		t.Errorf("cards[0].Name = %q, очікував %q", cards[0].Name, wantFirst)
	}
}

func TestCatalogFresh(t *testing.T) {
	cards, err := fixtureClient().Catalog(t.Context(), provider.CatalogFresh)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	assertCatalogCards(t, cards)

	seen := make(map[string]bool)
	for _, card := range cards {
		if seen[card.Slug] {
			t.Errorf("дубль слага %q", card.Slug)
		}
		seen[card.Slug] = true
	}
}

func TestCatalogUnknownKindIsProviderError(t *testing.T) {
	_, err := fixtureClient().Catalog(t.Context(), provider.CatalogKind("нема-такого"))
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Catalog error = %v, очікував ErrProvider", err)
	}
}

func TestParseCardsMalformed(t *testing.T) {
	tests := []struct {
		name string
		html string
		want int
	}{
		{name: "сміття", html: "<html><body><div>сміття</div></body></html>", want: 0},
		{
			name: "обрізана картка",
			html: `<article class="story"><h2 itemprop="name"><a href="https://anitube.in.ua/4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon.html">Фрірен, що проводжає в останню путь (1 сезон)</a></h2><div class="story_infa"><dt>Рік виходу аніме:</dt>`,
			want: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cards, err := parseCards([]byte(tc.html))
			if err != nil {
				t.Fatalf("parseCards: %v", err)
			}
			if len(cards) != tc.want {
				t.Errorf("len(cards) = %d, очікував %d", len(cards), tc.want)
			}
		})
	}
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

func TestFlatPlaylist(t *testing.T) {
	c := fixtureClient()
	ref := CanonicalRef("title-flat-ova")
	episodes, err := c.Episodes(t.Context(), ref)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("len(episodes) = %d, очікував 3", len(episodes))
	}
	for i, episode := range episodes {
		if episode.Number != i+1 {
			t.Errorf("episodes[%d].Number = %d, очікував %d", i, episode.Number, i+1)
		}
		want := provider.Release{Studio: "MetalFreak", Kind: provider.KindMulti}
		if len(episode.Releases) != 1 || episode.Releases[0] != want {
			t.Errorf("episodes[%d].Releases = %+v, очікував [%+v]", i, episode.Releases, want)
		}
	}

	sources, err := c.Sources(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, очікував 1", len(sources))
	}
	if sources[0].Embed != "https://tortuga.tw/vod/44420" {
		t.Errorf("Embed = %q", sources[0].Embed)
	}
	if sources[0].Referer != ref.URL {
		t.Errorf("Referer = %q, очікував %q", sources[0].Referer, ref.URL)
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

// Помилковий HTML не має викликати паніку; неповні гілки пропускаються,
// а плоска гілка може скористатися резервною студією.
func TestMalformedInputNoPanic(t *testing.T) {
	tests := []struct {
		name           string
		html           string
		fallbackStudio string
		wantStudio     string
	}{
		{name: "порожній HTML"},
		{name: "обрізаний HTML", html: "<li"},
		{name: "порожній data-file", html: `<li data-file="">x</li>`},
		{name: "не HTML", html: "не html взагалі"},
		{
			name: "замала глибина",
			html: `<li data-file="https://example.invalid/1" data-id="0">1 серія</li>`,
		},
		{
			name: "плоский без студії",
			html: `<li data-file="https://example.invalid/1" data-id="0_0">1 серія</li>`,
		},
		{
			name:           "плоский із резервною студією",
			html:           `<li data-file="https://example.invalid/1" data-id="0_0">1 серія</li>`,
			fallbackStudio: "X",
			wantStudio:     "X",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sources, err := parsePlaylists(tc.html, "https://example.invalid", tc.fallbackStudio)
			if tc.wantStudio == "" {
				if err == nil {
					t.Errorf("parsePlaylists(%q): очікував помилку", tc.html)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePlaylists: %v", err)
			}
			if len(sources) != 1 || sources[0].Studio != tc.wantStudio {
				t.Errorf("sources = %+v, очікував студію %q", sources, tc.wantStudio)
			}
		})
	}
}

// URL картки будується зі слага, а не з href: ворожа сторінка не має змоги
// скерувати застосунок на loopback (звідти й фетч, і запис у library.json).
func TestCardURLIgnoresHref(t *testing.T) {
	const html = `<article class="story"><h2 itemprop="name">` +
		`<a href="http://127.0.0.1:9200/1-x.html">Тайтл</a></h2></article>`
	cards, err := parseCards([]byte(html))
	if err != nil {
		t.Fatalf("parseCards: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("len(cards) = %d, очікував 1", len(cards))
	}
	if want := baseURL + "/1-x.html"; cards[0].URL != want {
		t.Errorf("URL = %q, очікував %q", cards[0].URL, want)
	}
	if cards[0].Slug != "1-x" {
		t.Errorf("Slug = %q, очікував %q", cards[0].Slug, "1-x")
	}
}

// Слаг із traversal чи не-ascii не має ставати карткою: він потрапив би
// в library.json та в імена файлів кешу.
func TestCardFromAnchorRejectsBadSlug(t *testing.T) {
	for _, href := range []string{
		"https://anitube.in.ua/../../etc/passwd.html",
		"https://anitube.in.ua/4465-фрірен.html",
		"https://anitube.in.ua/frren.html",
	} {
		cards, err := parseCards([]byte(`<article class="story"><h2 itemprop="name"><a href="` + href + `">Назва</a></h2></article>`))
		if err != nil {
			t.Fatalf("parseCards(%q): %v", href, err)
		}
		if len(cards) != 0 {
			t.Errorf("parseCards(%q) = %+v, очікував порожньо", href, cards)
		}
	}
}

// Назва зі сторінки — недовірений текст: ESC-послідовність у ній перемалювала б термінал.
func TestCardNameIsCleaned(t *testing.T) {
	const html = `<article class="story"><h2 itemprop="name">` +
		"<a href=\"https://anitube.in.ua/1-x.html\">Тайтл\x1b[2J</a></h2></article>"
	cards, err := parseCards([]byte(html))
	if err != nil {
		t.Fatalf("parseCards: %v", err)
	}
	if len(cards) != 1 || cards[0].Name != "Тайтл" {
		t.Fatalf("cards = %+v, очікував назву %q", cards, "Тайтл")
	}
}

func TestRefFromSlug(t *testing.T) {
	ref, err := RefFromSlug("4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon")
	if err != nil {
		t.Fatalf("RefFromSlug: %v", err)
	}
	if ref.URL != baseURL+"/4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon.html" {
		t.Errorf("URL = %q", ref.URL)
	}
	for _, slug := range []string{"", "../x", "4465-a/b", "abc"} {
		if _, err := RefFromSlug(slug); !errors.Is(err, errs.ErrProvider) {
			t.Errorf("RefFromSlug(%q) error = %v, очікував ErrProvider", slug, err)
		}
	}
}

// Джерела фетчаться за слагом: URL із library.json/кешу може бути чужим або порожнім.
func TestSourcesIgnoresRefURL(t *testing.T) {
	ref := CanonicalRef("title-flat-ova")
	hostile := provider.TitleRef{Provider: ref.Provider, Slug: ref.Slug, URL: "http://127.0.0.1:9200/evil.html"}
	sources, err := fixtureClient().Sources(t.Context(), hostile, 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	if sources[0].Referer != ref.URL {
		t.Errorf("Referer = %q, очікував %q", sources[0].Referer, ref.URL)
	}
}

func TestSourcesInvalidSlugIsProviderError(t *testing.T) {
	_, err := fixtureClient().Sources(t.Context(), provider.TitleRef{Provider: providerID, Slug: "../x"}, 1)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Sources error = %v, очікував ErrProvider", err)
	}
}
