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
		chosen, _ := Pick(all, Pin{Studio: "Glass Moon"}, Prefs{FavoriteStudio: "Unimay"})
		if chosen == nil || chosen.Studio != "Glass Moon" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("улюблена студія — другий пріоритет", func(t *testing.T) {
		chosen, _ := Pick(all, Pin{}, Prefs{FavoriteStudio: "FanVoxUA"})
		if chosen == nil || chosen.Studio != "FanVoxUA" {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("перевага типу dub", func(t *testing.T) {
		chosen, _ := Pick(all, Pin{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindDub {
			t.Fatalf("очікував дубляж, отримав %+v", chosen)
		}
	})

	t.Run("немає dub — будь-яке озвучення, не sub", func(t *testing.T) {
		voiced := []provider.Source{
			src("Glass Moon", provider.KindSub),
			src("FanVoxUA", provider.KindVoiceover),
		}
		chosen, _ := Pick(voiced, Pin{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("НІКОЛИ не деградуємо до sub за наявності озвучення; отримав %+v", chosen)
		}
	})

	t.Run("multi перед sub", func(t *testing.T) {
		s := []provider.Source{
			src("A", provider.KindSub),
			src("B", provider.KindMulti),
		}
		chosen, _ := Pick(s, Pin{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindMulti {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run("лише sub — граємо sub", func(t *testing.T) {
		s := []provider.Source{src("A", provider.KindSub)}
		chosen, _ := Pick(s, Pin{}, Prefs{})
		if chosen == nil || chosen.Kind != provider.KindSub {
			t.Fatalf("отримав %+v", chosen)
		}
	})

	t.Run(">1 студії без піна — кандидати на одне питання", func(t *testing.T) {
		chosen, cands := Pick(all, Pin{}, Prefs{PreferKind: provider.KindVoiceover})
		if chosen == nil || len(cands) < 2 {
			t.Fatalf("очікував кандидатів для питання, отримав chosen=%+v cands=%v", chosen, cands)
		}
		// вибір детермінований навіть без відповіді
		if chosen.Studio != "FanVoxUA" {
			t.Errorf("недетермінований вибір: %+v", chosen)
		}
	})

	t.Run("пін студії, якої більше немає — падаємо в загальний порядок", func(t *testing.T) {
		chosen, _ := Pick(all, Pin{Studio: "Зникла Студія"}, Prefs{})
		if chosen == nil || chosen.Kind == provider.KindSub {
			t.Fatalf("отримав %+v", chosen)
		}
	})
}

func TestPickEmpty(t *testing.T) {
	if c, _ := Pick(nil, Pin{}, Prefs{}); c != nil {
		t.Fatalf("очікував nil, отримав %+v", c)
	}
}

func TestStudioChoices(t *testing.T) {
	sources := []provider.Source{
		src("Ясен", provider.KindSub),
		src("Альфа", provider.KindSub),
		src("Ясен", provider.KindVoiceover),
		src("Альфа", provider.KindMulti),
		src("Ясен", provider.KindDub),
	}

	got := StudioChoices(sources)
	want := []provider.Source{
		src("Альфа", provider.KindMulti),
		src("Ясен", provider.KindDub),
	}
	if len(got) != len(want) {
		t.Fatalf("StudioChoices = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StudioChoices[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPinNeverDowngradesToSub(t *testing.T) {
	voiced := []provider.Source{
		src("A", provider.KindSub),
		src("B", provider.KindVoiceover),
	}

	t.Run("title studio pin", func(t *testing.T) {
		chosen, _ := Pick(voiced, Pin{Studio: "A"}, Prefs{})
		if chosen == nil || chosen.Studio != "B" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("Pick = %+v, want B/voiceover", chosen)
		}
	})

	t.Run("favorite studio", func(t *testing.T) {
		chosen, _ := Pick(voiced, Pin{}, Prefs{FavoriteStudio: "A"})
		if chosen == nil || chosen.Studio != "B" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("Pick = %+v, want B/voiceover", chosen)
		}
	})

	t.Run("pin holds when only subtitles exist", func(t *testing.T) {
		subs := []provider.Source{
			src("A", provider.KindSub),
			src("B", provider.KindSub),
		}
		chosen, _ := Pick(subs, Pin{Studio: "A"}, Prefs{})
		if chosen == nil || chosen.Studio != "A" || chosen.Kind != provider.KindSub {
			t.Fatalf("Pick = %+v, want A/sub", chosen)
		}
	})
}
