package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func (m Model) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m.requestQuit()
	}
	if key == "esc" && m.pending != nil && m.screen != screenPlaying {
		m.back()
		return m, nil
	}

	// під час фільтрації всі клавіші належать списку
	if m.screen != screenSearch && m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	if (key == "m" || key == "M") && !m.list.SettingFilter() && (m.screen != screenSearch || !m.input.Focused()) {
		switch m.screen {
		case screenHome, screenSearch, screenEpisodes:
			return m.bookmarkSelected()
		}
	}
	if (key == "s" || key == "S") && !m.list.SettingFilter() && (m.screen != screenSearch || !m.input.Focused()) && m.screen == screenEpisodes {
		it, ok := m.list.SelectedItem().(item)
		if !ok {
			return m, nil
		}
		p, ok := it.payload.(payloadEp)
		if !ok {
			return m, nil
		}
		m.pendingEp = p.num
		req := m.nextReq()
		m.status = i18n.TuiResolving
		return m, m.studiosCmd(m.ref, p.num, req)
	}

	switch m.screen {
	case screenHome:
		switch key {
		case "q", "Q":
			return m, tea.Quit
		case "enter":
			return m.openSelected()
		case "/":
			// На домівці фільтрувати нічого: «/» — це той самий «Пошук нового».
			return m.openSearch()
		case ",":
			m, _ = m.openSettings()
			return m, nil
		}

	case screenSettings:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		case "left", "h":
			m.cycleSetting(-1)
			return m, nil
		case "right", "l":
			m.cycleSetting(1)
			return m, nil
		}

	case screenSettingValue:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			if it, ok := m.list.SelectedItem().(item); ok {
				if p, ok := it.payload.(payloadSettingValue); ok {
					return m.pickSettingValue(p), nil
				}
			}
			return m, nil
		}

	case screenSearch:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			// Enter у полі вводу — шукати; Enter на результаті — відкрити
			if m.input.Focused() {
				q := m.input.Value()
				if q == "" {
					return m, nil
				}
				m.input.Blur()
				m.status = i18n.TuiSearching
				req := m.beginNav()
				m.query = q
				m.page, m.hasMore = 0, false
				return m, m.searchCmd(q, 1, req)
			}
			return m.openSelected()
		default:
			if m.input.Focused() {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			if key == "/" {
				m.input.SetValue("")
				return m, m.input.Focus()
			}
		}

	case screenEpisodes, screenHistory:
		switch key {
		case "esc":
			if m.list.FilterState() == list.FilterApplied {
				m.list.ResetFilter()
				return m, nil
			}
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		}

	case screenStudio:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		}

	case screenPlaying:
		if key == "esc" && m.playCancel != nil {
			m.playCancel()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	// Курсор ніколи не зупиняється на заголовку секції: він не робить нічого,
	// тож зупинка на ньому виглядає як зависання списку.
	m.skipHeaders(navDirection(key))
	return m, cmd
}

func (m Model) bookmarkSelected() (tea.Model, tea.Cmd) {
	var ref provider.TitleRef
	baseline := 0

	if m.screen == screenEpisodes {
		ref = m.ref
		baseline = maxEpisodeNumber(m.episodes)
	} else {
		it, ok := m.list.SelectedItem().(item)
		if !ok || it.header {
			return m, nil
		}
		switch payload := it.payload.(type) {
		case payloadTitle:
			ref, baseline = payload.ref, payload.epAired
		case payloadResume:
			ref = payload.ref
		default:
			return m, nil
		}
	}

	// Нуль у картці означає «невідомо», тому локальний кеш дає кращу
	// базову лінію без мережевого запиту й без запуску фонового узгодження.
	if baseline == 0 && m.eng != nil && m.eng.Store != nil {
		if episodes, _, found := m.eng.Store.LoadEpisodes(ref); found {
			baseline = maxEpisodeNumber(episodes)
		}
	}

	m.errText = ""
	result, err := m.eng.Bookmark(ref, baseline)
	if err != nil {
		m.errText = provider.CleanText(err.Error())
		return m, nil
	}
	var refreshCmd tea.Cmd
	switch m.screen {
	case screenHome:
		m.refreshHome()
	case screenSearch:
		// setItems завершується list.Select, а той працює у видимому
		// (відфільтрованому) просторі — тому тут саме Index(), не GlobalIndex():
		// searchRows перебудовує ті самі назви в тому ж порядку, і видима
		// позиція під фільтром не змінюється.
		refreshCmd = m.setItems(m.searchRows(), m.list.Index())
	}
	if result == library.BookmarkAdded {
		m.status = i18n.TuiBookmarkAdded
		title := m.eng.Lib.TitleByRef(ref)
		if title == nil {
			return m, refreshCmd
		}
		return m, tea.Batch(refreshCmd, m.bookmarkBaselineCmd(title.ID, ref, baseline))
	} else {
		m.status = i18n.TuiBookmarkRemoved
	}
	return m, refreshCmd
}

