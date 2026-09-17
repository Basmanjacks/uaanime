package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func TestThemeIcons(t *testing.T) {
	unicode, ascii := themeIcons(false), themeIcons(true)
	if unicode.Cursor != "❯" || unicode.Spark != "✳" {
		t.Errorf("unicode icons: cursor = %q, spark = %q", unicode.Cursor, unicode.Spark)
	}
	if ascii.Cursor != ">" || ascii.Spark != "*" {
		t.Errorf("ASCII icons: cursor = %q, spark = %q", ascii.Cursor, ascii.Spark)
	}
}

// TestBrandBannerUniformWidth — геометрія банера не залежить ні від сезону, ні
// від набору символів: brandWidth() вирішує, показувати банер чи fallback, і
// варіант, ширший за нього, зсунув би рамку всього екрана.
func TestBrandBannerUniformWidth(t *testing.T) {
	// Ширини й висоти закріплені числами: випадкова правка шаблону зсунула б
	// рамку всього екрана непомітно для решти тестів. Обидві геометрії — той
	// самий figlet у різних символах, тож числа збігаються за побудовою.
	wantWidth := map[bool]int{false: 39, true: 39}
	wantChrome := map[bool]int{false: 8, true: 8}
	for _, ascii := range []bool{false, true} {
		art := brandArtFor(ascii)
		if art.width != wantWidth[ascii] {
			t.Errorf("ascii=%t brandWidth = %d, want %d", ascii, art.width, wantWidth[ascii])
		}
		if got := brandChromeHeight(ascii); got != wantChrome[ascii] {
			t.Errorf("ascii=%t brandChromeHeight = %d, want %d", ascii, got, wantChrome[ascii])
		}
		for s := seasonWinter; s <= seasonAutumn; s++ {
			lines := brandVariant(brandOrnaments[s], ascii)
			if len(lines) != len(art.lines) {
				t.Fatalf("season %d ascii=%t lines = %d, want %d", s, ascii, len(lines), len(art.lines))
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != art.width {
					t.Errorf("season %d ascii=%t line %d width = %d, want %d", s, ascii, i, got, art.width)
				}
			}
			if strings.Contains(strings.Join(lines, ""), brandOrnamentMark) {
				t.Errorf("season %d ascii=%t left an unsubstituted ornament mark", s, ascii)
			}
		}
	}

	// Жирний варіант — та сама сітка: орнамент не має «переїхати».
	if len(brandBlock.lines) != len(brandASCII.lines) {
		t.Fatalf("brandBlock lines = %d, brandASCII lines = %d", len(brandBlock.lines), len(brandASCII.lines))
	}
	for i := range brandBlock.lines {
		if markColumns(brandBlock.lines[i]) != markColumns(brandASCII.lines[i]) {
			t.Errorf("line %d: ornament marks differ between block and ASCII templates", i)
		}
	}
}

// markColumns — колонки орнаменту в рядку шаблону, як рядок для порівняння.
// Рахуємо по рунах: жирний шаблон багатобайтовий, байтові індекси тут брешуть.
func markColumns(line string) string {
	var cols []string
	for i, r := range []rune(line) {
		if string(r) == brandOrnamentMark {
			cols = append(cols, fmt.Sprint(i))
		}
	}
	return strings.Join(cols, ",")
}

// TestBrandSplitColumn — стилізований рядок після зняття SGR і є банером: розріз
// на межі кольорів нічого не губить і не дублює в обох геометріях.
func TestBrandSplitColumn(t *testing.T) {
	for _, ascii := range []bool{false, true} {
		art := brandArtFor(ascii)
		o := brandOrnaments[seasonSummer]
		glyph := o.unicode
		if ascii {
			glyph = o.ascii
		}
		want := brandVariant(o, ascii)
		for i, line := range art.lines {
			if got := ansi.Strip(renderBrandLine(line, art.split, glyph)); got != want[i] {
				t.Errorf("ascii=%t line %d rendered text = %q, want %q", ascii, i, got, want[i])
			}
		}
	}
}

