package library

import (
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func numbered(nums ...int) []provider.Episode {
	out := make([]provider.Episode, 0, len(nums))
	for _, n := range nums {
		out = append(out, provider.Episode{Number: n})
	}
	return out
}

func watched(titleID string, nums ...int) []*Progress {
	out := make([]*Progress, 0, len(nums))
	for i, n := range nums {
		out = append(out, &Progress{
			TitleID:   titleID,
			Episode:   n,
			Completed: true,
			WatchedAt: time.Unix(int64(1000+i), 0),
		})
	}
	return out
}

func TestStatusOf(t *testing.T) {
	tests := []struct {
		name     string
		lib      *Library
		titleID  string
		episodes []provider.Episode
		want     Status
	}{
		{
			name:     "нічого не почато — у планах із повним списком",
			lib:      &Library{Entries: []*Entry{{TitleID: "t", KnownEpisodes: 3}}},
			titleID:  "t",
			episodes: eps(5),
			want:     Status{Kind: StatusPlanned, Total: 5, Remaining: 5, Fresh: 2},
		},
		{
			name: "частково переглянуто — залишок і новинки",
			lib: &Library{
				Entries:  []*Entry{{TitleID: "t", LastEpisode: 3, KnownEpisodes: 3}},
				Progress: watched("t", 1, 2, 3),
			},
			titleID:  "t",
			episodes: eps(5),
			want:     Status{Kind: StatusWatching, Total: 5, Remaining: 2, Fresh: 2},
		},
		{
			name: "усе наявне переглянуто — жодних новинок",
			lib: &Library{
				Entries:  []*Entry{{TitleID: "t", LastEpisode: 10, KnownEpisodes: 9}},
				Progress: watched("t", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10),
			},
			titleID:  "t",
			episodes: eps(10),
			want:     Status{Kind: StatusDone, Total: 10},
		},
		{
			name: "незавершена серія лишається в залишку",
			lib: &Library{
				Entries:  []*Entry{{TitleID: "t", LastEpisode: 2, KnownEpisodes: 2}},
				Progress: []*Progress{{TitleID: "t", Episode: 2, PositionSec: 60, DurationSec: 1440}},
			},
			titleID:  "t",
			episodes: eps(2),
			want:     Status{Kind: StatusWatching, Total: 2, Remaining: 2},
		},
		{
			name:     "списку серій немає — числа невідомі",
			lib:      &Library{Progress: watched("t", 1)},
			titleID:  "t",
			episodes: nil,
			want:     Status{Kind: StatusWatching},
		},
		{
			name:     "дубль номера рахується раз",
			lib:      &Library{},
			titleID:  "t",
			episodes: numbered(1, 3, 3),
			want:     Status{Kind: StatusPlanned, Total: 2, Remaining: 2, Fresh: 2},
		},
		{
			name:     "тайтла ще немає в бібліотеці",
			lib:      &Library{},
			titleID:  "",
			episodes: eps(4),
			want:     Status{Kind: StatusPlanned, Total: 4, Remaining: 4, Fresh: 4},
		},
		{
			name:     "нульова серія — звичайний номер",
			lib:      &Library{Entries: []*Entry{{TitleID: "t"}}, Progress: watched("t", 0)},
			titleID:  "t",
			episodes: numbered(0, 1),
			want:     Status{Kind: StatusWatching, Total: 2, Remaining: 1, Fresh: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lib.StatusOf(tt.titleID, tt.episodes); got != tt.want {
				t.Fatalf("StatusOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNextEpisodeAfterFindsZeroEpisode(t *testing.T) {
	episodes := numbered(0, 2, 5)

	if num, found := NextEpisodeAfter(episodes, -1); !found || num != 0 {
		t.Fatalf("NextEpisodeAfter(-1) = (%d, %v), очікував нульову серію", num, found)
	}
	if num, found := NextEpisodeAfter(episodes, 2); !found || num != 5 {
		t.Fatalf("NextEpisodeAfter(2) = (%d, %v)", num, found)
	}
	if _, found := NextEpisodeAfter(episodes, 5); found {
		t.Fatal("NextEpisodeAfter(5) знайшов серію після останньої")
	}
}

func TestResumeInSkipsNonExistentEpisode(t *testing.T) {
	// Рівно випадок «Вигнаного лицаря»: усі 10 наявних серій переглянуто,
	// пропонувати 11-ту нема на підставі чого.
	lib := &Library{
		Entries:  []*Entry{{TitleID: "t", LastEpisode: 10}},
		Progress: watched("t", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10),
	}

	if _, _, ok := lib.ResumeIn("t", eps(10)); ok {
		t.Fatal("ResumeIn запропонував серію після останньої наявної")
	}
	if got := lib.StatusOf("t", eps(10)).Kind; got != StatusDone {
		t.Fatalf("StatusOf = %v, інваріант «ok == false ⇔ Done» порушено", got)
	}
	// Вийшла нова серія — рядок повертається.
	if ep, _, ok := lib.ResumeIn("t", eps(11)); !ok || ep != 11 {
		t.Fatalf("ResumeIn після виходу серії = (%d, %v)", ep, ok)
	}
}

func TestResumeInOffersHoleBehind(t *testing.T) {
	// Ручні позначки лишили дірки: серія 10 переглянута, 1-9 — ні.
	lib := &Library{
		Entries:  []*Entry{{TitleID: "t", LastEpisode: 10}},
		Progress: watched("t", 10),
	}

	ep, pos, ok := lib.ResumeIn("t", eps(10))
	if !ok || ep != 1 || pos != 0 {
		t.Fatalf("ResumeIn = (%d, %v, %v), очікував найменшу непереглянуту", ep, pos, ok)
	}
	if got := lib.StatusOf("t", eps(10)); got.Kind != StatusWatching || got.Remaining != 9 {
		t.Fatalf("StatusOf = %+v", got)
	}
}

func TestResumeInWithoutEpisodeList(t *testing.T) {
	started := &Library{Progress: []*Progress{
		{TitleID: "t", Episode: 4, PositionSec: 300, DurationSec: 1440, WatchedAt: time.Unix(1000, 0)},
	}}
	for _, episodes := range [][]provider.Episode{nil, {}} {
		ep, pos, ok := started.ResumeIn("t", episodes)
		if !ok || ep != 4 || pos != 300 {
			t.Fatalf("ResumeIn без списку = (%d, %v, %v)", ep, pos, ok)
		}
	}

	// Остання серія завершена: наступну без списку вигадувати не можна.
	done := &Library{Progress: watched("t", 4)}
	if _, _, ok := done.ResumeIn("t", nil); ok {
		t.Fatal("ResumeIn без списку запропонував наступну серію")
	}
}
