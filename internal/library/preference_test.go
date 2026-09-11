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

func TestReleaseChoices(t *testing.T) {
	sources := []provider.Source{
		src("Ясен", provider.KindSub),
		src("Альфа", provider.KindSub),
		src("Ясен", provider.KindVoiceover),
		src("Альфа", provider.KindMulti),
		src("Ясен", provider.KindDub),
		src("Ясен", provider.KindDub), // дубль пари — один рядок
	}

	got := ReleaseChoices(sources)
	// Одна студія з озвученням і субтитрами дає ДВА рядки: пара — одиниця вибору.
	want := []provider.Source{
		src("Ясен", provider.KindDub),
		src("Ясен", provider.KindVoiceover),
		src("Альфа", provider.KindMulti),
		src("Альфа", provider.KindSub),
		src("Ясен", provider.KindSub),
	}
	if len(got) != len(want) {
		t.Fatalf("ReleaseChoices = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ReleaseChoices[%d] = %+v, want %+v", i, got[i], want[i])
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

func TestPinSubVetoCountsMulti(t *testing.T) {
	// Вето на тихе зниження до сабів має бачити multi: за kindRank він перед sub.
	mixed := []provider.Source{
		src("A", provider.KindSub),
		src("B", provider.KindMulti),
	}
	t.Run("title studio pin", func(t *testing.T) {
		chosen, _ := Pick(mixed, Pin{Studio: "A"}, Prefs{})
		if chosen == nil || chosen.Studio != "B" || chosen.Kind != provider.KindMulti {
			t.Fatalf("Pick = %+v, want B/multi", chosen)
		}
	})
	t.Run("favorite studio", func(t *testing.T) {
		chosen, _ := Pick(mixed, Pin{}, Prefs{FavoriteStudio: "A"})
		if chosen == nil || chosen.Studio != "B" || chosen.Kind != provider.KindMulti {
			t.Fatalf("Pick = %+v, want B/multi", chosen)
		}
	})
}

func TestExplicitSubPinIsHonoured(t *testing.T) {
	both := []provider.Source{
		src("A", provider.KindVoiceover),
		src("A", provider.KindSub),
		src("B", provider.KindDub),
	}
	t.Run("явний sub-пін грає саби при наявному озвученні", func(t *testing.T) {
		chosen, _ := Pick(both, Pin{Studio: "A", Kind: provider.KindSub}, Prefs{})
		if chosen == nil || chosen.Studio != "A" || chosen.Kind != provider.KindSub {
			t.Fatalf("Pick = %+v, want A/sub", chosen)
		}
		if d := DeviationOf(Pin{Studio: "A", Kind: provider.KindSub}, *chosen); d != DeviationNone {
			t.Fatalf("Deviation = %v, want None", d)
		}
	})
	t.Run("wildcard-пін — озвучення тієї ж студії", func(t *testing.T) {
		chosen, _ := Pick(both, Pin{Studio: "A"}, Prefs{})
		if chosen == nil || chosen.Studio != "A" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("Pick = %+v, want A/voiceover", chosen)
		}
	})
	t.Run("wildcard-пін з глобальним prefer_kind=sub — саби", func(t *testing.T) {
		chosen, _ := Pick(both, Pin{Studio: "A"}, Prefs{PreferKind: provider.KindSub})
		if chosen == nil || chosen.Studio != "A" || chosen.Kind != provider.KindSub {
			t.Fatalf("Pick = %+v, want A/sub", chosen)
		}
	})
	t.Run("явний sub-пін без сабів — озвучення тієї ж студії", func(t *testing.T) {
		voicedOnly := []provider.Source{src("A", provider.KindVoiceover), src("B", provider.KindSub)}
		pin := Pin{Studio: "A", Kind: provider.KindSub}
		chosen, _ := Pick(voicedOnly, pin, Prefs{})
		if chosen == nil || chosen.Studio != "A" || chosen.Kind != provider.KindVoiceover {
			t.Fatalf("Pick = %+v, want A/voiceover", chosen)
		}
		if d := DeviationOf(pin, *chosen); d != DeviationKind {
			t.Fatalf("Deviation = %v, want Kind", d)
		}
	})
}

func TestDeviationOf(t *testing.T) {
	cases := []struct {
		name   string
		pin    Pin
		chosen provider.Source
		want   Deviation
	}{
		{"без піна", Pin{}, src("A", provider.KindSub), DeviationNone},
		{"точна пара", Pin{Studio: "A", Kind: provider.KindDub}, src("A", provider.KindDub), DeviationNone},
		{"dub→voiceover", Pin{Studio: "A", Kind: provider.KindDub}, src("A", provider.KindVoiceover), DeviationKind},
		{"voiceover→sub", Pin{Studio: "A", Kind: provider.KindVoiceover}, src("A", provider.KindSub), DeviationKind},
		{"wildcard і озвучення", Pin{Studio: "A"}, src("A", provider.KindVoiceover), DeviationNone},
		{"wildcard і sub", Pin{Studio: "A"}, src("A", provider.KindSub), DeviationKind},
		{"інша студія", Pin{Studio: "A", Kind: provider.KindDub}, src("B", provider.KindDub), DeviationStudio},
	}
	for _, tc := range cases {
		if got := DeviationOf(tc.pin, tc.chosen); got != tc.want {
			t.Errorf("%s: DeviationOf = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWantsSub(t *testing.T) {
	if WantsSub(Pin{}, Prefs{}) || WantsSub(Pin{Studio: "A"}, Prefs{PreferKind: provider.KindDub}) {
		t.Fatal("без явного вибору сабів WantsSub має бути false")
	}
	if !WantsSub(Pin{Studio: "A", Kind: provider.KindSub}, Prefs{}) || !WantsSub(Pin{}, Prefs{PreferKind: provider.KindSub}) {
		t.Fatal("явний пін або глобальна перевага — WantsSub")
	}
}

func TestPinKindFallsBackWithinStudio(t *testing.T) {
	// Пін (A, voiceover) на серії, де в A лише саби, а озвучення немає ніде:
	// граємо саби A, і це відхилення типу — сигнал для статусу.
	subsOnly := []provider.Source{src("A", provider.KindSub)}
	pin := Pin{Studio: "A", Kind: provider.KindVoiceover}
	chosen, _ := Pick(subsOnly, pin, Prefs{})
	if chosen == nil || chosen.Kind != provider.KindSub {
		t.Fatalf("Pick = %+v, want A/sub", chosen)
	}
	if d := DeviationOf(pin, *chosen); d != DeviationKind {
		t.Fatalf("Deviation = %v, want Kind", d)
	}
	// А з озвученням іншої студії — пін поступається їй (правило 4).
	withOther := []provider.Source{subsOnly[0], src("B", provider.KindVoiceover)}
	chosen, _ = Pick(withOther, pin, Prefs{})
	if chosen == nil || chosen.Studio != "B" {
		t.Fatalf("Pick = %+v, want B/voiceover", chosen)
	}
	if d := DeviationOf(pin, *chosen); d != DeviationStudio {
		t.Fatalf("Deviation = %v, want Studio", d)
	}
}

func TestSourcesFromReleasesAndCoverage(t *testing.T) {
	eps := []provider.Episode{
		{Number: 1, Releases: []provider.Release{{Studio: "A", Kind: provider.KindVoiceover}, {Studio: "A", Kind: provider.KindSub}}},
		{Number: 1, Releases: []provider.Release{{Studio: "A", Kind: provider.KindVoiceover}}}, // дубль номера
		{Number: 2, Releases: []provider.Release{{Studio: "A", Kind: provider.KindSub}, {Studio: "", Kind: provider.KindDub}}},
	}
	got := SourcesFromReleases(eps[2])
	if len(got) != 1 || got[0].Episode != 2 || got[0].Studio != "A" || got[0].Kind != provider.KindSub {
		t.Fatalf("SourcesFromReleases = %+v", got)
	}
	coverage, total := ReleaseCoverage(eps)
	if total != 2 {
		t.Fatalf("total = %d, want 2 (унікальні номери)", total)
	}
	if coverage[provider.Release{Studio: "A", Kind: provider.KindVoiceover}] != 1 ||
		coverage[provider.Release{Studio: "A", Kind: provider.KindSub}] != 2 {
		t.Fatalf("coverage = %+v", coverage)
	}
}
