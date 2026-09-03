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

// recentRows — секція нещодавніх запитів; показується, поки на екрані немає
// результатів. Порожня історія не дає ані заголовка, ані порожнього рядка.
func (m *Model) recentRows() []item {
	if len(m.searches) == 0 {
		return nil
	}
	items := make([]item, 0, len(m.searches)+1)
	items = append(items, item{title: i18n.TuiBlockRecent, header: true})
	for _, q := range m.searches {
		items = append(items, item{icon: m.ic.Pending, title: q, payload: payloadQuery{q: q}})
	}
	return items
}

// loadSearches читає історію з диска. Синхронно на горутині Update — той самий
// клас, що LoadEpisodes у bookmarkSelected: файл крихітний, а фонова команда
// повернула б список уже після того, як людина почала друкувати.
func (m *Model) loadSearches() {
	if m.eng == nil || m.eng.Store == nil {
		m.searches = nil
		return
	}
	m.searches = m.eng.Store.LoadSearches()
}

// rememberSearch кладе успішний запит в історію. Помилка запису не має
// перебивати результати пошуку: історія — зручність, а не дані перегляду.
func (m *Model) rememberSearch(q string) {
	if m.eng == nil || m.eng.Store == nil {
		return
	}
	if list, err := m.eng.Store.AddSearch(q); err == nil {
		m.searches = list
	}
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
			// Запит без результатів не запам'ятовуємо: повторювати його немає сенсу.
			rows := m.recentRows()
			return m.setItems(rows, firstRow(rows))
		}
		m.rememberSearch(m.query)
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
