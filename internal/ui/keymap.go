package ui

import (
	"fmt"
	"slices"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

// binding — клавіша екрана: що вона робить і як підписана в підказці та
// довідці. Одна таблиця на обидві речі, тож показана клавіша не може не
// працювати, а робоча — випасти з довідки.
type binding struct {
	keys  []string // msg.String(), які запускають дію
	help  string   // підпис клавіші в підказці; "" — не показується
	label string
	// shown — чи показувати зараз (наприклад, «U» лише коли є що скасувати).
	// Не вимикає саму клавішу: прихована дія лишається no-op усередині run.
	shown func(Model) bool
	run   func(m Model, msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd)
}

type keyRun = func(m Model, msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd)

// do — дія, якій не потрібні ні повідомлення, ні сама клавіша.
func do(f func(Model) (tea.Model, tea.Cmd)) keyRun {
	return func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) { return f(m) }
}

func keys(k ...string) []string { return k }

func runBack(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
	m.back()
	return m, nil
}

func runPlaying(m Model, _ tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	return m.playingKey(key)
}

func bindHelp() binding {
	return binding{keys: keys("?"), help: "?", label: i18n.TuiActionHelp, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
		m.openOverlay(overlayHelp)
		return m, nil
	}}
}

func bindBookmark() binding {
	return binding{keys: keys("m", "M"), help: "M", label: i18n.TuiActionBookmark, run: do(Model.bookmarkSelected)}
}

func bindRefresh() binding {
	return binding{keys: keys("r", "R"), help: "R", label: i18n.TuiActionRefresh, run: do(Model.refreshNow)}
}

// d і D рівноправні, як s/S чи m/M: у підказці клавіша велика, але людина
// тисне без Shift. Пейджинг списку на «d» (vim-звичка bubbles) цього не
// вартий — PgDn і →/l лишаються.
func bindDownloads() binding {
	return binding{keys: keys("d", "D"), help: "D", label: i18n.TuiActionDownloads, run: do(Model.openDownloads)}
}

func bindEnter(label string) binding {
	return binding{keys: keys("enter"), help: "Enter", label: label, run: do(Model.openSelected)}
}

func bindEsc(label string) binding {
	return binding{keys: keys("esc"), help: "Esc", label: label, run: runBack}
}

// bindListFilter — «/» лишається клавішею списку (фільтр bubbles); у таблиці
// вона заради підказки, а історія перед фільтром підкладає всі рядки.
func bindListFilter() binding {
	return binding{keys: keys("/"), help: "/", label: i18n.TuiActionFilter,
		shown: func(m Model) bool { return m.list.FilteringEnabled() },
		run: func(m Model, msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
			if m.screen == screenHistory {
				m.historyFiltering = true
				m.historyAll = m.historyRows()
				_ = m.setItems(m.visibleHistoryRows(), -1)
			}
			return m.listKey(msg, key)
		}}
}

// listEsc — Esc на екрані зі списком: спершу знімає застосований фільтр, і
// лише тоді веде назад.
func listEsc() binding {
	return binding{keys: keys("esc"), help: "Esc", label: i18n.TuiActionBack, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
		if m.list.FilterState() == list.FilterApplied {
			if m.screen == screenHistory {
				return m, m.resetHistoryFilter()
			}
			m.list.ResetFilter()
			return m, nil
		}
		m.back()
		return m, nil
	}}
}

