package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// searchRows — картки як рядки списку плюс, за потреби, «показати ще».
// Рядки й картки йдуть один в один: індекс картки == індекс рядка, тому
// курсор після довантаження ставиться простою арифметикою.
func (m *Model) searchRows() []item {
	items := make([]item, 0, len(m.cards)+1)
	for _, c := range m.cards {
		items = append(items, item{
			title:     c.Name,
			meta:      cardMeta(c),
			metaParts: cardMetaParts(c),
			badge:     m.titleStateBadge(c.TitleRef),
			payload:   payloadTitle{ref: c.TitleRef, epAired: c.EpAired},
		})
	}
	if m.hasMore {
		items = append(items, item{icon: m.ic.Pending, title: i18n.TuiShowMore, payload: payloadMore{}})
	}
	return items
}

func (m *Model) titleStateBadge(ref provider.TitleRef) string {
	title := m.eng.Lib.TitleByRef(ref)
	if title == nil {
		return ""
	}
	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry == nil || entry.Hidden {
		return ""
	}
	return stateLabel(entry.State)
}

// applySearchPage — перша сторінка замінює результати, наступні дозаписуються.
// Курсор при довантаженні стає на перший нововантажений рядок: людина натиснула
// «показати ще» саме заради нього, повертати її на початок списку — образливо.
func (m *Model) applySearchPage(msg searchDoneMsg) tea.Cmd {
	if msg.page <= 1 {
		m.cards, m.page, m.hasMore = msg.cards, 1, msg.hasMore
		if len(m.cards) == 0 {
			m.hasMore = false
			m.setDelegate(false)
			m.status = i18n.TuiNothingFound
			return m.setItems(nil, 0)
		}
		m.setDelegate(true)
		return m.setItems(m.searchRows(), 0)
	}

	first := len(m.cards)
	m.cards = append(m.cards, msg.cards...)
	m.page, m.hasMore = msg.page, msg.hasMore
	m.setDelegate(true)
	cursor := first
	// first — індекс у повному списку, а Select працює у видимому просторі.
	// Під активним фільтром вони не збігаються — лишаємо курсор на місці.
	if m.list.FilterState() != list.Unfiltered {
		cursor = m.list.Index()
	}
	return m.setItems(m.searchRows(), cursor)
}