func maxEpisodeNumber(episodes []provider.Episode) int {
	maximum := 0
	for _, episode := range episodes {
		if episode.Number > maximum {
			maximum = episode.Number
		}
	}
	return maximum
}

// openSearch — вхід на екран пошуку; спільний для «Пошуку нового» і клавіші «/».
func (m Model) openSearch() (tea.Model, tea.Cmd) {
	m.stack = append(m.stack, m.snapshot())
	m.setScreen(screenSearch)
	_ = m.setItems(nil, 0)
	m.errText = ""
	m.status = ""
	m.query = ""
	m.cards, m.page, m.hasMore = nil, 0, false
	m.input.SetValue("")
	return m, m.input.Focus()
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.header {
		return m, nil
	}
	m.errText = ""
	switch p := it.payload.(type) {
	case payloadSearch:
		return m.openSearch()
	case payloadHistory:
		m.stack = append(m.stack, m.snapshot())
		m.showHistory()
		return m, nil
	case payloadSettings:
		m, _ = m.openSettings()
		return m, nil
	case payloadSetting:
		if len(m.settingValues(p.id)) < 2 {
			return m, nil
		}
		m.stack = append(m.stack, m.snapshot())
		m.showSettingValue(p.id)
		m.errText, m.status = "", ""
		return m, nil
	case payloadResume:
		snap := m.snapshot()
		req := m.beginNav()
		m.pending = &snap
		m.pendingReq = req
		m.ref = p.ref
		m.pendingEp = p.ep
		m.status = i18n.TuiResolving
		// серії підтягнемо у фоні, щоб після перегляду показати список
		return m, tea.Batch(
			m.resolveCmd(p.ref, p.ep, req, m.eng.ResolveHints(p.ref, p.ep)),
			m.episodesCmd(p.ref, req, false))
	case payloadTitle:
		snap := m.snapshot()
		req := m.beginNav()
		m.pending = &snap
		m.pendingReq = req
		m.ref = p.ref
		m.status = i18n.TuiSearching
		return m, m.episodesCmd(p.ref, req, true)
	case payloadMore:
		// Довантаження — теж навігаційна дія: свій req, старі відповіді летять у смітник.
		req := m.beginNav()
		m.status = i18n.TuiSearching
		return m, m.searchCmd(m.query, m.page+1, req)
	case payloadEp:
		req := m.beginNav()
		m.pendingEp = p.num
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, p.num, req, m.eng.ResolveHints(m.ref, p.num))
	case payloadStudio:
		if err := m.eng.PinStudio(m.ref, p.src.Studio); err != nil {
			m.errText = provider.CleanText(err.Error())
			return m, nil
		}
		req := m.beginNav()
		m.status = i18n.TuiResolving
		// підказки знімаються ПІСЛЯ PinStudio: новий пін має потрапити у вибір
		return m, m.resolveCmd(m.ref, m.pendingEp, req, m.eng.ResolveHints(m.ref, m.pendingEp))
	}
	return m, nil
}
