package provider

import (
	"reflect"
	"testing"
	"unicode"
)

func TestCleanText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"escape-послідовність", "Фрірен\x1b[2J", "Фрірен"},
		{"escape усередині", "Фрі\x1b[31mрен", "Фрірен"},
		{"C1 CSI без ESC", "Фрі\u009b31mрен", "Фрірен"},
		{"OSC із BEL", "a\x1b]0;titleа\x07b", "ab"},
		{"C0 і DEL", "a\x00\x7fb", "ab"},
		{"C1", "a\u009bmb", "ab"},
		{"пробіли стискаються", " a \n\t b ", "a b"},
		{"NBSP", "a\u00a0\u00a0b", "a b"},
		{"чистий текст без змін", "Фрірен, що проводжає в останню путь (1 сезон)", "Фрірен, що проводжає в останню путь (1 сезон)"},
		{"порожній", "", ""},
		{"обірваний ESC", "abc\x1b", "abc"},
		{"обірваний CSI", "abc\x1b[31", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanText(tc.in); got != tc.want {
				t.Errorf("CleanText(%q) = %q, очікував %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTextLeavesNoControls(t *testing.T) {
	for _, r := range CleanText("Ім'я\x1b[2J\u009b\x7f\x00студії") {
		if unicode.IsControl(r) {
			t.Errorf("керуючий символ %U лишився після CleanText", r)
		}
	}
}

func TestValidSlug(t *testing.T) {
	valid := []string{
		"4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon",
		"1917-na_by-proti-titanv-ova",
	}
	for _, slug := range valid {
		if !ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = false, очікував true", slug)
		}
	}
	invalid := []string{"", "../x", "4465-фрірен", "4465-a/b", "abc-def", "4465-", "4465", "4465-a\n"}
	for _, slug := range invalid {
		if ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = true, очікував false", slug)
		}
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range []Kind{KindDub, KindVoiceover, KindSub, KindMulti} {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) = false", k)
		}
	}
	for _, k := range []Kind{"", "dubbed", Kind("\x1b[2J")} {
		if ValidKind(k) {
			t.Errorf("ValidKind(%q) = true", k)
		}
	}
}

func TestCleanCard(t *testing.T) {
	card := TitleCard{
		TitleRef: TitleRef{
			Provider: "anitube",
			Slug:     "4465-frren",
			Name:     "  Фрірен\x1b[2J  ",
			URL:      "http://127.0.0.1/evil.html",
		},
		Episodes: "28 з 28",
		Genres:   []string{"пригоди\x00", "  ", "фентезі"},
		Studios:  []string{"Unimay\u009b"},
	}
	got, ok := CleanCard(card)
	if !ok {
		t.Fatal("CleanCard: очікував ok=true")
	}
	if got.Name != "Фрірен" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.URL != "" {
		t.Errorf("URL = %q, очікував порожній", got.URL)
	}
	if got.Episodes != "28 з 28" {
		t.Errorf("Episodes = %q", got.Episodes)
	}
	if !reflect.DeepEqual(got.Genres, []string{"пригоди", "фентезі"}) {
		t.Errorf("Genres = %q", got.Genres)
	}
	if !reflect.DeepEqual(got.Studios, []string{"Unimay"}) {
		t.Errorf("Studios = %q", got.Studios)
	}
}

func TestCleanCardsDropsInvalid(t *testing.T) {
	cards := []TitleCard{
		{TitleRef: TitleRef{Provider: "anitube", Slug: "../x", Name: "traversal"}},
		{TitleRef: TitleRef{Provider: "anitube", Slug: "4465-frren", Name: "\x1b"}},
		{TitleRef: TitleRef{Provider: "", Slug: "4465-frren", Name: "без провайдера"}},
		{TitleRef: TitleRef{Provider: "anitube", Slug: "4465-frren", Name: "Фрірен"}},
	}
	got := CleanCards(cards)
	if len(got) != 1 || got[0].Name != "Фрірен" {
		t.Fatalf("CleanCards = %+v, очікував лише валідну картку", got)
	}
}

func TestCleanEpisodes(t *testing.T) {
	eps := []Episode{
		{Number: 0, Releases: []Release{{Studio: "X", Kind: KindDub}}},
		{Number: 1, Releases: []Release{
			{Studio: "Uni\x1b[2Jmay", Kind: Kind("\x1b[2J")},
			{Studio: "\x00", Kind: KindDub},
			{Studio: "Glass Moon", Kind: KindSub},
		}},
	}
	got := CleanEpisodes(eps)
	// «0 серія» не фільтрується: кеш має збігатися з живим списком парсера.
	if len(got) != 2 || got[0].Number != 0 || got[1].Number != 1 {
		t.Fatalf("CleanEpisodes = %+v, очікував серії 0 і 1", got)
	}
	// Студія з самих керуючих символів стає порожньою, але реліз лишається.
	want := []Release{{Studio: "Unimay", Kind: KindMulti}, {Studio: "", Kind: KindDub}, {Studio: "Glass Moon", Kind: KindSub}}
	if !reflect.DeepEqual(got[1].Releases, want) {
		t.Errorf("Releases = %+v, очікував %+v", got[0].Releases, want)
	}
}

// Ключ — лише для порівняння: CleanText (і показ) лишають написання сайту.
func TestStudioKeyDoesNotChangeDisplay(t *testing.T) {
	const site = "FanVoxU\u0410"
	if CleanText(site) != site {
		t.Fatalf("CleanText змінив назву студії: %q", CleanText(site))
	}
	if StudioKey(site) != "fanvoxua" {
		t.Fatalf("StudioKey(%q) = %q", site, StudioKey(site))
	}
	if !SameRelease(Release{Studio: "FanVoxUA", Kind: KindDub}, Release{Studio: site, Kind: KindDub}) ||
		SameRelease(Release{Studio: "FanVoxUA", Kind: KindDub}, Release{Studio: site, Kind: KindSub}) {
		t.Fatal("SameRelease: студія за ключем, тип — точно")
	}
}

func TestCleanTextDropsBidiControls(t *testing.T) {
	// RLO та ізолятор переставили б увесь рядок списку; ZWJ і м'який перенос — ні.
	got := CleanText("Фрі\u202eрен\u2066 \u200dТест\u00ad")
	if want := "Фрірен \u200dТест\u00ad"; got != want {
		t.Fatalf("CleanText = %q, очікував %q", got, want)
	}
}

func TestStudioKey(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		same bool
	}{
		{"FanVoxUA", "FANVOXUA", true},
		{" Рідний  Голос ", "рідний голос", true},
		{"Glass\x1b[2JMoon", "glassmoon", true},
		{"FanVoxUA", "FanVox", false},
		// Кириличні двійники латинських літер (етап 4.2): «А» в кінці.
		{"FanVoxUA", "FanVoxU\u0410", true},
		{"fanvoxua", "FANVOXU\u0410", true},
		{"\u0412\u0415\u0420\u0422\u041e", "BEPTO", true}, // ВЕРТО ↔ BEPTO
		// Ключ не залежить від регістру й для кирилиці: «Т», «К», «Н» мають
		// двійника лише у великій формі, але малі зводяться так само.
		{"ТЕРМІНАЛ", "термінал", true},
		{"КАМІНЬ", "камінь", true},
		{"Рідний Голос", "РІДНИЙ ГОЛОС", true},
		// Без двійника літери лишаються собою.
		{"Ґлянець", "Glanec", false},
		{"Рідний Голос", "Ридний Голос", false},
	} {
		if got := SameStudio(tt.a, tt.b); got != tt.same {
			t.Errorf("SameStudio(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.same)
		}
	}
}
