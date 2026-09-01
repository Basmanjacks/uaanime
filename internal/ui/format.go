package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// maxCardStudios — скільки студій вміщує другий рядок картки. Більше — і
// мета-рядок перетворюється на суцільний список, у якому нічого не читається.
const maxCardStudios = 2

type metaKind int

const (
	metaYear metaKind = iota
	metaCount
	metaRating
	metaKinds
	metaStudio
)

type metaPart struct {
	text string
	kind metaKind
}

// cardMeta — другий рядок картки пошуку: рік, серії, оцінка, озвучення, студії.
// Порядок сталий, бо саме за позицією око знаходить потрібне поле; порожні
// поля просто зникають, а не лишають дірку в роздільниках.
func cardMeta(c provider.TitleCard) string {
	parts := cardMetaParts(c)
	texts := make([]string, len(parts))
	for i, part := range parts {
		texts[i] = part.text
	}
	return strings.Join(texts, metaSep)
}

func cardMetaParts(c provider.TitleCard) []metaPart {
	parts := make([]metaPart, 0, 4+maxCardStudios)
	if c.Year > 0 {
		parts = append(parts, metaPart{text: strconv.Itoa(c.Year), kind: metaYear})
	}
	// Провайдер уже дав людський рядок («28 з 28») — беремо його; інакше
	// збираємо з кількості вийшлих серій за правилом множини.
	switch {
	case c.Episodes != "":
		parts = append(parts, metaPart{text: c.Episodes, kind: metaCount})
	case c.EpAired > 0:
		parts = append(parts, metaPart{text: i18n.Episodes(c.EpAired), kind: metaCount})
	}
	if c.Rating > 0 {
		parts = append(parts, metaPart{text: fmt.Sprintf("%s %.1f", ratingMark, c.Rating), kind: metaRating})
	}
	if kinds := cardKinds(c); kinds != "" {
		parts = append(parts, metaPart{text: kinds, kind: metaKinds})
	}
	// Студії — як є: назва студії не перекладається й не скорочується.
	studios := 0
	for _, s := range c.Studios {
		if s == "" {
			continue
		}
		parts = append(parts, metaPart{text: s, kind: metaStudio})
		studios++
		if studios == maxCardStudios {
			break
		}
	}
	return parts
}

// cardKinds — коротка мітка озвучення: дубляж, субтитри або обидва.
func cardKinds(c provider.TitleCard) string {
	switch {
	case c.HasDub && c.HasSub:
		return i18n.TuiDubSub
	case c.HasDub:
		return i18n.TuiDub
	case c.HasSub:
		return i18n.TuiSub
	}
	return ""
}

// humanDate форматує час перегляду відносно локального календарного дня.
func humanDate(t, now time.Time) string {
	t = t.In(now.Location())
	if sameDay(t, now) {
		return fmt.Sprintf("%s %s", i18n.TuiToday, t.Format("15:04"))
	}
	if sameDay(t, now.AddDate(0, 0, -1)) {
		return fmt.Sprintf("%s %s", i18n.TuiYesterday, t.Format("15:04"))
	}
	if t.Year() == now.Year() {
		return t.Format("02.01")
	}
	return t.Format("02.01.2006")
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}
