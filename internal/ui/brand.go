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

// brandArt — шаблон банера разом із його геометрією. Дві геометрії, а не одна:
// жирні box-drawing штрихи в чистому ASCII не існують, тому ASCII-режим лишає
// той самий figlet у штрихових символах.
type brandArt struct {
	// lines — рядки шаблону з brandOrnamentMark на місці орнаменту.
	lines []string
	// split — колонка, з якої починається «anime»: ліворуч акцентне «ua»,
	// праворуч решта. Це межа кольорів між гліфами, а не порожня колонка: у
	// figlet вона проходить по стійці першої «a» та діагоналі другої.
	split int
	// width — ширина всіх рядків; рахується з lines при ініціалізації.
	width int
}

func newBrandArt(split int, lines []string) brandArt {
	w := 0
	for _, line := range lines {
		w = max(w, lipgloss.Width(line))
	}
	return brandArt{lines: lines, split: split, width: w}
}

// brandBlock — той самий figlet «small», що й brandASCII, але з жирними
// штрихами, щоб контур не губився на тлі: _ → ▁ (товста базова лінія на тому ж
// місці), | → ┃, / \ → ╱ ╲ (діагоналі на всю комірку; жирних діагоналей в
// Unicode немає), ' ` → ╹, , → ▁, ( ) → ▗ ▖, - → ━. Не bold SGR: моноширинний
// bold майже не товщає, а bold-гарнітури часто не мають box-drawing гліфів.
// Геометрія збігається з brandASCII за побудовою. Перевірено 2026-09-17.
var brandBlock = newBrandArt(12, []string{
	" #                       ▁           # ",
	" ▁  ▁  ▁▁ ▁  ▁▁ ▁  ▁ ▁  ▗▁▖ ▁ ▁▁   ▁▁▁ ",
	"┃ ┃┃ ┃╱ ▁╹ ┃╱ ▁╹ ┃┃ ╹ ╲ ┃ ┃┃ ╹  ╲ ╱ ━▁▖",
	" ╲▁▁▁┃╲▁▁▁▁┃╲▁▁▁▁┃┃▁┃┃▁┃┃▁┃┃▁┃▁┃▁┃╲▁▁▁┃",
})

// brandASCII — figlet «small» у чистому ASCII, для UAANIME_ASCII=1.
var brandASCII = newBrandArt(12, []string{
	" #                       _           # ",
	" _  _  __ _  __ _  _ _  (_) _ __   ___ ",
	"| || |/ _` |/ _` || ' \\ | || '  \\ / -_)",
	" \\_,_|\\__,_|\\__,_||_||_||_||_|_|_|\\___|",
})

func brandArtFor(ascii bool) brandArt {
	if ascii {
		return brandASCII
	}
	return brandBlock
}

// brandChromeExtra — рядки хрому понад сам арт: тагдайн, порожній рядок і два
// рядки, які chromeBase резервує під підказку внизу.
const brandChromeExtra = 4
const brandMinListRows = 10

// brandChromeHeight — скільки рядків забирає домашній екран із банером у цьому
// наборі символів: геометрії різної висоти, тож і хром різний.
func brandChromeHeight(ascii bool) int {
	return len(brandArtFor(ascii).lines) + brandChromeExtra
}

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

// brandCatOrnament — орнамент пасхалки «ня». Геометрія та сама, що в сезонів:
// кіт не має права зсунути кадр.
var brandCatOrnament = brandOrnament{"ᗢ", "^"}

// brandVariant — банер із підставленим орнаментом. Підстановка, а не окремі
// рядки на сезон: так усі варіанти однакової висоти й ширини за побудовою, а
// не за домовленістю.
func brandVariant(o brandOrnament, ascii bool) []string {
	ornament := o.unicode
	if ascii {
		ornament = o.ascii
	}
	art := brandArtFor(ascii)
	lines := make([]string, len(art.lines))
	for i, line := range art.lines {
		lines[i] = strings.ReplaceAll(line, brandOrnamentMark, ornament)
	}
	return lines
}

// brandOrnamentFor — руна, яку побачить саме цей запуск: пасхалка або сезон за
// датою.
func (m *Model) brandOrnamentFor() brandOrnament {
	if m.nya {
		return brandCatOrnament
	}
	return brandOrnaments[seasonFor(brandNow())]
}

// brandOrnamentGlyph — та сама руна в наборі символів за UAANIME_ASCII.
func (m *Model) brandOrnamentGlyph() string {
	o := m.brandOrnamentFor()
	if m.ic.ASCII {
		return o.ascii
	}
	return o.unicode
}

// brandBanner — банер без стилів: текст, який має побачити користувач.
func (m *Model) brandBanner() []string {
	return brandVariant(m.brandOrnamentFor(), m.ic.ASCII)
}

// brandWidth — ширина банера в цьому наборі символів: орнамент — рівно одна
// комірка, тож усі сезонні варіанти однієї геометрії однакової ширини.
func brandWidth(ascii bool) int {
	return brandArtFor(ascii).width
}

func (m *Model) bannerVisible() bool {
	return m.screen == screenHome && m.w >= brandWidth(m.ic.ASCII)+4 && m.h >= brandChromeHeight(m.ic.ASCII)+brandMinListRows
}

func (m *Model) brandHeader() string {
	art := brandArtFor(m.ic.ASCII)
	ornament := m.brandOrnamentGlyph()
	lines := make([]string, 0, len(art.lines))
	for _, line := range art.lines {
		lines = append(lines, renderBrandLine(line, art.split, ornament))
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

// renderBrandLine — рядок шаблону у двох стилях: «ua» до колонки split,
// «anime» після. Орнамент отримує стиль своєї колонки, як і літери поруч.
// Рендеримо з шаблону, а не з готового тексту, посегментно: так орнамент
// будь-якої ширини не зсуває межу кольорів.
func renderBrandLine(line string, split int, ornament string) string {
	var out, seg strings.Builder
	col, segStart := 0, 0
	flush := func() {
		if seg.Len() > 0 {
			out.WriteString(brandStyleAt(segStart, split).Render(seg.String()))
			seg.Reset()
		}
		segStart = col
	}
	for _, r := range line {
		if string(r) == brandOrnamentMark {
			flush()
			out.WriteString(brandStyleAt(col, split).Render(ornament))
			col += lipgloss.Width(ornament)
			segStart = col
			continue
		}
		if segStart < split && col >= split {
			flush()
		}
		seg.WriteRune(r)
		col += lipgloss.Width(string(r))
	}
	flush()
	return out.String()
}

// brandStyleAt — стиль сегмента, що починається в колонці col: ліворуч від
// split акцент «ua», далі решта.
func brandStyleAt(col, split int) lipgloss.Style {
	if col < split {
		return styleBrandUA
	}
	return styleBrandRest
}
