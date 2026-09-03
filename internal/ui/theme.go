// Усі кольори й відступи TUI живуть тут. У решті коду жодного хардкоду кольору.
package ui

import (
	"image/color"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
	"github.com/charmbracelet/x/ansi"
)

// AdaptiveColor прибрано у v2: світлий/темний фон обираємо явно.
// Світлий термінал — не крайній випадок, це половина людей.
func pick(light, dark string) color.Color {
	if compat.HasDarkBackground {
		return lipgloss.Color(dark)
	}
	return lipgloss.Color(light)
}

// Тепла нейтральна палітра зі світлою й темною парами однакового контрасту.
var (
	colAccent   = pick("#AE4E24", "#D97757") // основний акцент
	colFgBright = pick("#141210", "#FBF8F3") // яскравий основний текст
	colFg       = pick("#33302B", "#E3DED5") // основний текст рядка
	colDim      = pick("#6F6960", "#9A9389") // другорядний текст
	colFaint    = pick("#ADA69B", "#5C574F") // ледь помітне: неактивні крапки пагінації
	colOK       = pick("#3F7D4F", "#93C79A") // переглянуто
	colWarn     = pick("#B02A37", "#E8757F") // помилки
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colFgBright).Padding(0, 0, 1, 2)
	styleBanner  = lipgloss.NewStyle().Foreground(colAccent).PaddingLeft(2)
	styleTagline = lipgloss.NewStyle().Foreground(colDim).PaddingLeft(2)
	styleHint    = lipgloss.NewStyle().Foreground(colDim).Padding(1, 0, 0, 2)
	styleStatus  = lipgloss.NewStyle().Foreground(colDim).Padding(1, 0, 0, 2)
	styleErr     = lipgloss.NewStyle().Foreground(colWarn).Padding(1, 0, 0, 2)

	// Рядок списку. Вибране відрізняється яскравістю, а не фоном:
	// інверсія фону в терміналі з власною темою виглядає як артефакт.
	styleRow         = lipgloss.NewStyle().Foreground(colFg)
	styleRowSel      = lipgloss.NewStyle().Foreground(colFgBright)
	styleMeta        = lipgloss.NewStyle().Foreground(colDim)
	styleRemote      = lipgloss.NewStyle().Foreground(colDim).Padding(0, 0, 0, 2)
	styleEta         = lipgloss.NewStyle().Foreground(colDim).Padding(0, 0, 0, 2)
	styleMetaSel     = lipgloss.NewStyle().Foreground(colFg)
	styleMetaSep     = lipgloss.NewStyle().Foreground(colFaint)
	styleMetaKey     = lipgloss.NewStyle().Foreground(colFg)
	styleMetaKeySel  = lipgloss.NewStyle().Foreground(colFgBright)
	styleIconAccent  = lipgloss.NewStyle().Foreground(colAccent)
	styleBrandUA     = lipgloss.NewStyle().Foreground(colAccent)
	styleBrandRest   = lipgloss.NewStyle().Foreground(colFg)
	styleSectionName = lipgloss.NewStyle().Foreground(colDim)
	styleRule        = lipgloss.NewStyle().Foreground(colFaint)
	styleBadge       = lipgloss.NewStyle().Foreground(colOK)
	styleCursor      = lipgloss.NewStyle().Foreground(colAccent)
	styleMatch       = lipgloss.NewStyle().Underline(true)
)

// bullet — крапка пагінації; список тримає свою копію приватною.
const bullet = "•"

const ruleWidth = 40

// На широких терміналах список лишається в читабельній мірі, а не
// розтягується на весь екран.
const contentCap = 92

// ellipsis — символ обрізання рядка.
const ellipsis = "…"

// ratingMark — позначка оцінки в мета-рядку картки. Звичайний Unicode
// шириною в одну комірку, як і решта іконок: без Nerd Font і без емодзі.
const ratingMark = "★"

