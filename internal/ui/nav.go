package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// frame — кадр стека «назад»: що показував список і на чому стояв курсор,
// плюс стан екрана однією структурою (див. view).
type frame struct {
	screen screen
	items  []item
	cursor int
	view
}

// ---- побудова екранів ----

func (m *Model) setItems(items []item, cursor int) tea.Cmd {
	current := m.list.Index()
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	cmd := m.list.SetItems(li)
	if len(items) == 0 {
		return cmd
	}
	if cursor < 0 {
		cursor = current
	}
	cursor = max(0, min(cursor, len(items)-1))
	m.list.Select(cursor)
	return cmd
}

func (m *Model) snapshot() frame {
	items := make([]item, 0, len(m.list.Items()))
	for _, it := range m.list.Items() {
		if it, ok := it.(item); ok {
			items = append(items, it)
		}
	}
	return frame{
		screen: m.screen,
		items:  items,
		cursor: m.list.GlobalIndex(),
		view:   m.clone(),
	}
}

func (m *Model) nextReq() int {
	m.reqID++
	return m.reqID
}

// beginNav — початок навігації: відкладений кадр попереднього переходу вже не
// діє, а новий req робить недійсними всі відповіді, замовлені до нього.
func (m *Model) beginNav() int {
	m.pending = nil
	m.pendingReq = 0
	return m.nextReq()
}

func (m *Model) commitPending(req int) {
	if m.pending == nil || m.pendingReq != req {
		return
	}
	m.stack = append(m.stack, *m.pending)
	m.pending = nil
	m.pendingReq = 0
}

func (m *Model) back() {
	if m.pending != nil {
		m.pending = nil
		m.pendingReq = 0
		m.nextReq()
		m.status = ""
		return
	}

	m.nextReq()
	if len(m.stack) == 0 {
		m.showHome()
		return
	}

	f := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	if f.screen == screenHome {
		m.showHome()
		if len(m.list.Items()) > 0 {
			// Секції могли перебудуватись, поки нас не було, — сповзаємо
			// із заголовка, якщо збережений індекс потрапив саме на нього.
			m.list.Select(max(0, min(f.cursor, len(m.list.Items())-1)))
			m.skipHeaders(1)
		}
		return
	}

	m.view = f.view
	// Статус кадру — минуле («додано в закладки», «Шукаю…»): повернення
	// назад показує підказку, а не відповідь на дію, якої вже немає.
	m.status = ""
	m.setScreen(f.screen)
	// setScreen скидає делегата в однорядковий — картки пошуку повертаємо
	// у двох рядках, інакше після Esc список раптом міняє вигляд.
	if f.screen == screenSearch && len(f.cards) > 0 {
		m.setDelegate(true)
	}
	_ = m.setItems(f.items, -1)
	if len(f.items) > 0 {
		m.list.Select(f.cursor)
	}
	if f.screen == screenSearch {
		m.input.SetValue(f.query)
		m.input.Blur()
	}
}

func (m *Model) setDelegate(twoLine bool) {
	m.list.SetDelegate(rowDelegate{twoLine: twoLine, ic: m.ic})
}

// setScreen — єдине місце, де застосовується конфігурація списку, залежна від
// екрана. Інакше налаштування протікають між екранами: список один на всіх.
func (m *Model) setScreen(s screen) {
	m.screen = s
	m.setDelegate(false)
	m.list.ResetFilter()
	// Домівка — це секції з дій, а не однорідний список; «/» там означає
	// «шукати нове», тому вбудований фільтр вимкнено.
	m.list.SetFilteringEnabled(s != screenHome && s != screenSettings && s != screenSettingValue)
	m.relayout()
}

// firstRow — індекс першого рядка, який можна вибрати. Курсор ніколи не стоїть
// на заголовку секції.
func firstRow(items []item) int {
	for i, it := range items {
		if !it.header {
			return i
		}
	}
	return 0
}

func isHeaderAt(items []list.Item, i int) bool {
	if i < 0 || i >= len(items) {
		return false
	}
	it, ok := items[i].(item)
	return ok && it.header
}

// skipHeaders зсуває курсор далі в напрямку dir, поки той стоїть на заголовку
// секції. Якщо в цьому напрямку рядків більше немає (курсор уперся в край
// списку) — відходимо назад до найближчого рядка у зворотному напрямку.
func (m *Model) skipHeaders(dir int) {
	items := m.list.Items()
	if dir == 0 || len(items) == 0 {
		return
	}
	i := m.list.Index()
	for isHeaderAt(items, i) && i+dir >= 0 && i+dir < len(items) {
		i += dir
	}
	if isHeaderAt(items, i) {
		for j := i - dir; j >= 0 && j < len(items); j -= dir {
			if !isHeaderAt(items, j) {
				i = j
				break
			}
		}
	}
	if !isHeaderAt(items, i) && i != m.list.Index() {
		m.list.Select(i)
	}
}

// navDirection — куди рухався курсор, якщо клавіша належить навігації списку.
// «На початок» рахуємо рухом уперед, «у кінець» — назад: саме туди треба
// зісковзнути із заголовка, що опинився на краю.
func navDirection(key string) int {
	switch key {
	case "up", "k", "left", "h", "pgup", "b", "u", "end", "G":
		return -1
	case "down", "j", "right", "l", "pgdown", "f", "d", "home", "g":
		return 1
	}
	return 0
}

const chromeBase = 4

func (m *Model) chromeHeight() int {
	if m.bannerVisible() {
		return brandChromeHeight
	}
	return chromeBase
}

func (m *Model) listHeight() int {
	n := m.h - m.chromeHeight()
	if m.screen == screenSearch {
		n--
	}
	return max(1, n)
}

func (m *Model) relayout() {
	m.list.SetSize(min(m.w-2, contentCap), m.listHeight())
	if m.w > 0 {
		// Рядок пошуку: 2 відступу + промпт + поле + комірка курсора. Поле на
		// один стовпець ширше — і термінал переносить рядок, зсуваючи кадр.
		m.input.SetWidth(max(1, min(m.w-3-lipgloss.Width(m.input.Prompt), contentCap-2)))
	}
}
