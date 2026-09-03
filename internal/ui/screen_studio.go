package ui

import (
	"fmt"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func (m *Model) showStudioChoice(candidates []provider.Source) {
	m.setScreen(screenStudio)
	pin := m.studioPin()
	coverage, total := m.studioCoverage()
	seen := map[string]bool{}
	var items []item
	for _, s := range candidates {
		key := s.Studio + "|" + string(s.Kind)
		if seen[key] {
			continue
		}
		seen[key] = true
		it := item{
			title:   s.Studio,
			meta:    i18n.KindLabel(s.Kind),
			payload: payloadStudio{src: s},
		}
		// Покриття йде в мету, а не в бейдж: зелений бейдж читається як
		// «все добре», а «3/12» — це попередження, а не похвала. Нуль не
		// показуємо взагалі: список серій міг просто відстати від сайту.
		if n := coverage[s.Studio]; n > 0 && total > 0 {
			it.meta += metaSep + fmt.Sprintf(i18n.TuiStudioCoverage, n, total)
		}
		if s.Studio == pin {
			it.icon = m.ic.Done
			it.iconAccent = true
		}
		items = append(items, it)
	}
	_ = m.setItems(items, 0)
}

// studioCoverage — скільки серій тайтлу має кожна студія і скільки їх усього.
// Рахується з релізів, які вже лежать у списку серій: мережа тут не потрібна,
// а без списку покриття просто не показується.
func (m *Model) studioCoverage() (map[string]int, int) {
	episodes, ok := m.currentEpisodes()
	if !ok {
		return nil, 0
	}
	coverage := map[string]int{}
	for _, ep := range episodes {
		// Одна серія рахується студії один раз, навіть якщо в неї там і
		// дубляж, і субтитри.
		counted := map[string]bool{}
		for _, r := range ep.Releases {
			if counted[r.Studio] {
				continue
			}
			counted[r.Studio] = true
			coverage[r.Studio]++
		}
	}
	return coverage, len(episodes)
}

// currentEpisodes — серії саме поточного тайтлу. m.episodes лишається від
// попереднього, поки не прийде episodesDoneMsg, тож без збігу ref беремо кеш
// із диска (як bookmarkSelected), а не чужий список.
func (m *Model) currentEpisodes() ([]provider.Episode, bool) {
	if len(m.episodes) > 0 && m.episodesRef == m.ref {
		return m.episodes, true
	}
	if m.eng == nil || m.eng.Store == nil {
		return nil, false
	}
	episodes, _, found := m.eng.Store.LoadEpisodes(m.ref)
	if !found || len(episodes) == 0 {
		return nil, false
	}
	return episodes, true
}

func (m Model) studioPin() string {
	title := m.eng.Lib.TitleByRef(m.ref)
	if title == nil {
		return ""
	}
	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry == nil {
		return ""
	}
	return entry.StudioPin
}