// TestBannerVisibleThresholds — пороги видимості читаються з геометрії. Зараз
// обидві геометрії однакового розміру; тест ловить розсинхрон, якщо одну з них
// колись змінять окремо.
func TestBannerVisibleThresholds(t *testing.T) {
	tests := []struct {
		ascii bool
		w, h  int
		want  bool
	}{
		{false, 42, 24, false},
		{false, 43, 24, true},
		{true, 42, 24, false},
		{true, 43, 24, true},
		{false, 80, 17, false},
		{false, 80, 18, true},
		{true, 80, 17, false},
		{true, 80, 18, true},
	}
	for _, tt := range tests {
		m := newTestModel(t)
		m.ic = themeIcons(tt.ascii)
		m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: tt.w, Height: tt.h})
		if got := m.bannerVisible(); got != tt.want {
			t.Errorf("ascii=%t %dx%d bannerVisible = %t, want %t", tt.ascii, tt.w, tt.h, got, tt.want)
		}
	}
}

// TestSeasonFor — межі метеорологічних сезонів; банер міняється разом із ними.
func TestSeasonFor(t *testing.T) {
	tests := []struct {
		date time.Time
		want season
	}{
		{time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC), seasonWinter},
		{time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), seasonSpring},
		{time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC), seasonSummer},
		{time.Date(2026, time.October, 31, 0, 0, 0, 0, time.UTC), seasonAutumn},
		// Грудень — уже зима, вересень — уже осінь.
		{time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC), seasonWinter},
		{time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC), seasonAutumn},
	}
	for _, tt := range tests {
		if got := seasonFor(tt.date); got != tt.want {
			t.Errorf("seasonFor(%s) = %d, want %d", tt.date.Format("2006-01-02"), got, tt.want)
		}
	}
}

// TestBrandBannerSeasonalOrnament — банер бере руну сезону, а в ASCII-режимі
// не показує Unicode взагалі.
func TestBrandBannerSeasonalOrnament(t *testing.T) {
	brandNow = func() time.Time { return time.Date(2026, time.January, 5, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { brandNow = time.Now })

	m := newTestModel(t)
	top := m.brandBanner()[0]
	if !strings.Contains(top, brandOrnaments[seasonWinter].unicode) {
		t.Errorf("winter banner top line = %q, want the winter ornament", top)
	}

	m.ic = themeIcons(true)
	top = m.brandBanner()[0]
	if !strings.Contains(top, brandOrnaments[seasonWinter].ascii) {
		t.Errorf("ASCII winter banner top line = %q, want the ASCII ornament", top)
	}
	for _, line := range m.brandBanner() {
		for _, r := range line {
			if r > 127 {
				t.Fatalf("ASCII banner contains a non-ASCII rune %q in %q", r, line)
			}
		}
	}
}

// TestBrandBannerUsesTwoColors — «ua» акцентом, «anime» кольором рядка, межа
// рівно на split, орнаменти в кольорі своєї частини — в обох наборах символів.
func TestBrandBannerUsesTwoColors(t *testing.T) {
	oldProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = oldProfile })

	uaSGR := firstSGR(styleBrandUA.Render("x"))
	restSGR := firstSGR(styleBrandRest.Render("x"))
	if uaSGR == "" || restSGR == "" || uaSGR == restSGR {
		t.Fatalf("brand SGR styles = %q and %q, want distinct non-empty sequences", uaSGR, restSGR)
	}

	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%t", ascii), func(t *testing.T) {
			m := newTestModel(t)
			m.ic = themeIcons(ascii)
			art := brandArtFor(ascii)
			glyph := m.brandOrnamentGlyph()
			lines := strings.Split(m.brandHeader(), "\n")
			for i, want := range m.brandBanner() {
				if got := strings.TrimPrefix(ansi.Strip(lines[i]), "  "); got != want {
					t.Errorf("banner line %d text = %q, want %q", i, got, want)
				}
				runes := []rune(art.lines[i])
				left := strings.TrimSpace(string(runes[:art.split]))
				right := strings.TrimSpace(string(runes[art.split:]))
				if left != "" && !strings.Contains(lines[i], uaSGR) {
					t.Errorf("banner line %d lacks the ua SGR: %q", i, lines[i])
				}
				if right != "" && !strings.Contains(lines[i], restSGR) {
					t.Errorf("banner line %d lacks the rest SGR: %q", i, lines[i])
				}
				if strings.Contains(art.lines[i], brandOrnamentMark) {
					// Лівий орнамент в акценті, правий — у кольорі «anime».
					wantLeft := uaSGR + glyph
					wantRight := restSGR + glyph
					if !strings.Contains(lines[i], wantLeft) || !strings.Contains(lines[i], wantRight) {
						t.Errorf("banner line %d: ornaments not styled by their side: %q", i, lines[i])
					}
				}
			}

			// Межа кольорів рівно на split: рядок без орнаментів, перший SGR —
			// акцент, перший restSGR стоїть перед символом колонки split.
			line := art.lines[len(art.lines)-1]
			rendered := renderBrandLine(line, art.split, glyph)
			if firstSGR(rendered) != uaSGR {
				t.Errorf("rendered line starts with %q, want ua SGR", firstSGR(rendered))
			}
			at := strings.Index(rendered, restSGR)
			if at < 0 {
				t.Fatalf("rendered line has no rest SGR: %q", rendered)
			}
			if got := lipgloss.Width(ansi.Strip(rendered[:at])); got != art.split {
				t.Errorf("ua segment width = %d, want %d", got, art.split)
			}
		})
	}
}