// metaSep — роздільник частин мета-рядка. Крапка з пробілами читається як
// пауза, а не як пунктуація назви.
const metaSep = " · "

// listStyles — стилі самого списку. Дефолти bubbles рожеві (#EE6FF8) і
// нечутливі до теми, тому все видиме перекриваємо палітрою.
//
// Крапки пагінації список копіює у Paginator у момент list.New, тому після
// SetStyles їх треба ще раз проставити у m.Paginator (див. New у ui.go).
func listStyles() list.Styles {
	s := list.DefaultStyles(compat.HasDarkBackground)

	s.NoItems = lipgloss.NewStyle().Foreground(colDim)

	s.PaginationStyle = lipgloss.NewStyle().Foreground(colDim).PaddingLeft(2)
	s.ActivePaginationDot = lipgloss.NewStyle().Foreground(colAccent).SetString(bullet)
	s.InactivePaginationDot = lipgloss.NewStyle().Foreground(colFaint).SetString(bullet)
	s.ArabicPagination = lipgloss.NewStyle().Foreground(colDim)

	prompt := lipgloss.NewStyle().Foreground(colDim)
	s.Filter.Focused.Prompt = prompt
	s.Filter.Blurred.Prompt = prompt
	s.Filter.Focused.Text = lipgloss.NewStyle().Foreground(colFg)
	s.Filter.Blurred.Text = lipgloss.NewStyle().Foreground(colFg)
	s.Filter.Focused.Placeholder = lipgloss.NewStyle().Foreground(colFaint)
	s.Filter.Blurred.Placeholder = lipgloss.NewStyle().Foreground(colFaint)
	s.Filter.Cursor.Color = colAccent

	s.DefaultFilterCharacterMatch = styleMatch

	return s
}

func searchInputStyles() textinput.Styles {
	st := textinput.StyleState{
		Prompt:      lipgloss.NewStyle().Foreground(colDim),
		Text:        lipgloss.NewStyle().Foreground(colFg),
		Placeholder: lipgloss.NewStyle().Foreground(colFaint),
		Suggestion:  lipgloss.NewStyle().Foreground(colFaint),
	}
	return textinput.Styles{
		Focused: st, Blurred: st,
		Cursor: textinput.CursorStyle{Color: colAccent, Shape: tea.CursorBar, Blink: true},
	}
}

// Іконки — звичайний Unicode, без Nerd Font і без емодзі: емодзі займають
// дві комірки й ламають вирівнювання колонки. За UAANIME_ASCII=1 — чистий ASCII.
type icons struct {
	Play, Done, Pending, Search, Cursor, Spark, Rule, Settings string
	// ASCII — чи це чистий ASCII-набір. Прапорець потрібен тим, хто малює
	// власні символи поза цією структурою (орнамент банера): вгадувати режим
	// за виглядом окремої іконки — це другий, розсинхронізований, детектор.
	ASCII bool
}

// Settings — U+2699 без VS16: із селектором емодзі це гарантовані дві комірки.
func themeIcons(ascii bool) icons {
	if ascii {
		return icons{Play: ">", Done: "v", Pending: "-", Search: "+", Cursor: ">", Spark: "*", Rule: "-", Settings: "*", ASCII: true}
	}
	return icons{Play: "▶", Done: "✓", Pending: "·", Search: "+", Cursor: "❯", Spark: "✳", Rule: "─", Settings: "⚙"}
}

// padIcon доповнює рядок пробілами до ширини w у комірках терміналу.
// Ширину міряємо через lipgloss: рунa ≠ комірка (емодзі — дві, ANSI — нуль).
func padIcon(s string, w int) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	return s + strings.Repeat(" ", gap)
}

// truncate обрізає рядок до w комірок, лишаючи «…» замість хвоста.
// ANSI-послідовності не рахуються як видимі комірки й не розриваються.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return ellipsis
	}
	return ansi.Truncate(s, w, ellipsis)
}
