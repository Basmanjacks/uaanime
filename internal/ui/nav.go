package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// frame — кадр стека «назад»: що показував список і на чому стояв курсор,
// плюс стан екрана однією структурою (див. view).
type frame struct {
	screen           screen
	items            []item
	cursor           int
	selectedKey      string
	filterText       string
	filterState      list.FilterState
	historyShown     int
	historyFiltering bool
	view
}

// ---- побудова екранів ----

func (m *Model) setItems(items []item, cursor int) tea.Cmd {
	m.filterGen++
	current := m.list.Index()
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	cmd := guardFilter(m.list.SetItems(li), m.filterGen)
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
		screen:      m.screen,
		items:       items,
		cursor:      m.list.Index(),
		selectedKey: m.selectedKey(), filterText: m.list.FilterValue(), filterState: m.list.FilterState(), historyShown: m.historyShown, historyFiltering: m.historyFiltering,
		view: m.clone(),
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
	// Оновлення, що летить, більше не стосується екрана, з якого пішли; сама
	// операція доживає (кеш на диску їй потрібен), тому refreshBusy не чіпаємо.
	m.refreshGen++
	m.statusGen++
	m.statusKind = statusInfo
	m.status = ""
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
	if m.playCancel == nil && m.chainActive {
		m.endChain()
	}
	wasPending := m.pending != nil
	m.beginNav()
	if wasPending {
		m.status = ""
		return
	}

	if len(m.stack) == 0 {
		m.showHome()
		return
	}

	f := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	m.view = f.view
	m.status = ""
	m.setScreen(f.screen)
	m.pendingUI = m.restoreRows(f)
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
	m.filterGen++
	m.restoreSelection = nil
	m.screen = s
	// Плейлист пульта належить тайтлу, а не сесії: тільки ці три екрани ним
	// володіють. Чистимо саме тут, бо back() відновлює пошук чи історію в обхід
	// showHome, і список тайтлу лишився б на телефоні після виходу з нього.
	if s != screenEpisodes && s != screenStudio && s != screenPlaying {
		m.clearPlaylist()
	}
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
	items := m.list.VisibleItems()
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
	if m.overlay == overlayNone && m.bannerVisible() {
		return brandChromeHeight
	}
	return chromeBase
}

func (m *Model) listHeight() int {
	n := m.h - m.chromeHeight()
	if m.overlay == overlayBudget {
		n -= len(m.budgetNoteLines())
	}
	if m.screen == screenSearch && m.overlay == overlayNone {
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

// firstRowIndex — індекс першого рядка списку, який можна вибрати; −1, якщо
// вибирати нічого. Разом із selectFirstRow це вся механіка передачі фокуса між
// полем пошуку і списком.
func (m *Model) firstRowIndex() int {
	items := m.list.Items()
	for i := range items {
		if !isHeaderAt(items, i) {
			return i
		}
	}
	return -1
}

func (m *Model) selectFirstRow() bool {
	i := m.firstRowIndex()
	if i < 0 {
		return false
	}
	m.list.Select(i)
	return true
}

// restoreRows rebuilds domain-backed lists before restoring their visible selection.
func (m *Model) restoreRows(f frame) tea.Cmd {
	m.list.ResetFilter()
	var rows []item
	switch m.screen {
	case screenHome:
		m.rebuildHome()
		m.selectKey(f.selectedKey, f.cursor)
		return nil
	case screenBookmarks:
		m.epsScratch = map[string][]provider.Episode{}
		rows = m.bookmarkRows()
	case screenHistory:
		m.historyShown = f.historyShown
		m.historyFiltering = f.historyFiltering
		m.historyAll = m.historyRows()
		rows = m.visibleHistoryRows()
	case screenEpisodes:
		rows = m.episodeRows()
	case screenSearch:
		m.setDelegate(len(m.cards) > 0)
		rows = m.searchRows()
		if len(m.cards) == 0 {
			rows = m.recentRows()
		}
	default:
		rows = f.items
	}
	if f.filterState != list.Unfiltered {
		m.list.FilterInput.SetValue(f.filterText)
		m.list.SetFilterState(f.filterState)
		cmd := m.setItems(rows, f.cursor)
		m.restoreSelection = &f
		return cmd
	}
	cmd := m.setItems(rows, f.cursor)
	m.selectKey(f.selectedKey, f.cursor)
	return cmd
}
