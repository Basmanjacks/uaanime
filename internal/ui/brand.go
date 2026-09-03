// The banner is the only ASCII art sanctioned by AGENTS.md: home screen only,
// with a mandatory one-line fallback.
package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

// brandOrnamentMark — місце під сезонну руну в шаблоні банера. Орнамент живе
// лише в порожньому верхньому рядку: літери мусять лишатися тими самими в усі
// пори року, інакше це вже інший логотип.
const brandOrnamentMark = "#"

var brandTemplate = []string{
	" #                       _           # ",
	" _  _  __ _  __ _  _ _  (_) _ __   ___ ",
	"| || |/ _` |/ _` || ' \\ | || '  \\ / -_)",
	" \\_,_|\\__,_|\\__,_||_||_||_||_|_|_|\\___|",
}

const brandChromeHeight = 8
const brandMinListRows = 10
const brandSplitColumn = 12

// brandNow — шов для тестів: сезон рахується від локальної дати, а не від
// того, коли комусь заманулося запустити тести.
var brandNow = time.Now

type season int

const (
	seasonWinter season = iota
	seasonSpring
	seasonSummer
	seasonAutumn
)

// seasonFor — сезони метеорологічні: грудень уже зима, вересень уже осінь.
// Астрономічні дати рівнодень тут нічого не дають, а «зима з першого грудня»
// збігається з тим, як про пори року говорять.
func seasonFor(t time.Time) season {
	switch t.Month() {
	case time.December, time.January, time.February:
		return seasonWinter
	case time.March, time.April, time.May:
		return seasonSpring
	case time.June, time.July, time.August:
		return seasonSummer
	default:
		return seasonAutumn
	}
}

// brandOrnament — руна сезону в двох наборах. Усі вони односмугові: ширина
// банера закладена в brandWidth(), тому широкий символ (а емодзі широкі
// завжди) зсунув би рамку всього екрана.
type brandOrnament struct{ unicode, ascii string }

var brandOrnaments = map[season]brandOrnament{
	seasonWinter: {"❄", "*"},
	seasonSpring: {"✿", "+"},
	seasonSummer: {"☀", "o"},
	seasonAutumn: {"✦", "."},
}

// brandVariant — банер із підставленим орнаментом. Підстановка, а не окремі
// рядки на сезон: так усі варіанти однакової висоти й ширини за побудовою, а
// не за домовленістю.
func brandVariant(o brandOrnament, ascii bool) []string {
	ornament := o.unicode
	if ascii {
		ornament = o.ascii
	}
	lines := make([]string, len(brandTemplate))
	for i, line := range brandTemplate {
		lines[i] = strings.ReplaceAll(line, brandOrnamentMark, ornament)
	}
	return lines
}

// brandBanner — банер, який побачить саме цей запуск: сезон за датою, набір
// символів за UAANIME_ASCII.
func (m *Model) brandBanner() []string {
	return brandVariant(brandOrnaments[seasonFor(brandNow())], m.ic.ASCII)
}

// brandBannerWidth рахується з шаблону: орнамент — рівно одна комірка, тож
// ширина всіх сезонних варіантів однакова й дорівнює ширині шаблону.
var brandBannerWidth = func() int {
	w := 0
	for _, line := range brandTemplate {
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
	banner := m.brandBanner()
	lines := make([]string, 0, len(banner))
	for _, line := range banner {
		left, right := splitAtDisplayColumn(line, brandSplitColumn)
		rendered := styleBrandUA.Render(left)
		if right != "" {
			rendered += styleBrandRest.Render(right)
		}
		lines = append(lines, rendered)
	}
	framed := styleBanner.UnsetForeground().Render(strings.Join(lines, "\n"))
	return framed + "\n" +
		styleTagline.Render(strings.ToUpper(i18n.TuiTagline)) + "\n\n"
}

func (m *Model) brandFallbackTitle() string {
	title := []rune(i18n.TuiAppTitle)
	cut := min(2, len(title))
	brand := styleBrandUA.Render(m.ic.Spark+" "+string(title[:cut])) +
		styleBrandRest.Render(string(title[cut:]))
	tagline := styleTagline.Padding(0).Render(metaSep + strings.ToUpper(i18n.TuiTaglineShort))
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
