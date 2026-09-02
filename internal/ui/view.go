package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

// ---- view ----

// currentTitleName — назва відкритого тайтлу: спершу з бібліотеки, далі з
// посилання, і лише як остання межа — назва застосунку.
func (m Model) currentTitleName() string {
	if t := m.eng.Lib.TitleByRef(m.ref); t != nil && t.Name != "" {
		return t.Name
	}
	if m.ref.Name != "" {
		return m.ref.Name
	}
	return i18n.TuiAppTitle
}

func (m Model) View() tea.View {
	var title string
	switch m.screen {
	case screenSearch:
		title = i18n.TuiSearchTitle
	case screenEpisodes:
		title = m.currentTitleName()
		pin := m.studioPin()
		if pin == "" {
			pin = i18n.TuiStudioAuto
		}
		tail := styleMetaSep.Render(metaSep) + styleMeta.Render(fmt.Sprintf(i18n.TuiStudioPinned, pin))
		nameWidth := m.w - 2 - lipgloss.Width(tail)
		if nameWidth < 8 {
			nameWidth = 8
		}
		title = truncate(truncate(title, nameWidth)+tail, m.w-2)
	case screenPlaying:
		title = m.currentTitleName()
	case screenStudio:
		title = i18n.TuiStudioTitle
	case screenHistory:
		title = i18n.TuiHistoryItem
	default:
		title = i18n.TuiAppTitle
	}
	// Жоден рядок не має бути ширшим за термінал: перенесення зсуває кадр і
	// ховає нижній рядок. Заголовок і підказка/статус обрізаються тут, список
	// обрізає делегат, а банер має власний fallback.
	if m.w > 0 {
		title = truncate(title, m.w-2)
	}

	var body string
	if m.screen == screenHome {
		if m.bannerVisible() {
			body = m.brandHeader()
		} else {
			body = m.brandFallbackTitle() + "\n"
		}
	} else {
		body = styleTitle.Render(title) + "\n"
	}
	if m.screen == screenSearch {
		body += "  " + m.input.View() + "\n"
	}
	if m.screen != screenPlaying {
		listView := m.list.View()
		if len(m.list.Items()) == 0 {
			// Не даємо bubbles показати англійське «No items.» і тримаємо
			// геометрію сталою навіть до появи результатів.
			listView = lipgloss.NewStyle().Height(m.listHeight()).Render(styleStatus.Render(""))
		}
		body += listView + "\n"
	}

	fit := func(s string) string {
		if m.w > 0 {
			return truncate(s, m.w-2)
		}
		return s
	}
	switch {
	case m.errText != "":
		body += styleErr.Render(fit(m.errText))
	case m.status != "":
		body += styleStatus.Render(fit(m.status))
	default:
		body += styleHint.Render(fit(m.hint()))
	}

	v := tea.NewView(body)
	if m.screen == screenSearch {
		if c := m.input.Cursor(); c != nil {
			c.X += 2
			c.Y += 2
			v.Cursor = c
		}
	}
	v.AltScreen = true
	return v
}

func (m Model) hint() string {
	switch m.screen {
	case screenSearch:
		return i18n.TuiHintSearch
	case screenEpisodes:
		return i18n.TuiHintEpisodes
	case screenStudio:
		return i18n.TuiHintStudio
	case screenHistory:
		return i18n.TuiHintList
	default:
		return i18n.TuiHintHome
	}
}