// bindings — клавіші поточного екрана в порядку важливості: підказка внизу
// обрізається з кінця, тому перші — ті, без яких екраном не скористатися.
func (m Model) bindings() []binding {
	var b []binding
	switch m.screen {
	case screenHome:
		b = []binding{
			bindEnter(i18n.TuiActionOpen),
			bindHelp(),
			// На домівці фільтрувати нічого: «/» — це той самий «Пошук нового».
			{keys: keys("/"), help: "/", label: i18n.TuiActionSearch, run: do(Model.openSearch)},
			bindBookmark(),
			{keys: keys(","), help: ",", label: i18n.TuiActionSettings, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				m, _ = m.openSettings()
				return m, nil
			}},
			{keys: keys("q", "Q"), help: "Q", label: i18n.TuiActionQuit, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				return m.requestQuit(true)
			}},
			bindRefresh(),
			bindDownloads(),
		}

	case screenSearch:
		if m.input.Focused() {
			// Поле вводу і список ділять екран, тому фокус передається явно: поки
			// поле активне, решта клавіш — це текст запиту, а не команди.
			b = []binding{
				{keys: keys("enter"), help: "Enter", label: i18n.TuiActionSearch, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
					return m.runSearch(m.input.Value())
				}},
				bindEsc(i18n.TuiActionBack),
				{keys: keys("down", "tab"), help: "↓", label: i18n.TuiActionMove, run: func(m Model, msg tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
					if m.selectFirstRow() {
						m.input.Blur()
						return m, nil
					}
					return m.inputKey(msg)
				}},
			}
			break
		}
		noCards := func(m Model) bool { return len(m.cards) == 0 }
		b = []binding{
			bindEnter(i18n.TuiActionOpen),
			bindEsc(i18n.TuiActionBack),
			bindHelp(),
			{keys: keys("/"), help: "/", label: i18n.TuiActionSearchInput, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				m.input.SetValue("")
				return m, m.input.Focus()
			}},
			{keys: keys("x", "X"), help: "X", label: i18n.TuiActionForgetQuery, shown: noCards, run: do(Model.forgetSelectedQuery)},
			{keys: keys("m", "M"), help: "M", label: i18n.TuiActionBookmark, shown: func(m Model) bool { return !noCards(m) }, run: do(Model.bookmarkSelected)},
			bindDownloads(),
			// Вихід угору з першого рядка — назад у поле, зі збереженим запитом:
			// інакше з рядка історії немає куди повернутись, окрім «/».
			{keys: keys("up"), run: func(m Model, msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
				if i := m.firstRowIndex(); i < 0 || m.list.Index() <= i {
					return m, m.input.Focus()
				}
				return m.listKey(msg, key)
			}},
		}

	case screenEpisodes:
		b = []binding{
			bindEnter(i18n.TuiActionPlay),
			listEsc(),
			bindHelp(),
			bindListFilter(),
			bindBookmark(),
			{keys: keys("s", "S"), help: "S", label: i18n.TuiActionStudio, run: do(Model.requestStudios)},
			{keys: keys("d", "D"), help: "D", label: i18n.TuiActionDownload, run: do(Model.requestDownload)},
			{keys: keys("x", "X"), help: "X", label: i18n.TuiActionWatched, run: do(Model.toggleWatched)},
			{keys: keys("W"), help: "W", label: i18n.TuiActionBulk, run: do(Model.watchedBefore)},
			bindRefresh(),
		}

	case screenHistory, screenBookmarks:
		b = []binding{bindEnter(i18n.TuiActionOpen), listEsc(), bindHelp(), bindListFilter()}
		if m.screen == screenBookmarks {
			b = append(b, bindBookmark(), bindRefresh())
		}
		b = append(b, bindDownloads())

	case screenSettings:
		b = []binding{
			bindEnter(i18n.TuiActionPick),
			bindEsc(i18n.TuiActionBack),
			bindHelp(),
			{keys: keys("left", "h", "right", "l"), help: "←/→", label: i18n.TuiActionChange, run: func(m Model, _ tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
				dir := 1
				if key == "left" || key == "h" {
					dir = -1
				}
				m.cycleSetting(dir)
				return m, nil
			}},
		}

	case screenSettingValue:
		b = []binding{
			{keys: keys("enter"), help: "Enter", label: i18n.TuiActionPick, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				if it, ok := m.list.SelectedItem().(item); ok {
					if p, ok := it.payload.(payloadSettingValue); ok {
						return m.pickSettingValue(p), nil
					}
				}
				return m, nil
			}},
			bindEsc(i18n.TuiActionBack),
			bindHelp(),
		}

	case screenStudio:
		b = []binding{bindEnter(i18n.TuiActionPick), bindEsc(i18n.TuiActionBack), bindHelp()}

	case screenDownloadQuality:
		b = []binding{bindEnter(i18n.TuiActionSave), bindEsc(i18n.TuiActionCancel), bindHelp()}

	case screenDownloads:
		b = []binding{
			bindEnter(i18n.TuiActionPlay),
			{keys: keys("x", "X"), help: "X", label: i18n.TuiActionRemove, run: do(Model.removeDownload)},
			bindEsc(i18n.TuiActionBack),
			bindHelp(),
		}

	case screenDownloadDir:
		// Поле вводу володіє клавішами, як на екрані пошуку: решта — це текст
		// шляху, а не команди списку.
		return []binding{
			{keys: keys("enter"), help: "Enter", label: i18n.TuiActionSave, run: do(Model.saveDownloadDir)},
			{keys: keys("esc"), help: "Esc", label: i18n.TuiActionCancel, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				m.input.Blur()
				m.back()
				return m, nil
			}},
		}

	case screenPlaying:
		// Esc, як і раніше, лише скасовує сесію — вихід робить playDoneMsg.
		return []binding{
			{keys: keys("space"), help: "Space", label: i18n.TuiActionPause, run: runPlaying},
			{keys: keys("esc"), help: "Esc", label: i18n.TuiActionStop, run: runPlaying},
			bindHelp(),
			{keys: keys("left", "right"), help: "←/→", label: i18n.TuiActionSeek, run: runPlaying},
			{keys: keys("shift+left", "shift+right"), help: "Shift+←/→", label: i18n.TuiActionSeekBig, run: runPlaying},
			{keys: keys("n", "N"), help: "N", label: i18n.TuiActionNext, run: runPlaying},
			{keys: keys("+", "=", "-"), help: "+/−", label: i18n.TuiActionVolume, run: runPlaying},
			{keys: keys("."), help: ".", label: i18n.TuiActionStopAfter, run: runPlaying},
			{keys: keys("B"), help: "B", label: i18n.TuiActionBudget, run: func(m Model, _ tea.KeyPressMsg, _ string) (tea.Model, tea.Cmd) {
				m.openOverlay(overlayBudget)
				return m, nil
			}},
		}
	}
	if !m.inputFocused() {
		b = append(b, binding{keys: keys("U"), help: "U", label: i18n.TuiActionUndo,
			shown: func(m Model) bool { return m.eng.WatchedUndoAvailable(m.undo) },
			run:   do(Model.undoWatched)})
	}
	return b
}

