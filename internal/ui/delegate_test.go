package ui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/charmbracelet/x/ansi"
)

const testWidth = 80

// Список рахує сторінки за Height() делегата. Якщо хоч один рядок рендериться
// іншої висоти — з'їжджає все: і скрол, і пагінація.
func TestRowHeightUniform(t *testing.T) {
	items := []list.Item{
		item{icon: "▶", title: "Фрірен: за межами подорожі", meta: "AniUA, DZUSK"},
		item{title: "Без мети"},
		item{title: "Улюблене", header: true},
		item{header: true, spacer: true},
		item{icon: "✓", title: "Епізод 1", badge: "переглянуто"},
		item{
			icon:  "·",
			title: "Дуже довга назва тайтлу, яка гарантовано не влізає у вісімдесят комірок терміналу",
			meta:  "Студія перша, Студія друга, Студія третя, Студія четверта, Студія п'ята",
			badge: "нове",
		},
	}

	for _, twoLine := range []bool{false, true} {
		d := rowDelegate{twoLine: twoLine, ic: themeIcons(false)}
		m := list.New(items, d, testWidth, 20)
		for i := range items {
			var buf bytes.Buffer
			d.Render(&buf, m, i, items[i])
			out := buf.String()
			if h := lipgloss.Height(out); h != d.Height() {
				t.Errorf("twoLine=%v item %d: висота %d, очікували %d (%q)",
					twoLine, i, h, d.Height(), out)
			}
			if w := lipgloss.Width(out); w > testWidth {
				t.Errorf("twoLine=%v item %d: ширина %d > %d (%q)",
					twoLine, i, w, testWidth, out)
			}
		}
	}
}

// Колонка іконки має бути рівно iconWidth комірок для будь-якого символу,
// включно з порожнім і дводольним емодзі — інакше назви не вирівняні.
func TestPadIcon(t *testing.T) {
	unicode, ascii := themeIcons(false), themeIcons(true)
	cases := []string{
		"", " ", "🔍", "🇺🇦",
		unicode.Play, unicode.Done, unicode.Pending, unicode.Search, unicode.Cursor,
		ascii.Play, ascii.Done, ascii.Pending, ascii.Search, ascii.Cursor,
	}
	for _, c := range cases {
		if w := lipgloss.Width(padIcon(c, iconWidth)); w != iconWidth {
			t.Errorf("padIcon(%q, %d): ширина %d", c, iconWidth, w)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"епізод", 10, "епізод"},
		{"епізод", 6, "епізод"},
		{"епізод", 5, "епіз…"},
		{"епізод", 1, "…"},
		{"епізод", 0, ""},
		{"епізод", -1, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.w); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, очікували %q", c.in, c.w, got, c.want)
		}
	}
	// Дводольні символи не мають переповнювати рядок.
	if w := lipgloss.Width(truncate("🔍🔍🔍🔍", 5)); w > 5 {
		t.Errorf("truncate емодзі: ширина %d > 5", w)
	}
}

// Підсвітка збігів фільтра не має ані ламати геометрію рядка, ані падати на
// індексах, що вийшли за межі обрізаної назви.
func TestRenderWithFilter(t *testing.T) {
	items := []list.Item{
		item{icon: "▶", title: "Фрірен: за межами подорожі", meta: "AniUA"},
		item{icon: "·", title: "Магічна битва"},
	}
	d := rowDelegate{ic: themeIcons(false)}
	m := list.New(items, d, 24, 10)
	m, _ = m.Update(nil) // ініціалізуємо внутрішній стан списку
	m.SetFilterText("рен")

	for i := range items {
		var buf bytes.Buffer
		d.Render(&buf, m, i, items[i])
		out := buf.String()
		if h := lipgloss.Height(out); h != d.Height() {
			t.Errorf("item %d: висота %d, очікували %d", i, h, d.Height())
		}
		if w := lipgloss.Width(out); w > 24 {
			t.Errorf("item %d: ширина %d > 24 (%q)", i, w, out)
		}
	}
}

// Заголовок секції не мусить збігатися з жодним запитом фільтра.
func TestHeaderFilterValue(t *testing.T) {
	if got := (item{title: "Улюблене", header: true}).FilterValue(); got != "\x00" {
		t.Errorf("FilterValue заголовка = %q", got)
	}
	rule := item{title: i18n.TuiBlockCatalog, header: true, rule: true}
	if got := rule.FilterValue(); got != "\x00" {
		t.Errorf("FilterValue правила = %q", got)
	}
	if got := rule.key(); got != "" {
		t.Errorf("key правила = %q, want empty", got)
	}
	if got := (item{title: "Фрірен"}).FilterValue(); got != "Фрірен" {
		t.Errorf("FilterValue рядка = %q", got)
	}
}

