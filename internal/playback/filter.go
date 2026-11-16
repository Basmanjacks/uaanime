package playback

import (
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// KindFilter — тип із `--kind`: один із provider.Kind, крім multi, або
// KindVoiced — «будь-яке озвучення» (dub чи voiceover).
type KindFilter string

const KindVoiced KindFilter = "voiced"

// ParseKindFilter приймає рівно dub|voiceover|sub|voiced.
func ParseKindFilter(s string) (KindFilter, bool) {
	switch k := KindFilter(s); k {
	case KindFilter(provider.KindDub), KindFilter(provider.KindVoiceover), KindFilter(provider.KindSub), KindVoiced:
		return k, true
	}
	return "", false
}

func (k KindFilter) matches(kind provider.Kind) bool {
	switch k {
	case "":
		return true
	case KindVoiced:
		return kind == provider.KindDub || kind == provider.KindVoiceover
	}
	return kind == provider.Kind(k)
}

// ReleaseFilter — явний вибір релізу з командного рядка (`--studio`,
// `--kind`) для одного запиту. Нуль — автоматичний вибір за library.Pick,
// рівно як без прапорців. Фільтр звужує кандидатів, а порядок серед них
// лишається за Pick; недоступний запитаний реліз — помилка, а не заміна.
// Фільтр нічого не пише: ані піна, ані бібліотеки, ані налаштувань.
type ReleaseFilter struct {
	Studio string
	Kind   KindFilter
}

func (f ReleaseFilter) IsZero() bool { return f == ReleaseFilter{} }

func (f ReleaseFilter) matches(s provider.Source) bool {
	return (f.Studio == "" || provider.SameStudio(s.Studio, f.Studio)) && f.Kind.matches(s.Kind)
}

// apply лишає джерела, що підходять під фільтр. `--studio` без `--kind` —
// ця студія з типом за правилами Pick для піна-wildcard, тож і з його вето:
// субтитри студії не підставляються, поки в серії є не-sub реліз, який можна
// відтворити (playable — джерела з екстрактором або локальним файлом), і
// людина сама не хоче субтитрів (жорстке правило 4). Озвучення лише на
// непідтримуваному хості вето не дає — так само, як у циклі ResolveWith.
func (f ReleaseFilter) apply(sources, playable []provider.Source, prefs library.Prefs) []provider.Source {
	var out []provider.Source
	for _, s := range sources {
		if f.matches(s) {
			out = append(out, s)
		}
	}
	if f.Studio != "" && f.Kind == "" && !library.WantsSub(library.Pin{}, prefs) && hasNonSub(playable) {
		voiced := out[:0:0]
		for _, s := range out {
			if s.Kind != provider.KindSub {
				voiced = append(voiced, s)
			}
		}
		out = voiced
	}
	return out
}

// pin — пін, з яким Pick обирає серед уже відфільтрованих джерел. Зі студією
// у фільтрі це пін на неї (назвою з сайту, бо Pick порівнює байтово) з
// типом фільтра або wildcard; без студії — чинний пін тайтлу.
func (f ReleaseFilter) pin(filtered []provider.Source, titlePin library.Pin) library.Pin {
	if f.Studio == "" || len(filtered) == 0 {
		return titlePin
	}
	pin := library.Pin{Studio: filtered[0].Studio}
	if f.Kind != "" && f.Kind != KindVoiced {
		pin.Kind = provider.Kind(f.Kind)
	}
	return pin
}

func hasNonSub(sources []provider.Source) bool {
	for _, s := range sources {
		if s.Kind != provider.KindSub {
			return true
		}
	}
	return false
}
