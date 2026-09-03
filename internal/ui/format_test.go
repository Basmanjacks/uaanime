package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestCardMeta(t *testing.T) {
	tests := []struct {
		name string
		card provider.TitleCard
		want string
	}{
		{
			name: "full card",
			card: provider.TitleCard{
				TitleRef: provider.TitleRef{Name: "Фрірен"},
				Year:     2023,
				Episodes: "28 з 28",
				EpAired:  28,
				Rating:   9.5,
				HasDub:   true,
				HasSub:   true,
				Studios:  []string{"Studio1", "Studio2", "Studio3"},
			},
			want: "2023 · 28 з 28 · ★ 9.5 · Дуб+Саб · Studio1 · Studio2",
		},
		{
			name: "name only",
			card: provider.TitleCard{TitleRef: provider.TitleRef{Name: "Без метаданих"}},
			want: "",
		},
		{
			name: "aired count without human string",
			card: provider.TitleCard{Year: 2024, EpAired: 3, HasSub: true},
			want: "2024 · 3 серії · Саб",
		},
		{
			name: "dub only",
			card: provider.TitleCard{Rating: 8, HasDub: true},
			want: "★ 8.0 · Дуб",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := cardMeta(test.card); got != test.want {
				t.Fatalf("cardMeta() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCardMetaParts(t *testing.T) {
	tests := []struct {
		name string
		card provider.TitleCard
		want []metaPart
	}{
		{
			name: "kinds and order",
			card: provider.TitleCard{
				Year:     2023,
				Episodes: "28 з 28",
				Rating:   9.5,
				HasDub:   true,
				HasSub:   true,
				Studios:  []string{"Studio1", "Studio2", "Studio3"},
			},
			want: []metaPart{
				{text: "2023", kind: metaYear},
				{text: "28 з 28", kind: metaCount},
				{text: "★ 9.5", kind: metaRating},
				{text: "Дуб+Саб", kind: metaKinds},
				{text: "Studio1", kind: metaStudio},
				{text: "Studio2", kind: metaStudio},
			},
		},
		{
			name: "empty fields skipped",
			card: provider.TitleCard{Studios: []string{"", "Studio1", "", "Studio2"}},
			want: []metaPart{
				{text: "Studio1", kind: metaStudio},
				{text: "Studio2", kind: metaStudio},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := cardMetaParts(test.card)
			if len(got) != len(test.want) {
				t.Fatalf("cardMetaParts() = %#v, want %#v", got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Errorf("cardMetaParts()[%d] = %#v, want %#v", i, got[i], test.want[i])
				}
			}
		})
	}
}

func TestHumanDate(t *testing.T) {
	location := time.FixedZone("Europe/Kyiv", 3*60*60)
	now := time.Date(2026, time.August, 31, 21, 40, 0, 0, location)

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "same day", at: time.Date(2026, time.August, 31, 15, 4, 0, 0, location), want: "сьогодні 15:04"},
		{name: "yesterday", at: time.Date(2026, time.August, 30, 9, 7, 0, 0, location), want: "вчора 09:07"},
		{name: "same year older", at: time.Date(2026, time.January, 2, 15, 4, 0, 0, location), want: "02.01"},
		{name: "previous year", at: time.Date(2025, time.December, 31, 15, 4, 0, 0, location), want: "31.12.2025"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := humanDate(test.at, now); got != test.want {
				t.Fatalf("humanDate(%v, %v) = %q, want %q", test.at, now, got, test.want)
			}
		})
	}
}

// Українська множина в заголовку: 1 — однина, 2–4 — форма для кількох,
// решта — множина; 11 попри «1» на кінці — множина.
func TestRemainingEpisodesPlural(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{
		{1, "залишилась 1 серія"},
		{2, "залишилось 2 серії"},
		{5, "залишилось 5 серій"},
		{11, "залишилось 11 серій"},
		{21, "залишилась 21 серія"},
	} {
		if got := i18n.RemainingEpisodes(tc.n); got != tc.want {
			t.Errorf("RemainingEpisodes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	for _, tc := range []struct {
		sec  float64
		want string
	}{
		{0, ""},
		{20, ""},
		{60, "1 хв"},
		{45 * 60, "45 хв"},
		{2*3600 + 45*60, "2 год 45 хв"},
		{3 * 3600, "3 год"},
	} {
		if got := i18n.HumanDuration(tc.sec); got != tc.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", tc.sec, got, tc.want)
		}
	}
}

func TestRemainingLabel(t *testing.T) {
	newModel := func(t *testing.T) Model {
		t.Helper()
		m := newTestModel(t)
		ref := testRefs("remaining", 1)[0]
		m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
		m.ref, m.episodesRef = ref, ref
		m.episodes = testEpisodes(12)
		return m
	}

	// без жодного семпла тривалості лишається сама кількість
	m := newModel(t)
	if got, want := m.remainingLabel(), i18n.RemainingEpisodes(12); got != want {
		t.Errorf("remainingLabel without samples = %q, want %q", got, want)
	}

	// дві переглянуті серії по 24 хв → 10 по 24 хв = 4 год
	m = newModel(t)
	for ep := 1; ep <= 2; ep++ {
		m.eng.Lib.Progress = append(m.eng.Lib.Progress, &library.Progress{
			TitleID: m.ref.Slug, Episode: ep, DurationSec: 1440, Completed: true,
		})
	}
	want := fmt.Sprintf(i18n.TuiRemainingFmt, i18n.RemainingEpisodes(10), "4 год")
	if got := m.remainingLabel(); got != want {
		t.Errorf("remainingLabel = %q, want %q", got, want)
	}

	// недодивлена серія лишається в залишку
	m.eng.Lib.Progress = append(m.eng.Lib.Progress, &library.Progress{
		TitleID: m.ref.Slug, Episode: 3, PositionSec: 300, DurationSec: 1440,
	})
	if got := m.remainingLabel(); got != want {
		t.Errorf("remainingLabel with a partial episode = %q, want %q", got, want)
	}

	// усе переглянуто — рядка немає
	m = newModel(t)
	for ep := 1; ep <= 12; ep++ {
		m.eng.Lib.Progress = append(m.eng.Lib.Progress, &library.Progress{
			TitleID: m.ref.Slug, Episode: ep, Completed: true,
		})
	}
	if got := m.remainingLabel(); got != "" {
		t.Errorf("remainingLabel for a finished title = %q, want empty", got)
	}

	// серій не знаємо зовсім — теж немає
	m = newModel(t)
	m.episodes, m.episodesRef = nil, provider.TitleRef{}
	if got := m.remainingLabel(); got != "" {
		t.Errorf("remainingLabel without episodes = %q, want empty", got)
	}
}
