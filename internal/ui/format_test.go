package ui

import (
	"testing"
	"time"

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
