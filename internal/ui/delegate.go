package ui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Анатомія рядка:
//
//	▌▶ Фрірен — епізод 12                    переглянуто
//	 ✓ Епізод 1
//	 · Епізод 2
//
// Колонки 0–1 — курсор, колонки 2–3 — іконка (рівно iconWidth комірок,
// тому назви завжди починаються з однієї позиції), далі назва.
// Секція не має ні курсора, ні іконки — вона не вибирається.
const (
	cursorWidth = 2
	iconWidth   = 2
	rowIndent   = cursorWidth + iconWidth
)

// rowDelegate малює рядок списку. Один рядок = одна лінія терміналу; режим
// twoLine (результати пошуку) додає другу лінію з метаданими. Висота однакова
// для всіх елементів, інакше список рахує сторінки неправильно.
type rowDelegate struct {
	twoLine bool
	ic      icons
}

func (d rowDelegate) Height() int {
	if d.twoLine {
		return 2
	}
	return 1
}

func (d rowDelegate) Spacing() int { return 0 }

func (d rowDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d rowDelegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	it, ok := li.(item)
	if !ok {
		return
	}
	width := m.Width()
	if width <= 0 {
		return
	}

	if it.header {
		d.renderHeader(w, it, width)
		return
	}

	cursor, titleStyle, metaStyle := "  ", styleRow, styleMeta
	selected := index == m.Index() && m.FilterState() != list.Filtering
	if selected {
		cursor = styleCursor.Render(d.ic.Cursor) + " "
		titleStyle, metaStyle = styleRowSel, styleMetaSel
	}

	avail := width - rowIndent
	iconStyle := titleStyle
	if it.iconAccent {
		iconStyle = styleIconAccent
	}
	prefix := cursor + iconStyle.Render(padIcon(it.icon, iconWidth))

	if !d.twoLine {
		cluster := ""
		if it.meta != "" {
			cluster = metaStyle.Render(it.meta)
		}
		if it.badge != "" {
			if cluster != "" {
				cluster += " "
			}
			cluster += styleBadge.Render(it.badge)
		}
		clusterWidth := lipgloss.Width(cluster)
		titleWidth := max(0, avail-clusterWidth-2)
		left := prefix + d.title(m, index, it, titleWidth, titleStyle)
		pad := width - lipgloss.Width(left) - clusterWidth
		if cluster != "" && pad >= 2 {
			fmt.Fprint(w, left+strings.Repeat(" ", pad)+cluster) //nolint:errcheck
			return
		}
	}

	titleWidth, badge := avail, ""
	// В одному рядку бейдж живе праворуч від назви; у двох — під нею, з метою.
	if !d.twoLine && it.badge != "" && avail > lipgloss.Width(it.badge)+2 {
		badge, titleWidth = it.badge, avail-lipgloss.Width(it.badge)-1
	}
	// В одному рядку мета йде одразу за назвою приглушеним кольором,
	// інакше «зупинився на 12:34» чи «переглядаєш» ніде показати.
	meta := ""
	if !d.twoLine && it.meta != "" {
		metaW := lipgloss.Width(it.meta) + 2
		if titleWidth-metaW >= lipgloss.Width(it.title) {
			meta, titleWidth = it.meta, titleWidth-metaW
		} else if titleWidth > metaW+10 { // довга назва: ділимо, але не душимо її
			meta = truncate(it.meta, titleWidth/3)
			titleWidth -= lipgloss.Width(meta) + 2
		}
	}

	line := prefix + d.title(m, index, it, titleWidth, titleStyle)
	if meta != "" {
		line += "  " + metaStyle.Render(meta)
	}
	if badge != "" {
		line += " " + styleBadge.Render(badge)
	}
	if !d.twoLine {
		fmt.Fprint(w, line) //nolint:errcheck
		return
	}
	fmt.Fprint(w, line+"\n"+d.metaLine(it, width, metaStyle, selected)) //nolint:errcheck
}

