package i18n

import "fmt"

// Plural повертає українську форму слова: однину для чисел на 1, форму для 2–4
// та множину для решти; числа на 11–14 завжди мають множину.
func Plural(n int, one, few, many string) string {
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
	return fmt.Sprintf("%d %s", n, Plural(n, "серія", "серії", "серій"))
}

// NewEpisodes форматує кількість нових серій за українським правилом множини.
func NewEpisodes(n int) string {
	return fmt.Sprintf("+%d %s", n, Plural(n, "нова серія", "нові серії", "нових серій"))
}
