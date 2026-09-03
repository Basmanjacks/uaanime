package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
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
		title = m.episodesHeader()
	case screenPlaying:
		title = m.currentTitleName()
	case screenStudio:
		title = i18n.TuiStudioTitle
	case screenHistory:
		title = i18n.TuiHistoryItem
	case screenSettings:
		title = i18n.TuiSettingsTitle
	case screenSettingValue:
		title = settingTitle(m.settingID)
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
	if m.screen == screenPlaying {
		if line := m.liveLine(); line != "" {
			if m.w > 0 {
				line = truncate(line, m.w-2)
			}
			body += styleEta.Render(line) + "\n"
		}
		if line := m.remoteLine(); line != "" {
			body += line + "\n"
		}
		// QR під адресою: навести камеру простіше, ніж набрати 70 символів
		// руками. Рахуємо вже намальовані рядки плюс підказку внизу — код
		// з'являється тільки тоді, коли нічого з них не витісняє.
		if block, ok := m.remoteQR(strings.Count(body, "\n") + hintBlockLines); ok {
			body += block + "\n"
		}
	} else {
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
	// Помилки й статуси несуть текст ззовні (шляхи, адреси, відповіді сайту):
	// чистимо в одному місці замість кожного продюсера.
	switch {
	case m.errText != "":
		body += styleErr.Render(fit(provider.CleanText(m.errText)))
	case m.status != "":
		body += styleStatus.Render(fit(provider.CleanText(m.status)))
	default:
		body += styleHint.Render(fit(m.hint()))
	}

	v := tea.NewView(body)
	// Заголовок вікна ставимо лише під час перегляду: на решті екранів людина
	// й так дивиться в термінал. Порожнє значення рендерер скидає сам.
	if m.screen == screenPlaying {
		v.WindowTitle = fmt.Sprintf(i18n.TuiWindowTitle, m.currentTitleName(), m.pendingEp)
	}
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

// episodesHeader — назва тайтла плюс хвіст із метаданих: скільки лишилось і
// яка озвучка закріплена. Хвіст коштує колонок, тому у вузькому вікні частини
// відкидаються зліва направо, поки назві не лишиться менше за minTitleName.
func (m Model) episodesHeader() string {
	pin := m.studioPin()
	if pin == "" {
		pin = i18n.TuiStudioAuto
	}
	parts := make([]string, 0, 2)
	if remaining := m.remainingLabel(); remaining != "" {
		parts = append(parts, remaining)
	}
	parts = append(parts, fmt.Sprintf(i18n.TuiStudioPinned, pin))

	limit := m.w - 2
	tail := metaTail(parts)
	for len(parts) > 0 && limit-lipgloss.Width(tail) < minTitleName {
		parts = parts[1:]
		tail = metaTail(parts)
	}
	nameWidth := limit - lipgloss.Width(tail)
	if nameWidth < 8 {
		nameWidth = 8
	}
	return truncate(truncate(m.currentTitleName(), nameWidth)+tail, limit)
}

func metaTail(parts []string) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(styleMetaSep.Render(metaSep))
		b.WriteString(styleMeta.Render(part))
	}
	return b.String()
}

// liveLine — усе, що екран «Грає» знає про сесію: коли вона закінчиться і на
// якій гучності грає. Гучність показується завжди, коли плеєр її повідомив:
// це єдине підтвердження, що клавіші «+»/«−» справді дійшли.
func (m Model) liveLine() string {
	parts := make([]string, 0, 2)
	if eta := m.etaLine(); eta != "" {
		parts = append(parts, eta)
	}
	if m.live.Playing && m.live.VolumePct >= 0 {
		parts = append(parts, fmt.Sprintf(i18n.TuiVolume, int(math.Round(m.live.VolumePct))))
	}
	return strings.Join(parts, metaSep)
}

// etaLine — коли серія закінчиться при поточній позиції. Без відомої
// тривалості рядка немає: «закінчиться колись» — не інформація. На паузі час
// усе одно показується (він перераховується на кожному тіку), але з позначкою,
// інакше застигла оцінка виглядала б як зависання.
func (m Model) etaLine() string {
	if !m.live.Playing || m.live.DurationSec <= 0 {
		return ""
	}
	left := max(m.live.DurationSec-m.live.PositionSec, 0)
	at := m.now().Add(time.Duration(left * float64(time.Second))).Format("15:04")
	if m.live.Paused {
		return fmt.Sprintf(i18n.TuiFinishAtPaused, at)
	}
	return fmt.Sprintf(i18n.TuiFinishAt, at)
}

// remoteLine — адреса пульта на екрані «Грає». Обрізаний URL гірший за
// жоден (половина токена нікуди не веде), тому щаблі такі: підписана адреса,
// гола адреса, те саме за IP (він коротший за mDNS-ім'я і веде туди ж), і лише
// потім — підказка, де адресу взяти.
func (m Model) remoteLine() string {
	if m.remote.URL == "" && m.remote.AltURL == "" {
		return ""
	}
	limit := m.w - 2
	if m.w <= 0 {
		limit = 0
	}
	var variants []string
	for _, url := range []string{m.remote.URL, m.remote.AltURL} {
		if url == "" {
			continue
		}
		variants = append(variants, fmt.Sprintf(i18n.TuiRemote, url), url)
	}
	for _, text := range variants {
		if limit == 0 || lipgloss.Width(text) <= limit {
			return styleRemote.Render(text)
		}
	}
	return styleRemote.Render(i18n.TuiRemoteNarrow)
}

// remoteURL — найкоротша з адрес пульта. Для камери телефона обидві рівноцінні,
// а коротша дає меншу версію символу, тобто більший шанс, що QR узагалі влізе
// в термінал; IP майже завжди коротший за mDNS-ім'я.
func (m Model) remoteURL() string {
	url := m.remote.URL
	if alt := m.remote.AltURL; alt != "" && (url == "" || len(alt) < len(url)) {
		url = alt
	}
	return url
}

// hintBlockLines — висота підказки/статусу внизу кадру: порожній рядок відступу
// плюс сам текст. QR не має права з'їсти ці рядки.
const hintBlockLines = 2

// remoteQR — QR-код адреси пульта під рядком з адресою; usedRows — скільки
// рядків кадру вже зайнято. Напівблоки не мають ASCII-заміни, тому в
// ASCII-режимі лишається сам текстовий рядок.
func (m Model) remoteQR(usedRows int) (string, bool) {
	if m.ic.ASCII || m.w <= 0 || m.h <= 0 {
		return "", false
	}
	block, ok := qrBlock(m.remoteURL(), m.w-2, m.h-usedRows)
	if !ok {
		return "", false
	}
	return styleQR.Render(block), true
}

// hintPlayingNarrow — ширина, нижче якої повна підказка «Грає» вже не влазить
// і починає обрізатися на півслові.
const hintPlayingNarrow = 76

func (m Model) hint() string {
	switch m.screen {
	case screenSearch:
		return i18n.TuiHintSearch
	case screenPlaying:
		if m.w > 0 && m.w < hintPlayingNarrow {
			return i18n.TuiHintPlayingNarrow
		}
		return i18n.TuiHintPlaying
	case screenEpisodes:
		return i18n.TuiHintEpisodes
	case screenStudio:
		return i18n.TuiHintStudio
	case screenHistory:
		return i18n.TuiHintList
	case screenSettings:
		return i18n.TuiHintSettings
	case screenSettingValue:
		return i18n.TuiHintSettingsPick
	default:
		return i18n.TuiHintHome
	}
}
