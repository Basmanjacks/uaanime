package ui

import (
	"fmt"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func (m *Model) showEpisodes() tea.Cmd {
	m.setScreen(screenEpisodes)
	return m.setItems(m.episodeRows(), 0)
}

// episodeRows будує рядки списку серій із m.episodes і прогресу бібліотеки.
// Окремо від showEpisodes, бо клавіша «переглянуто» перебудовує ті самі рядки,
// не рухаючи курсор і не заходячи на екран заново.
//
// Реліз тайтла показується один раз, рядком під назвою (m.epRelease); рядок
// серії каже про реліз лише тоді, коли гратиме не він (той самий Pick, що й у
// відтворенні, лише з метаданих), і попереджає, коли це саби, яких людина не
// просила. Переглянуті рядки реліз не показують: журнал його не зберігає, а
// домальовувати минуле з поточного вибору — нова брехня замість старої.
func (m *Model) episodeRows() []item {
	title := m.eng.Lib.TitleByRef(m.ref)
	titleID := ""
	pin := library.Pin{}
	if title != nil {
		titleID = title.ID
		pin = m.pinFor(title.ID)
	}
	progress := library.IndexProgress(titleID, m.eng.Lib.Progress)
	episodes, _ := m.currentEpisodes()
	// Одне читання папки завантажень на всю перебудову списку (див. savedFor).
	saved := m.savedFor(m.ref)
	width := episodeNumberWidth(episodes)
	picks := make([]episodePick, len(episodes))
	for i, ep := range episodes {
		chosen, cands := library.Pick(library.SourcesFromReleases(ep), pin, m.eng.Prefs)
		picks[i] = episodePick{chosen: chosen, ambiguous: len(cands) > 1}
	}
	m.epRelease = summarizeReleases(episodes, picks, pin)
	items := make([]item, 0, len(episodes))
	for i, ep := range episodes {
		it := item{
			icon:    m.ic.Pending,
			title:   fmt.Sprintf(i18n.TuiEpisodeNoPad, width, ep.Number),
			role:    m.ref.Provider + ":" + m.ref.Slug,
			payload: payloadEp{num: ep.Number},
		}
		chosen := picks[i].chosen
		p, started := progress[ep.Number]
		if started && p.Completed {
			it.icon, it.badge = m.ic.Done, i18n.TuiEpDone
			m.applyDownloadBadge(&it, ep.Number, saved, chosen)
			items = append(items, it)
			continue
		}
		if chosen != nil {
			if m.epRelease.differs(*chosen) {
				it.meta = releasePair(*chosen)
			}
			if chosen.Kind == provider.KindSub && !library.WantsSub(pin, m.eng.Prefs) {
				it.badge, it.badgeWarn = i18n.TuiKindNotOutYet, true
			}
		}
		if started && p.PositionSec > 0 {
			it.icon = m.ic.Play
			at := fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
			if it.meta != "" {
				it.meta += metaSep + at
			} else {
				it.meta = at
			}
		}
		// Стан завантаження — останнім: він перекриває бейдж вибору релізу,
		// бо каже про дію, яка вже триває або вже дала файл на диску.
		m.applyDownloadBadge(&it, ep.Number, saved, chosen)
		items = append(items, it)
	}
	return items
}

type episodePick struct {
	chosen    *provider.Source
	ambiguous bool // Pick лишив кілька студій — Enter спитає, яку
}

// titleRelease — реліз тайтла для рядка під назвою: що гратиме, якщо нічого
// не обирати, і скільки є інших. main — найчастіший вибір Pick по серіях;
// nil, коли без піна вибір неоднозначний (тоді обіцяти студію не можна) або
// релізів немає зовсім. voiced/subs — інші релізи, коли main є, і всі — коли
// його немає.
type titleRelease struct {
	pinned       bool
	ambiguous    bool
	main         *provider.Release
	voiced, subs int
}

// differs — чи гратиме серія не той реліз, що в рядку під назвою. Без
// спільного релізу рядкам нема від чого відхилятися.
func (r titleRelease) differs(s provider.Source) bool {
	return r.main != nil && !provider.SameRelease(*r.main, releaseOf(s))
}

func releaseOf(s provider.Source) provider.Release {
	return provider.Release{Studio: s.Studio, Kind: s.Kind}
}

func summarizeReleases(episodes []provider.Episode, picks []episodePick, pin library.Pin) titleRelease {
	r := titleRelease{pinned: pin.Studio != ""}
	var counts []int
	var chosen []provider.Release
	for _, p := range picks {
		if p.ambiguous && !r.pinned {
			r.ambiguous = true
		}
		if p.chosen == nil {
			continue
		}
		rel := releaseOf(*p.chosen)
		i := slices.IndexFunc(chosen, func(c provider.Release) bool { return provider.SameRelease(c, rel) })
		if i < 0 {
			chosen, counts = append(chosen, rel), append(counts, 0)
			i = len(chosen) - 1
		}
		counts[i]++
	}
	if !r.ambiguous && len(chosen) > 0 {
		best := 0
		for i := range counts {
			if counts[i] > counts[best] {
				best = i
			}
		}
		r.main = &chosen[best]
	}
	var all []provider.Release
	for _, ep := range episodes {
		for _, rel := range provider.CleanEpisode(ep).Releases {
			if slices.ContainsFunc(all, func(c provider.Release) bool { return provider.SameRelease(c, rel) }) {
				continue
			}
			all = append(all, rel)
			if r.main != nil && provider.SameRelease(*r.main, rel) {
				continue
			}
			// multi рахується озвученням, як і в Pick.
			if rel.Kind == provider.KindSub {
				r.subs++
			} else {
				r.voiced++
			}
		}
	}
	return r
}

// episodeNumberWidth — ширина колонки номера: «Серія  9» під «Серія 10», щоб
// токени типу стояли рівно один під одним.
func episodeNumberWidth(episodes []provider.Episode) int {
	maxNumber := 0
	for _, ep := range episodes {
		if ep.Number > maxNumber {
			maxNumber = ep.Number
		}
	}
	return len(strconv.Itoa(maxNumber))
}

// releasePair — «Озв · Рідний Голос»: токен типу першим, бо він переживає
// будь-яке обрізання мети, а саме заради нього рядок і читають.
func releasePair(s provider.Source) string {
	return i18n.KindShort(s.Kind) + metaSep + i18n.StudioName(s.Studio)
}

// pinFor — пін тайтлу значеннями (для Pick на Update-горутині).
func (m Model) pinFor(titleID string) library.Pin {
	entry := m.eng.Lib.EntryLookup(titleID)
	if entry == nil {
		return library.Pin{}
	}
	return library.Pin{Studio: entry.StudioPin, Kind: entry.KindPin}
}

// willPlay — що Pick обере для серії num з кешованих метаданих; nil, коли
// серії або її релізів у списку немає.
func (m Model) willPlay(episodes []provider.Episode, num int, pin library.Pin) *provider.Source {
	for _, ep := range episodes {
		if ep.Number != num {
			continue
		}
		chosen, _ := library.Pick(library.SourcesFromReleases(ep), pin, m.eng.Prefs)
		return chosen
	}
	return nil
}