// title обрізає назву до titleWidth і підсвічує збіги активного фільтра.
// Підсвітка — тільки на назві: за нею користувач і шукає.
func (d rowDelegate) title(m list.Model, index int, it item, titleWidth int, style lipgloss.Style) string {
	t := truncate(it.title, titleWidth)
	filtered := m.FilterState() == list.Filtering || m.FilterState() == list.FilterApplied
	if !filtered {
		return style.Render(t)
	}
	unmatched := style.Inline(true)
	return lipgloss.StyleRunes(t, m.MatchesForItem(index), unmatched.Inherit(styleMatch), unmatched)
}

// metaLine — другий рядок: мета вирівняна під назвою, бейдж у кінці.
func (d rowDelegate) metaLine(it item, width int, style lipgloss.Style, selected bool) string {
	avail := width - rowIndent
	metaWidth, badge := avail, ""
	if it.badge != "" && avail > lipgloss.Width(it.badge)+2 {
		badge, metaWidth = it.badge, avail-lipgloss.Width(it.badge)-2
	}
	plainMeta := it.meta
	if len(it.metaParts) > 0 {
		texts := make([]string, len(it.metaParts))
		for i, part := range it.metaParts {
			texts[i] = part.text
		}
		plainMeta = strings.Join(texts, metaSep)
	}
	meta := truncate(plainMeta, metaWidth)
	if meta == "" && badge == "" {
		return ""
	}
	line := strings.Repeat(" ", rowIndent)
	if meta != "" {
		if len(it.metaParts) > 0 {
			line += renderMetaParts(it.metaParts, meta, selected)
		} else {
			line += style.Render(meta)
		}
	}
	if badge != "" {
		if meta != "" {
			line += "  "
		}
		line += styleBadge.Render(badge)
	}
	return line
}

type styledMetaSegment struct {
	text  string
	style lipgloss.Style
}

func renderMetaParts(parts []metaPart, truncated string, selected bool) string {
	metaStyle, keyStyle, ratingMarkStyle := styleMeta, styleMetaKey, styleMetaSep
	if selected {
		metaStyle, keyStyle, ratingMarkStyle = styleMetaSel, styleMetaKeySel, styleMeta
	}

	segments := make([]styledMetaSegment, 0, len(parts)*2)
	for i, part := range parts {
		if i > 0 {
			segments = append(segments, styledMetaSegment{text: metaSep, style: styleMetaSep})
		}
		switch part.kind {
		case metaYear:
			segments = append(segments, styledMetaSegment{text: part.text, style: keyStyle})
		case metaRating:
			mark, value, ok := strings.Cut(part.text, " ")
			if !ok {
				segments = append(segments, styledMetaSegment{text: part.text, style: ratingMarkStyle})
				continue
			}
			segments = append(segments,
				styledMetaSegment{text: mark + " ", style: ratingMarkStyle},
				styledMetaSegment{text: value, style: keyStyle},
			)
		default:
			segments = append(segments, styledMetaSegment{text: part.text, style: metaStyle})
		}
	}

	plain := make([]string, len(parts))
	for i, part := range parts {
		plain[i] = part.text
	}
	cut := truncated != strings.Join(plain, metaSep)
	prefix := truncated
	if cut {
		prefix = strings.TrimSuffix(prefix, ellipsis)
	}

	var out strings.Builder
	for _, segment := range segments {
		if prefix == "" {
			if cut {
				out.WriteString(segment.style.Render(ellipsis))
			}
			return out.String()
		}
		if len(prefix) >= len(segment.text) {
			out.WriteString(segment.style.Render(segment.text))
			prefix = prefix[len(segment.text):]
			continue
		}
		out.WriteString(segment.style.Render(prefix))
		if cut {
			out.WriteString(segment.style.Render(ellipsis))
		}
		return out.String()
	}
	return out.String()
}

func (d rowDelegate) renderHeader(w io.Writer, it item, width int) {
	if it.spacer {
		fmt.Fprint(w, "") //nolint:errcheck
		if d.twoLine {
			fmt.Fprint(w, "\n") //nolint:errcheck
		}
		return
	}
	out := "  " + styleSectionName.Render(strings.ToUpper(truncate(it.title, width-2)))
	if d.twoLine {
		out += "\n"
	}
	fmt.Fprint(w, out) //nolint:errcheck
}
