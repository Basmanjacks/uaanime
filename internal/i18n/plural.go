package i18n

import (
	"fmt"
	"math"
)

// plural повертає українську форму слова: однину для чисел на 1, форму для 2–4
// та множину для решти; числа на 11–14 завжди мають множину.
func plural(n int, one, few, many string) string {
	last := n % 10
	lastTwo := n % 100
	if last < 0 {
		last = -last
	}
	if lastTwo < 0 {
		lastTwo = -lastTwo
	}

	if last == 1 && lastTwo != 11 {
		return one
	}
	if last >= 2 && last <= 4 && (lastTwo < 12 || lastTwo > 14) {
		return few
	}
	return many
}

// Episodes форматує кількість серій за українським правилом множини.
func Episodes(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "серія", "серії", "серій"))
}

// NewEpisodes форматує кількість нових серій за українським правилом множини.
func NewEpisodes(n int) string {
	return fmt.Sprintf("+%d %s", n, plural(n, "нова серія", "нові серії", "нових серій"))
}

// RemainingEpisodes — скільки серій ще не переглянуто, за українським правилом
// множини. Дієслово теж змінюється, тому форми беруться цілими фразами.
func RemainingEpisodes(n int) string {
	return fmt.Sprintf(plural(n, "залишилась %d серія", "залишилось %d серії", "залишилось %d серій"), n)
}

// HumanDuration — тривалість словами: «2 год 45 хв». Секунди не показуємо —
// це оцінка із середнього, а не таймер; «год» і «хв» скорочення й не
// відмінюються. Менше за хвилину — порожньо: показувати «0 хв» гірше, ніж нічого.
func HumanDuration(sec float64) string {
	minutes := int(math.Round(sec / 60))
	if minutes <= 0 {
		return ""
	}
	h, mm := minutes/60, minutes%60
	switch {
	case h > 0 && mm > 0:
		return fmt.Sprintf("%d год %d хв", h, mm)
	case h > 0:
		return fmt.Sprintf("%d год", h)
	default:
		return fmt.Sprintf("%d хв", mm)
	}
}
