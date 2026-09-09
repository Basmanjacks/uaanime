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
	groups := groupHistory(m.eng.Lib.Progress)

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

type historyGroup struct {
	newest *library.Progress
	count  int
	index  int
}

func groupHistory(progress []*library.Progress) []historyGroup {
	var groups []historyGroup
	byTitle := make(map[string]int)
	for i, p := range progress {
		if index, ok := byTitle[p.TitleID]; ok {
			g := &groups[index]
			g.count++
			if p.WatchedAt.After(g.newest.WatchedAt) {
				g.newest, g.index = p, i
			}
		} else {
			byTitle[p.TitleID] = len(groups)
			groups = append(groups, historyGroup{newest: p, count: 1, index: i})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].newest.WatchedAt.Equal(groups[j].newest.WatchedAt) {
			// Match sorting all progress stably before grouping: ties belong
			// to the first newest record, not the title's first older record.
			return groups[i].index < groups[j].index
		}
		return groups[i].newest.WatchedAt.After(groups[j].newest.WatchedAt)
	})
	return groups
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
