package library

import (
	"sort"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Prefs — глобальні переваги користувача (з config.json).
type Prefs struct {
	FavoriteStudio string
	PreferKind     provider.Kind // за замовчуванням dub
}

// Pin — закріплення тайтлу значеннями, а не покажчиком на Entry: Pick
// викликається з фонової горутини, якій заборонено читати пам'ять Library.
// Нульове значення означає «піна немає».
//
// Kind == "" — wildcard: «ця студія, тип за глобальною перевагою». Kind == sub
// буває лише з явного вибору в пікері: неявний пін після першого перегляду
// (playback.Begin) субтитри не записує, а старі бібліотеки мігруються в
// Normalize.
type Pin struct {
	Studio string
	Kind   provider.Kind
}

// Deviation — чим обране джерело відрізняється від піна. Рахується окремо від
// Pick (DeviationOf), щоб фоновий резолв і екрани рахували одне й те саме.
type Deviation uint8

const (
	DeviationNone   Deviation = iota
	DeviationKind             // та сама студія, інший тип (або wildcard-пін і обрано sub)
	DeviationStudio           // інша студія
)

// DeviationOf порівнює вибір із піном. Без піна відхилення немає за
// визначенням; «wildcard і sub» — відхилення, бо wildcard обіцяє озвучення.
func DeviationOf(pin Pin, chosen provider.Source) Deviation {
	if pin.Studio == "" {
		return DeviationNone
	}
	if chosen.Studio != pin.Studio {
		return DeviationStudio
	}
	if pin.Kind != "" {
		if chosen.Kind != pin.Kind {
			return DeviationKind
		}
		return DeviationNone
	}
	if chosen.Kind == provider.KindSub {
		return DeviationKind
	}
	return DeviationNone
}

// WantsSub — користувач сам хоче субтитри: явний пін або глобальна перевага.
// Усі попередження «грають субтитри» мають форму chosen.Kind == sub && !WantsSub:
// свідомий вибір сабів ніколи не показується як fallback.
func WantsSub(pin Pin, p Prefs) bool {
	return pin.Kind == provider.KindSub || p.PreferKind == provider.KindSub
}

// Pick — серце продукту: вибір релізу для серії. Порядок розв'язання:
//
//  1. точна пара піна (студія, тип), якщо вона є в серії
//  2. студія піна з іншим типом: wildcard бере глобальну перевагу типу, інакше
//     найкращий за kindRank; субтитри — лише коли в серії немає жодного
//     не-sub релізу (dub/voiceover/multi) або користувач хоче саби (WantsSub)
//  3. глобальна улюблена студія (як 2, без типу)
//  4. глобальна перевага типу (dub за замовчуванням)
//  5. будь-яке інше українське озвучення (dub/voiceover), потім multi —
//     невідомий тип імовірніше озвучення, ніж саби
//  6. українські субтитри
//
// Жорстке правило: пріоритет студії ніколи не може ТИХО знизити вибір до
// субтитрів, поки доступний будь-який не-sub реліз. Явний пін на субтитри —
// рішення користувача, а не тихе зниження, тому він поважається.
//
// Повертає обране джерело і candidates: якщо на переможному ярусі лишилося
// кілька студій і жоден пін/улюблена не вирішили — інтерфейс має спитати
// ОДИН раз і закріпити вибір (питати повторно за той самий тайтл — баг).
// chosen при цьому детермінований (перша студія за абеткою), щоб headless-режим
// працював без інтерактиву.
func Pick(sources []provider.Source, pin Pin, p Prefs) (chosen *provider.Source, candidates []provider.Source) {
	if len(sources) == 0 {
		return nil, nil
	}
	if p.PreferKind == "" {
		p.PreferKind = provider.KindDub
	}
	wantsSub := WantsSub(pin, p)

	// 1–3: студійні пріоритети.
	if pin.Studio != "" {
		if s := studioPick(sources, pin.Studio, pin.Kind, p.PreferKind, wantsSub); s != nil {
			return s, nil
		}
	}
	if p.FavoriteStudio != "" {
		if s := studioPick(sources, p.FavoriteStudio, "", p.PreferKind, wantsSub); s != nil {
			return s, nil
		}
	}

	// 4–6: яруси типів. Перший непорожній ярус перемагає.
	tiers := [][]provider.Source{
		filterKind(sources, p.PreferKind),
		filterVoiced(sources),
		filterKind(sources, provider.KindMulti),
		filterKind(sources, provider.KindSub),
	}
	for _, tier := range tiers {
		if len(tier) == 0 {
			continue
		}
		sort.SliceStable(tier, func(i, j int) bool { return tier[i].Studio < tier[j].Studio })
		if countStudios(tier) > 1 {
			return &tier[0], tier
		}
		return &tier[0], nil
	}
	return nil, nil
}

// ReleaseChoices — по одному джерелу на пару (студія, тип): озвучення попереду,
// в межах типу за абеткою студій. Це одиниця вибору в пікері: одна студія з
// озвученням і субтитрами дає два рядки, а не один «найкращий».
func ReleaseChoices(sources []provider.Source) []provider.Source {
	seen := make(map[provider.Release]bool, len(sources))
	choices := make([]provider.Source, 0, len(sources))
	for _, source := range sources {
		key := provider.Release{Studio: source.Studio, Kind: source.Kind}
		if seen[key] {
			continue
		}
		seen[key] = true
		choices = append(choices, source)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		a, b := choices[i], choices[j]
		if kindRank(a.Kind) != kindRank(b.Kind) {
			return kindRank(a.Kind) < kindRank(b.Kind)
		}
		return a.Studio < b.Studio
	})
	return choices
}

