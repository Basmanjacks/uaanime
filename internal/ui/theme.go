// Усі кольори й відступи TUI живуть тут. У решті коду жодного хардкоду кольору.
package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// AdaptiveColor прибрано у v2: світлий/темний фон обираємо явно.
// Світлий термінал — не крайній випадок, це половина людей.
func pick(light, dark string) color.Color {
	if compat.HasDarkBackground {
		return lipgloss.Color(dark)
	}
	return lipgloss.Color(light)
}

var (
	colAccent = pick("#8839ef", "#cba6f7") // акцент (заголовок, вибране)
	colDim    = pick("#6c6f85", "#7f849c") // другорядний текст
	colOK     = pick("#40a02b", "#a6e3a1") // переглянуто
	colWarn   = pick("#d20f39", "#f38ba8") // помилки

	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(colAccent).Padding(0, 0, 1, 2)
	styleHint   = lipgloss.NewStyle().Foreground(colDim).Padding(1, 0, 0, 2)
	styleStatus = lipgloss.NewStyle().Foreground(colDim).Padding(1, 0, 0, 2)
	styleErr    = lipgloss.NewStyle().Foreground(colWarn).Padding(1, 0, 0, 2)
	styleDone   = lipgloss.NewStyle().Foreground(colOK)
)

// Іконки — звичайний Unicode, без Nerd Font; за UAANIME_ASCII=1 — чистий ASCII.
type icons struct {
	Play, Done, Search string
}

func themeIcons(ascii bool) icons {
	if ascii {
		return icons{Play: ">", Done: "v", Search: "/"}
	}
	return icons{Play: "▶", Done: "✓", Search: "🔍"}
}
