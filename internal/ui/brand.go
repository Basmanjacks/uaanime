// The banner is the only ASCII art sanctioned by AGENTS.md: home screen only,
// with a mandatory one-line fallback.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

var brandBanner = []string{
	"                         _             ",
	" _  _  __ _  __ _  _ _  (_) _ __   ___ ",
	"| || |/ _` |/ _` || ' \\ | || '  \\ / -_)",
	" \\_,_|\\__,_|\\__,_||_||_||_||_|_|_|\\___|",
}

const brandChromeHeight = 8
const brandMinListRows = 10
const brandSplitColumn = 12

var brandBannerWidth = func() int {
	w := 0
	for _, line := range brandBanner {
		w = max(w, lipgloss.Width(line))
	}
	return w
}()

func brandWidth() int {
	return brandBannerWidth
}

func (m *Model) bannerVisible() bool {
	return m.screen == screenHome && m.w >= brandWidth()+4 && m.h >= brandChromeHeight+brandMinListRows
}

func (m *Model) brandHeader() string {
	lines := make([]string, 0, len(brandBanner))
	for _, line := range brandBanner {
		left, right := splitAtDisplayColumn(line, brandSplitColumn)
		rendered := styleBrandUA.Render(left)
		if right != "" {
			rendered += styleBrandRest.Render(right)
		}
		lines = append(lines, rendered)
	}
	banner := styleBanner.UnsetForeground().Render(strings.Join(lines, "\n"))
	return banner + "\n" +
		styleTagline.Render(i18n.TuiTagline) + "\n\n"
}

func (m *Model) brandFallbackTitle() string {
	title := []rune(i18n.TuiAppTitle)
	cut := min(2, len(title))
	brand := styleBrandUA.Render(m.ic.Spark+" "+string(title[:cut])) +
		styleBrandRest.Render(string(title[cut:]))
	tagline := styleTagline.Padding(0).Render(metaSep + i18n.TuiTaglineShort)
	return lipgloss.NewStyle().Padding(0, 0, 1, 2).Render(brand + tagline)
}

func splitAtDisplayColumn(s string, column int) (string, string) {
	if lipgloss.Width(s) < column {
		return s, ""
	}

	runes := []rune(s)
	width := 0
	for i, r := range runes {
		runeWidth := lipgloss.Width(string(r))
		if width+runeWidth > column {
			return string(runes[:i]), string(runes[i:])
		}
		width += runeWidth
		if width == column {
			return string(runes[:i+1]), string(runes[i+1:])
		}
	}
	return s, ""
}
