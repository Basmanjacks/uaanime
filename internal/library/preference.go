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

// Pick — серце продукту: вибір релізу для серії. Порядок розв'язання:
//
//  1. закріплена студія тайтлу (StudioPin, з урахуванням KindPin)
//  2. глобальна улюблена студія
//  3. глобальна перевага типу (dub за замовчуванням)
//  4. будь-яке інше українське озвучення (dub/voiceover, потім multi —
//     невідомий тип імовірніше озвучення, ніж саби)
//  5. українські субтитри
//
// Жорстке правило: якщо доступне будь-яке українське озвучення — на субтитри
// не перемикаємо. Ніколи.
//
// Повертає обране джерело і candidates: якщо на переможному ярусі лишилося
// кілька студій і жоден пін/улюблена не вирішили — інтерфейс має спитати
// ОДИН раз і закріпити вибір (питати повторно за той самий тайтл — баг).
// chosen при цьому детермінований (перша студія за абеткою), щоб headless-режим
// працював без інтерактиву.
func Pick(sources []provider.Source, e *Entry, p Prefs) (chosen *provider.Source, candidates []provider.Source) {
	if len(sources) == 0 {
		return nil, nil
	}
	if p.PreferKind == "" {
		p.PreferKind = provider.KindDub
	}

	// 1–2: студійні пріоритети.
	if e != nil && e.StudioPin != "" {
		if s := bestOfStudio(sources, e.StudioPin, e.KindPin); s != nil {
			return s, nil
		}
	}
	if p.FavoriteStudio != "" {
		if s := bestOfStudio(sources, p.FavoriteStudio, ""); s != nil {
			return s, nil
		}
	}

	// 3–5: яруси типів. Перший непорожній ярус перемагає.
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

// bestOfStudio: джерела студії, озвучення попереду; kindPin звужує вибір,
// але його відсутність у релізах не скасовує пін студії.
func bestOfStudio(sources []provider.Source, studio string, kindPin provider.Kind) *provider.Source {
	var all []provider.Source
	for _, s := range sources {
		if s.Studio != studio {
			continue
		}
		if kindPin != "" && s.Kind == kindPin {
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

func countStudios(sources []provider.Source) int {
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s.Studio] = true
	}
	return len(seen)
}
