package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
)

// showHistory — переглянуті тайтли, згруповані за найсвіжішим прогресом.
func (m *Model) showHistory() {
	m.setScreen(screenHistory)
	sorted := make([]*library.Progress, len(m.eng.Lib.Progress))
	copy(sorted, m.eng.Lib.Progress)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].WatchedAt.After(sorted[j].WatchedAt) })

	type historyGroup struct {
		newest *library.Progress
		count  int
	}
	groups := make([]historyGroup, 0, len(sorted))
	groupByTitle := make(map[string]int, len(sorted))
	for _, p := range sorted {
		if index, ok := groupByTitle[p.TitleID]; ok {
			groups[index].count++
			continue
		}
		groupByTitle[p.TitleID] = len(groups)
		groups = append(groups, historyGroup{newest: p, count: 1})
	}

	now := time.Now()
	var items []item
	for _, group := range groups {
		p := group.newest
		t := m.titleByID(p.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		icon := m.ic.Play
		if p.Completed {
			icon = m.ic.Done
		}
		items = append(items, item{
			icon:  icon,
			title: titleName(t),
			meta: strings.Join([]string{
				fmt.Sprintf(i18n.TuiEpisodeNo, p.Episode),
				i18n.Episodes(group.count),
				humanDate(p.WatchedAt, now),
			}, " · "),
			payload: payloadResume{ref: t.Sources[0], ep: p.Episode},
		})
		if len(items) == 20 {
			break
		}
	}
	_ = m.setItems(items, 0)
}

// titleName: тайтли, зіграні headless-командою, ще не мають назви — показуємо слаг.
func titleName(t *library.LocalTitle) string {
	if t.Name != "" {
		return t.Name
	}
	if len(t.Sources) > 0 {
		return t.Sources[0].Slug
	}
	return t.ID
}

func (m *Model) titleByID(id string) *library.LocalTitle {
	for _, t := range m.eng.Lib.Titles {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func stateLabel(s library.State) string {
	switch s {
	case library.StateCompleted:
		return i18n.TuiStateDone
	case library.StatePlanned:
		return i18n.TuiStatePlanned
	default:
		return i18n.TuiStateWatching
	}
}
