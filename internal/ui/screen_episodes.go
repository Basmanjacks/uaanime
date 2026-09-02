package ui

import (
	"fmt"
	"sort"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func (m *Model) showEpisodes() tea.Cmd {
	m.setScreen(screenEpisodes)
	title := m.eng.Lib.TitleByRef(m.ref)
	var items []item
	for _, ep := range m.episodes {
		icon, meta := m.ic.Pending, releasesSummary(ep.Releases)
		badge := ""
		if title != nil {
			if p := m.eng.Lib.ProgressFor(title.ID, ep.Number); p != nil {
				if p.Completed {
					icon, meta, badge = m.ic.Done, "", i18n.TuiEpDone
				} else if p.PositionSec > 0 {
					icon = m.ic.Play
					meta = fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
				}
			}
		}
		items = append(items, item{
			icon:    icon,
			title:   fmt.Sprintf(i18n.TuiEpisodeNo, ep.Number),
			meta:    meta,
			badge:   badge,
			payload: payloadEp{num: ep.Number},
		})
	}
	return m.setItems(items, 0)
}

func releasesSummary(rels []provider.Release) string {
	seen := map[string]bool{}
	var studios []string
	for _, r := range rels {
		if !seen[r.Studio] {
			seen[r.Studio] = true
			studios = append(studios, r.Studio)
		}
	}
	sort.Strings(studios)
	if len(studios) > 3 {
		return fmt.Sprintf("%s, %s, %s %s", studios[0], studios[1], studios[2],
			fmt.Sprintf(i18n.TuiMoreStudios, len(studios)-3))
	}
	if len(studios) == 0 {
		return ""
	}
	out := studios[0]
	for _, s := range studios[1:] {
		out += ", " + s
	}
	return out
}