// SourcesFromReleases — джерела лише з метаданих серії (без embed): досить,
// щоб Pick сказав, ЩО гратиме, без мережі. Реальний резолв може відхилити
// пару без екстрактора — це вже покриває статус після запуску.
func SourcesFromReleases(ep provider.Episode) []provider.Source {
	releases := provider.CleanEpisode(ep).Releases
	out := make([]provider.Source, 0, len(releases))
	for _, r := range releases {
		out = append(out, provider.Source{Episode: ep.Number, Studio: r.Studio, Kind: r.Kind})
	}
	return out
}

// ReleaseCoverage — у скількох серіях є кожна пара (студія, тип) і скільки
// серій усього. Рахується по унікальних номерах, як і StatusOf.
func ReleaseCoverage(episodes []provider.Episode) (map[provider.Release]int, int) {
	coverage := map[provider.Release]int{}
	seenNumbers := map[int]bool{}
	for _, ep := range episodes {
		if seenNumbers[ep.Number] {
			continue
		}
		seenNumbers[ep.Number] = true
		counted := map[provider.Release]bool{}
		for _, r := range provider.CleanEpisode(ep).Releases {
			if counted[r] {
				continue
			}
			counted[r] = true
			coverage[r]++
		}
	}
	return coverage, len(seenNumbers)
}

// studioPick — вибір у межах студії з вето на тихе зниження до субтитрів.
func studioPick(sources []provider.Source, studio string, kindPin, preferKind provider.Kind, wantsSub bool) *provider.Source {
	chosen := bestOfStudio(sources, studio, kindPin, preferKind)
	if chosen != nil && chosen.Kind == provider.KindSub && !wantsSub && hasNonSub(sources) {
		return nil
	}
	return chosen
}

// bestOfStudio: джерела студії; точний збіг бажаного типу (пін, а для
// wildcard — глобальна перевага) перемагає, інакше найкращий за kindRank.
// Відсутність бажаного типу в релізах не скасовує пін студії.
func bestOfStudio(sources []provider.Source, studio string, kindPin, preferKind provider.Kind) *provider.Source {
	want := kindPin
	if want == "" {
		want = preferKind
	}
	var all []provider.Source
	for _, s := range sources {
		if s.Studio != studio {
			continue
		}
		if want != "" && s.Kind == want {
			s := s
			return &s
		}
		all = append(all, s)
	}
	if len(all) == 0 {
		return nil
	}
	sort.SliceStable(all, func(i, j int) bool { return kindRank(all[i].Kind) < kindRank(all[j].Kind) })
	return &all[0]
}

func kindRank(k provider.Kind) int {
	switch k {
	case provider.KindDub:
		return 0
	case provider.KindVoiceover:
		return 1
	case provider.KindMulti:
		return 2
	default: // sub — останній завжди
		return 3
	}
}

func filterKind(sources []provider.Source, k provider.Kind) []provider.Source {
	var out []provider.Source
	for _, s := range sources {
		if s.Kind == k {
			out = append(out, s)
		}
	}
	return out
}

func filterVoiced(sources []provider.Source) []provider.Source {
	var out []provider.Source
	for _, s := range sources {
		if s.Kind == provider.KindDub || s.Kind == provider.KindVoiceover {
			out = append(out, s)
		}
	}
	return out
}

// hasNonSub — чи є в серії хоч один реліз, що не є субтитрами (multi теж
// рахується: за kindRank він стоїть перед sub).
func hasNonSub(sources []provider.Source) bool {
	for _, s := range sources {
		if s.Kind != provider.KindSub {
			return true
		}
	}
	return false
}

func countStudios(sources []provider.Source) int {
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s.Studio] = true
	}
	return len(seen)
}
