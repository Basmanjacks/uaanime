package ui

import (
	"fmt"
	"strings"
	"testing"

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

func TestBrandBannerUniformWidth(t *testing.T) {
	if len(brandBanner) != 4 {
		t.Fatalf("brandBanner lines = %d, want 4", len(brandBanner))
	}
	w := lipgloss.Width(brandBanner[0])
	for i, line := range brandBanner[1:] {
		if got := lipgloss.Width(line); got != w {
			t.Errorf("brandBanner line %d width = %d, want %d", i+1, got, w)
		}
	}
}

func TestBrandBannerUsesTwoColors(t *testing.T) {
	oldProfile := compat.Profile
	compat.Profile = colorprofile.TrueColor
	t.Cleanup(func() { compat.Profile = oldProfile })

	m := newTestModel(t)
	lines := strings.Split(m.brandHeader(), "\n")
	uaSGR := firstSGR(styleBrandUA.Render("x"))
	restSGR := firstSGR(styleBrandRest.Render("x"))
	if uaSGR == "" || restSGR == "" || uaSGR == restSGR {
		t.Fatalf("brand SGR styles = %q and %q, want distinct non-empty sequences", uaSGR, restSGR)
	}

	for i, want := range brandBanner {
		if got := strings.TrimPrefix(ansi.Strip(lines[i]), "  "); got != want {
			t.Errorf("banner line %d text = %q, want %q", i, got, want)
		}
		if !strings.Contains(lines[i], uaSGR) || !strings.Contains(lines[i], restSGR) {
			t.Errorf("banner line %d does not contain both brand SGR styles: %q", i, lines[i])
		}
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
	if !strings.Contains(view, brandBanner[2]) {
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
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 24})

	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, brandBanner[2]) {
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
	if strings.Contains(view, brandBanner[2]) {
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

	if strings.Contains(ansi.Strip(m.View().Content), brandBanner[2]) {
		t.Error("search view contains brand banner")
	}
}