func TestRenderHeaderFillsRow(t *testing.T) {
	const width = 24
	wantLine := "  НАЗВА"

	for _, twoLine := range []bool{false, true} {
		t.Run(fmt.Sprintf("twoLine=%v", twoLine), func(t *testing.T) {
			var buf bytes.Buffer
			(rowDelegate{twoLine: twoLine}).renderHeader(&buf, item{title: "Назва", header: true}, width)

			got := ansi.Strip(buf.String())
			if twoLine {
				if !strings.HasSuffix(got, "\n") {
					t.Fatalf("renderHeader() = %q, want trailing newline", got)
				}
				got = strings.TrimSuffix(got, "\n")
			}
			if got != wantLine {
				t.Errorf("renderHeader() = %q, want %q", got, wantLine)
			}
			if strings.Contains(got, "─") {
				t.Errorf("renderHeader() = %q, want no rules", got)
			}
		})
	}
}

func TestRenderHeaderVeryNarrow(t *testing.T) {
	for width := 2; width <= 5; width++ {
		for _, twoLine := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/twoLine=%v", width, twoLine), func(t *testing.T) {
				var buf bytes.Buffer
				(rowDelegate{twoLine: twoLine}).renderHeader(&buf, item{title: "Назва", header: true}, width)

				got := ansi.Strip(buf.String())
				if twoLine {
					if !strings.HasSuffix(got, "\n") {
						t.Fatalf("renderHeader() = %q, want trailing newline", got)
					}
					got = strings.TrimSuffix(got, "\n")
				}
				if gotWidth := lipgloss.Width(got); gotWidth > width {
					t.Errorf("renderHeader() width = %d, want at most %d (%q)", gotWidth, width, got)
				}
			})
		}
	}
}

func TestRenderRuleFitsWidth(t *testing.T) {
	for _, width := range []int{9, 12, 20, 40, 80} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			var buf bytes.Buffer
			(rowDelegate{ic: themeIcons(false)}).renderHeader(&buf, item{
				title:  i18n.TuiBlockCatalog,
				header: true,
				rule:   true,
			}, width)

			got := ansi.Strip(buf.String())
			if gotWidth := lipgloss.Width(got); gotWidth > width {
				t.Errorf("renderHeader() width = %d, want at most %d (%q)", gotWidth, width, got)
			}
			if strings.Contains(got, "\n") {
				t.Errorf("renderHeader() = %q, want one line", got)
			}
			label := strings.ToUpper(i18n.TuiBlockCatalog)
			wantLabel := label
			if width == 9 {
				wantLabel = truncate(label, width-2)
			}
			if !strings.Contains(got, wantLabel) {
				t.Errorf("renderHeader() = %q, want label %q", got, wantLabel)
			}
			if width == 80 {
				want := "  ── КАТАЛОГ ────────────────────────────────────────"
				if got != want {
					t.Errorf("renderHeader() = %q, want %q", got, want)
				}
			}
		})
	}
}

func TestRenderRuleASCII(t *testing.T) {
	var buf bytes.Buffer
	(rowDelegate{ic: themeIcons(true)}).renderHeader(&buf, item{
		title:  i18n.TuiBlockCatalog,
		header: true,
		rule:   true,
	}, 80)

	got := ansi.Strip(buf.String())
	if !strings.Contains(got, "--") {
		t.Errorf("renderHeader() = %q, want ASCII rule", got)
	}
	if strings.Contains(got, "─") {
		t.Errorf("renderHeader() = %q, contains Unicode rule", got)
	}
}

func TestRenderSpacer(t *testing.T) {
	for _, twoLine := range []bool{false, true} {
		t.Run(fmt.Sprintf("twoLine=%v", twoLine), func(t *testing.T) {
			var buf bytes.Buffer
			it := item{header: true, spacer: true}
			(rowDelegate{twoLine: twoLine}).renderHeader(&buf, it, 24)

			want := ""
			if twoLine {
				want = "\n"
			}
			if got := buf.String(); got != want {
				t.Errorf("renderHeader() = %q, want %q", got, want)
			}
			if got := it.key(); got != "" {
				t.Errorf("spacer key() = %q, want empty", got)
			}
		})
	}
}

func TestRenderOneLineInlineMetaAndBadge(t *testing.T) {
	const width = 40
	it := item{icon: "▶", title: "Серія 1", meta: "зупинився на 10:13", badge: "нове"}
	d := rowDelegate{ic: themeIcons(false)}
	m := list.New([]list.Item{it}, d, width, 10)

	var buf bytes.Buffer
	d.Render(&buf, m, 1, it)
	got := ansi.Strip(buf.String())

	want := "  " + padIcon(it.icon, iconWidth) + it.title + metaSep + it.meta + " " + it.badge
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
	if gotWidth := lipgloss.Width(got); gotWidth > width {
		t.Errorf("Render() width = %d, want at most %d (%q)", gotWidth, width, got)
	}
}

