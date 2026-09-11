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
		title = i18n.TuiPickerTitle
	case screenBookmarks:
		title = i18n.TuiBookmarksTitle
	case screenHistory:
		title = i18n.TuiHistoryItem
	case screenSettings:
		title = i18n.TuiSettingsTitle
	case screenSettingValue:
		title = settingTitle(m.settingID)
	default:
		title = i18n.TuiAppTitle
	}
	if m.overlay == overlayHelp {
		title = i18n.TuiHelpTitle
	}
	if m.overlay == overlayBudget {
		title = i18n.TuiBudgetTitle
	}
	// Жоден рядок не має бути ширшим за термінал: перенесення зсуває кадр і
	// ховає нижній рядок. Заголовок і підказка/статус обрізаються тут, список
	// обрізає делегат, а банер має власний fallback.
	if m.w > 0 {
		title = truncate(title, m.w-2)
	}

	var body string
	if m.screen == screenHome && m.overlay == overlayNone {
		if m.bannerVisible() {
			body = m.brandHeader()
		} else {
			body = m.brandFallbackTitle() + "\n"
		}
	} else {
		body = styleTitle.Render(title) + "\n"
	}
	if m.overlay == overlayBudget {
		for _, line := range m.budgetNoteLines() {
			body += styleRemote.Render(line) + "\n"
		}
	}
	if m.screen == screenSearch && m.overlay == overlayNone {
		body += "  " + m.input.View() + "\n"
	}
	if m.screen == screenPlaying && m.overlay == overlayNone {
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
			empty := ""
			if m.screen == screenBookmarks {
				empty = i18n.TuiBookmarksEmpty
				if m.w > 0 {
					empty = truncate(empty, m.w-2)
				}
			}
			listView = lipgloss.NewStyle().Height(m.listHeight()).Render(styleStatus.Render(empty))
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
	case m.journalWarning:
		body += styleErr.Render(fit(i18n.MsgJournalFailed))
	case m.overlay == overlayBudget:
		body += styleHint.Render(fit(m.actionHint()))
	case m.overlay == overlayHelp:
		body += styleHint.Render(fit(m.actionHint()))
	case m.errText != "":
		body += styleErr.Render(fit(provider.CleanText(m.errText)))
	case m.baselineWarning:
		body += styleErr.Render(fit(i18n.TuiBaselineFailed))
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

// episodesHeader — назва тайтла плюс хвіст із метаданих: скільки лишилось,
// приблизно скільки це часу і який реліз закріплено. Хвіст коштує колонок,
// тому у вузькому вікні частини відкидаються за цінністю: спершу оцінка часу
// (похідна від кількості), потім кількість, і лише тоді пін — поки назві не
// лишиться менше за minTitleName.
func (m Model) episodesHeader() string {
	remaining := m.remainingParts()
	eta := ""
	if len(remaining) > 1 {
		eta = "~" + remaining[1] // середнє, а не точна тривалість
		remaining = remaining[:1]
	}
	pin := fmt.Sprintf(i18n.TuiStudioPinned, m.pinLabel())
	assemble := func() []string {
		parts := append([]string{}, remaining...)
		if eta != "" {
			parts = append(parts, eta)
		}
		return append(parts, pin)
	}

	limit := m.w - 2
	parts := assemble()
	tail := metaTail(parts)
	for len(parts) > 0 && limit-lipgloss.Width(tail) < minTitleName {
		switch {
		case eta != "":
			eta = ""
		case len(remaining) > 0:
			remaining = nil
		default:
			pin = ""
		}
		parts = assemble()
		if pin == "" {
			parts = nil
		}
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
	if !m.live.Playing {
		return ""
	}
	pos := m.displayPosition()
	clock := func(v float64) string { return fmt.Sprintf("%02d:%02d", int(v)/60, int(v)%60) }
	// Реліз стоїть одразу після номера серії: «в чому грає» — це те, заради
	// чого сюди дивляться після Enter. Скидається лише у зовсім вузькому вікні,
	// після повного годинника й перед скороченням «Серія» до «С.».
	release := ""
	if m.live.Studio != "" {
		release = m.live.Studio + metaSep + i18n.KindShort(m.live.Kind)
	}
	tail := []string{}
	if m.live.Paused {
		tail = append(tail, i18n.TuiPaused)
	}
	if m.live.SessionLimited {
		tail = append(tail, fmt.Sprintf(i18n.TuiBudgetRemaining, m.live.SessionRemaining))
	}
	build := func(episode, rel, clk string) []string {
		out := []string{episode}
		if rel != "" {
			out = append(out, rel)
		}
		out = append(out, clk)
		return append(out, tail...)
	}
	fits := func(p []string) bool { return m.w <= 0 || lipgloss.Width(strings.Join(p, metaSep)) <= m.w-2 }
	full := clock(pos)
	if m.live.DurationSec > 0 {
		full += " / " + clock(m.live.DurationSec)
	}
	parts := build(fmt.Sprintf(i18n.TuiEpisodeNo, m.pendingEp), release, full)
	if !fits(parts) && m.live.DurationSec > 0 {
		parts = build(fmt.Sprintf(i18n.TuiEpisodeNo, m.pendingEp), release, clock(pos))
	}
	if !fits(parts) && release != "" {
		parts = build(fmt.Sprintf(i18n.TuiEpisodeNo, m.pendingEp), "", clock(pos))
	}
	if !fits(parts) {
		if m.live.SessionLimited {
			tail[len(tail)-1] = fmt.Sprintf(i18n.TuiBudgetShort, m.live.SessionRemaining)
		}
		parts = build(fmt.Sprintf(i18n.TuiEpisodeShort, m.pendingEp), "", clock(pos))
	}
	extras := []string{}
	if m.live.VolumePct >= 0 {
		extras = append(extras, fmt.Sprintf(i18n.TuiVolume, int(math.Round(m.live.VolumePct))))
	}
	if eta := m.etaLine(); eta != "" {
		extras = append(extras, eta)
	}
	for _, extra := range extras {
		line := strings.Join(append(parts, extra), metaSep)
		if m.w > 0 && lipgloss.Width(line) > m.w-2 {
			continue
		}
		parts = append(parts, extra)
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
	left := max(m.live.DurationSec-m.displayPosition(), 0)
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

func (m Model) hint() string { return m.actionHint() }