func TestBrandFallbackTitleUsesTwoColorsWithBothIconSets(t *testing.T) {
	oldProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = oldProfile })

	uaSGR := firstSGR(styleBrandUA.Render("x"))
	restSGR := firstSGR(styleBrandRest.Render("x"))
	if uaSGR == "" || restSGR == "" || uaSGR == restSGR {
		t.Fatalf("brand SGR styles = %q and %q, want distinct non-empty sequences", uaSGR, restSGR)
	}

	for _, ascii := range []bool{false, true} {
		t.Run(fmt.Sprintf("ascii=%t", ascii), func(t *testing.T) {
			m := newTestModel(t)
			m.ic = themeIcons(ascii)
			got := m.brandFallbackTitle()
			want := m.ic.Spark + " " + i18n.TuiAppTitle + metaSep + strings.ToUpper(i18n.TuiTaglineShort)
			if stripped := strings.TrimSpace(ansi.Strip(got)); stripped != want {
				t.Errorf("fallback title text = %q, want %q", stripped, want)
			}

			title := []rune(i18n.TuiAppTitle)
			accent := styleBrandUA.Render(m.ic.Spark + " " + string(title[:2]))
			rest := styleBrandRest.Render(string(title[2:]))
			if !strings.Contains(got, accent+rest) {
				t.Errorf("fallback title does not contain distinct accent/rest segments: %q", got)
			}
		})
	}
}

func firstSGR(s string) string {
	start := strings.Index(s, "\x1b[")
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start:], 'm')
	if end < 0 {
		return ""
	}
	return s[start : start+end+1]
}

func TestHomeBannerRendered(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, m.brandBanner()[1]) {
		t.Error("home view does not contain brand banner")
	}
	if !strings.Contains(view, strings.ToUpper(i18n.TuiTagline)) {
		t.Error("home view does not contain full tagline")
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Errorf("view height = %d, want 24", got)
	}
}

func TestHomeBannerFallbackNarrow(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 39, Height: 24})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, m.brandBanner()[1]) {
		t.Error("narrow home view contains brand banner")
	}
	if !strings.Contains(view, strings.ToUpper(i18n.TuiTaglineShort)) {
		t.Error("narrow home view does not contain short tagline")
	}
	if got := lipgloss.Height(view); got != 24 {
		t.Errorf("view height = %d, want 24", got)
	}
}

func TestHomeBannerFallbackShort(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 14})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, m.brandBanner()[1]) {
		t.Error("short home view contains brand banner")
	}
	if !strings.Contains(view, strings.ToUpper(i18n.TuiTaglineShort)) {
		t.Error("short home view does not contain short tagline")
	}
	if got := lipgloss.Height(view); got != 14 {
		t.Errorf("view height = %d, want 14", got)
	}
}

func TestSearchScreenHasNoBanner(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenSearch
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if strings.Contains(ansi.Strip(m.View().Content), m.brandBanner()[1]) {
		t.Error("search view contains brand banner")
	}
}
