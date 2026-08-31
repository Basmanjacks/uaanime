package library

import (
	"testing"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func src(studio string, kind provider.Kind) provider.Source {
	return provider.Source{Studio: studio, Kind: kind, Embed: "e", Episode: 1}
}

func TestPickOrder(t *testing.T) {
	all := []provider.Source{
		src("FanVoxUA", provider.KindVoiceover),
		src("Glass Moon", provider.KindVoiceover),
		src("Unimay", provider.KindDub),
		src("Glass Moon", provider.KindSub),
	}

	t.Run("пін студії перемагає все", func(t *testing.T) {
		chosen, _ := Pick(all, &Entry{StudioPin: "Glass Moon"}, Prefs{FavoriteStudio: "Unimay"})
		if chosen == nil || chosen.Studio != "Glass Moon" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("улюблена студія — другий пріоритет", func(t *testing.T) {
		chosen, _ := Pick(all, &Entry{}, Prefs{FavoriteStudio: "FanVoxUA"})
		if chosen == nil || chosen.Studio != "FanVoxUA" {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("перевага типу dub", func(t *testing.T) {
		chosen, _ := Pick(all, &Entry{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindDub {
			t.Fatalf("очікував дубляж, отримав %+v", chosen)
		}
	})

	t.Run("немає dub — будь-яке озвучення, не sub", func(t *testing.T) {
		voiced := []provider.Source{
			src("Glass Moon", provider.KindSub),
			src("FanVoxUA", provider.KindVoiceover),
		}
		chosen, _ := Pick(voiced, &Entry{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("НІКОЛИ не деградуємо до sub за наявності озвучення; отримав %+v", chosen)
		}
	})

	t.Run("multi перед sub", func(t *testing.T) {
		s := []provider.Source{
			src("A", provider.KindSub),
			src("B", provider.KindMulti),
		}
		chosen, _ := Pick(s, &Entry{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindMulti {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("лише sub — граємо sub", func(t *testing.T) {
		s := []provider.Source{src("A", provider.KindSub)}
		chosen, _ := Pick(s, &Entry{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindSub {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run(">1 студії без піна — кандидати на одне питання", func(t *testing.T) {
		chosen, cands := Pick(all, &Entry{}, Prefs{PreferKind: provider.KindVoiceover})
		if chosen == nil || len(cands) < 2 {
			t.Fatalf("очікував кандидатів для питання, отримав chosen=%+v cands=%v", chosen, cands)
		}
		// вибір детермінований навіть без відповіді
		if chosen.Studio != "FanVoxUA" {
			t.Errorf("недетермінований вибір: %+v", chosen)
		}
	})

	t.Run("пін студії, якої більше немає — падаємо в загальний порядок", func(t *testing.T) {
		chosen, _ := Pick(all, &Entry{StudioPin: "Зникла Студія"}, Prefs{})
		if chosen == nil || chosen.Kind == provider.KindSub {
			t.Fatalf("отримав %+v", chosen)
		}
	})
}

func TestPickEmpty(t *testing.T) {
	if c, _ := Pick(nil, &Entry{}, Prefs{}); c != nil {
		t.Fatalf("очікував nil, отримав %+v", c)
	}
}
