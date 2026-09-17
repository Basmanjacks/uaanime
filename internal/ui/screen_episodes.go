package ui

import (
	"fmt"
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
// Кожен непереглянутий рядок каже, ЩО гратиме (той самий Pick, що й у
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
	items := make([]item, 0, len(episodes))
	for _, ep := range episodes {
		it := item{
			icon:    m.ic.Pending,
			title:   fmt.Sprintf(i18n.TuiEpisodeNoPad, width, ep.Number),
			role:    m.ref.Provider + ":" + m.ref.Slug,
			payload: payloadEp{num: ep.Number},
		}
		p, started := progress[ep.Number]
		if started && p.Completed {
			it.icon, it.badge = m.ic.Done, i18n.TuiEpDone
			m.applyDownloadBadge(&it, ep.Number, saved)
			items = append(items, it)
			continue
		}
		chosen, cands := library.Pick(library.SourcesFromReleases(ep), pin, m.eng.Prefs)
		if chosen != nil {
			it.meta = releaseSummary(*chosen, ep)
			switch {
			case chosen.Kind == provider.KindSub && !library.WantsSub(pin, m.eng.Prefs):
				it.badge, it.badgeWarn = i18n.TuiKindNotOutYet, true
			case pin.Studio == "" && len(cands) > 1:
				it.badge = i18n.TuiPickWillChooseStudio
			}
		}
		if started && p.PositionSec > 0 {
			it.icon = m.ic.Play
			at := fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
			if chosen != nil {
				// Позиція замість хвоста «що ще є»: для початої серії
				// важливіше, де зупинився, ніж хто ще озвучив.
				it.meta = releasePair(*chosen) + metaSep + at
			} else {
				it.meta = at
			}
		}
		// Стан завантаження — останнім: він перекриває бейдж вибору релізу,
		// бо каже про дію, яка вже триває або вже дала файл на диску.
		m.applyDownloadBadge(&it, ep.Number, saved)
		items = append(items, it)
	}
	return items
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
	return i18n.KindShort(s.Kind) + metaSep + s.Studio
}

// releaseSummary — обраний реліз і скільки ще є: «Озв · Рідний Голос · ще 1 озв · 1 саб».
// Нульові групи не друкуються; multi рахується озвученням, як і в Pick.
func releaseSummary(chosen provider.Source, ep provider.Episode) string {
	voiced, subs := 0, 0
	for _, r := range provider.CleanEpisode(ep).Releases {
		if r.Studio == chosen.Studio && r.Kind == chosen.Kind {
			continue
		}
		if r.Kind == provider.KindSub {
			subs++
		} else {
			voiced++
		}
	}
	out := releasePair(chosen)
	if voiced > 0 {
		out += metaSep + fmt.Sprintf(i18n.TuiMoreVoiced, voiced)
	}
	if subs > 0 {
		out += metaSep + fmt.Sprintf(i18n.TuiMoreSubs, subs)
	}
	return out
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