func TestRenderOneLineLongTitleDoesNotOverflow(t *testing.T) {
	const width = 32
	it := item{
		icon:  "▶",
		title: "Дуже довга назва тайтлу, яка не вміщується",
		meta:  "епізод 123",
		badge: "переглянуто",
	}
	d := rowDelegate{ic: themeIcons(false)}
	m := list.New([]list.Item{it}, d, width, 10)

	var buf bytes.Buffer
	d.Render(&buf, m, 1, it)
	got := ansi.Strip(buf.String())
	if gotWidth := lipgloss.Width(got); gotWidth > width {
		t.Errorf("Render() width = %d, want at most %d (%q)", gotWidth, width, got)
	}
	if !strings.HasSuffix(got, it.badge) {
		t.Errorf("Render() = %q, want badge %q at end", got, it.badge)
	}
}

func TestRenderOneLineNarrowDropsMetaKeepsBadge(t *testing.T) {
	const width = 58
	it := item{
		icon:  "▶",
		title: "Переродження: Життя з нуля в іншому світі / Re: Життя в іншому світі з нуля",
		meta:  "переглядаєш",
		badge: "+22 нові серії",
	}
	d := rowDelegate{ic: themeIcons(false)}
	m := list.New([]list.Item{it}, d, width, 10)

	var buf bytes.Buffer
	d.Render(&buf, m, 0, it)
	got := ansi.Strip(buf.String())

	if !strings.Contains(got, it.badge) {
		t.Errorf("Render() = %q, want badge %q", got, it.badge)
	}
	if strings.Contains(got, it.meta) {
		t.Errorf("Render() = %q, want meta dropped", got)
	}
	if gotWidth := lipgloss.Width(got); gotWidth > width {
		t.Errorf("Render() width = %d, want at most %d (%q)", gotWidth, width, got)
	}
	if !strings.Contains(got, ellipsis+" "+it.badge) {
		t.Errorf("Render() = %q, want truncated title before badge", got)
	}
}

func TestRenderOneLineNoMetaBadgeOnly(t *testing.T) {
	const width = 40
	it := item{icon: "▶", title: "Серія 2", badge: "переглянуто"}
	d := rowDelegate{ic: themeIcons(false)}
	m := list.New([]list.Item{it}, d, width, 10)

	var buf bytes.Buffer
	d.Render(&buf, m, 1, it)
	got := ansi.Strip(buf.String())
	want := "  " + padIcon(it.icon, iconWidth) + it.title + " " + it.badge
	if got != want {
		t.Errorf("Render() = %q, want %q", got, want)
	}
}

func TestMetaLineRendersStyledPartsWithoutChangingText(t *testing.T) {
	it := item{
		meta: "2023 · 28 з 28 · ★ 9.5 · Дуб+Саб · Studio1",
		metaParts: []metaPart{
			{text: "2023", kind: metaYear},
			{text: "28 з 28", kind: metaCount},
			{text: "★ 9.5", kind: metaRating},
			{text: "Дуб+Саб", kind: metaKinds},
			{text: "Studio1", kind: metaStudio},
		},
	}
	d := rowDelegate{twoLine: true}

	for _, test := range []struct {
		name     string
		width    int
		style    lipgloss.Style
		selected bool
	}{
		{name: "full", width: 80, style: styleMeta},
		{name: "truncated", width: 24, style: styleMeta},
		{name: "selected", width: 80, style: styleMetaSel, selected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := d.metaLine(it, test.width, test.style, test.selected)
			want := strings.Repeat(" ", rowIndent) + truncate(it.meta, test.width-rowIndent)
			if got := ansi.Strip(out); got != want {
				t.Errorf("ansi.Strip(metaLine()) = %q, want %q", got, want)
			}
			if gotWidth := lipgloss.Width(out); gotWidth > test.width {
				t.Errorf("metaLine() width = %d, want at most %d (%q)", gotWidth, test.width, ansi.Strip(out))
			}
		})
	}

	out := d.metaLine(it, 80, styleMeta, false)
	if styleMetaKey.Render("x") == styleMetaSep.Render("x") {
		t.Fatal("test requires distinct key and separator styles")
	}
	for _, styled := range []string{
		styleMetaKey.Render("2023"),
		styleMetaSep.Render(metaSep),
		styleMetaKey.Render("9.5"),
	} {
		if !strings.Contains(out, styled) {
			t.Errorf("metaLine() = %q, want styled segment %q", out, styled)
		}
	}
}