// actions — рядок підказки й довідка: показані клавіші поточного екрана або
// оверлею, у порядку таблиці.
func (m Model) actions() []action {
	if m.overlay == overlayBudget {
		return []action{{"Enter", i18n.TuiActionPick}, {"Esc", i18n.TuiActionBack}}
	}
	if m.overlay == overlayHelp {
		return []action{{"Esc", i18n.TuiHelpClose}, {"↑/↓", i18n.TuiActionMove}}
	}
	var a []action
	for _, b := range m.bindings() {
		if b.help != "" && (b.shown == nil || b.shown(m)) {
			a = append(a, action{b.help, b.label})
		}
	}
	return a
}

// screenKey — клавіша, яку не перехопила прелюдія handleKey: дія з таблиці
// екрана або, якщо такої немає, полю вводу чи списку.
func (m Model) screenKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	for _, b := range m.bindings() {
		if slices.Contains(b.keys, key) {
			return b.run(m, msg, key)
		}
	}
	switch {
	case m.screen == screenPlaying:
		return m, nil
	case m.inputFocused() || m.screen == screenDownloadDir:
		return m.inputKey(msg)
	}
	return m.listKey(msg, key)
}

func (m Model) inputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) listKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	// Під --debug непізнана клавіша показує своє ім'я: інакше «натиснув — і
	// нічого» не відрізнити від не тієї розкладки чи не того протоколу
	// клавіатури в терміналі.
	if m.opts.Debug && len([]rune(key)) == 1 {
		m.setStatus(statusInfo, fmt.Sprintf(i18n.TuiDebugKey, key))
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	// Курсор ніколи не зупиняється на заголовку секції: він не робить нічого,
	// тож зупинка на ньому виглядає як зависання списку.
	m.skipHeaders(navDirection(key))
	return m, cmd
}
