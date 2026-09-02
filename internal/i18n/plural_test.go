package i18n

import "testing"

func TestUkrainianEpisodePlurals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n           int
		plural      string
		episodes    string
		newEpisodes string
	}{
		{n: 1, plural: "серія", episodes: "1 серія", newEpisodes: "+1 нова серія"},
		{n: 2, plural: "серії", episodes: "2 серії", newEpisodes: "+2 нові серії"},
		{n: 4, plural: "серії", episodes: "4 серії", newEpisodes: "+4 нові серії"},
		{n: 5, plural: "серій", episodes: "5 серій", newEpisodes: "+5 нових серій"},
		{n: 11, plural: "серій", episodes: "11 серій", newEpisodes: "+11 нових серій"},
		{n: 12, plural: "серій", episodes: "12 серій", newEpisodes: "+12 нових серій"},
		{n: 14, plural: "серій", episodes: "14 серій", newEpisodes: "+14 нових серій"},
		{n: 21, plural: "серія", episodes: "21 серія", newEpisodes: "+21 нова серія"},
		{n: 22, plural: "серії", episodes: "22 серії", newEpisodes: "+22 нові серії"},
		{n: 25, plural: "серій", episodes: "25 серій", newEpisodes: "+25 нових серій"},
		{n: 101, plural: "серія", episodes: "101 серія", newEpisodes: "+101 нова серія"},
		{n: 111, plural: "серій", episodes: "111 серій", newEpisodes: "+111 нових серій"},
	}

	for _, tt := range tests {
		t.Run(tt.episodes, func(t *testing.T) {
			if got := plural(tt.n, "серія", "серії", "серій"); got != tt.plural {
				t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.plural)
			}
			if got := Episodes(tt.n); got != tt.episodes {
				t.Errorf("Episodes(%d) = %q, want %q", tt.n, got, tt.episodes)
			}
			if got := NewEpisodes(tt.n); got != tt.newEpisodes {
				t.Errorf("NewEpisodes(%d) = %q, want %q", tt.n, got, tt.newEpisodes)
			}
		})
	}
}
