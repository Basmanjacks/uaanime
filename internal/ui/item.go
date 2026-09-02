package ui

import (
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// item — універсальний елемент для всіх екранів; payload каже, що робить Enter.
// Іконка, мета й бейдж — окремі поля, а не склеєні в title: інакше колонки
// не вирівняти, а фільтр шукав би по іконці.
type item struct {
	icon       string // символ у колонці ліворуч від назви
	title      string
	meta       string // другорядний рядок: студії, час, стан
	metaParts  []metaPart
	badge      string // короткий статус праворуч
	header     bool   // заголовок секції: не вибирається й не фільтрується
	spacer     bool   // порожній роздільник; завжди разом із header
	rule       bool   // підписаний роздільник; завжди разом із header
	iconAccent bool   // іконка в акцентному кольорі
	role       string // розрізняє однаковий тайтл у бібліотеці й каталозі домівки
	payload    any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.meta }

// FilterValue: заголовок секції не має збігатися ні з чим, що вводить людина.
func (i item) FilterValue() string {
	if i.header || i.spacer || i.rule {
		return "\x00"
	}
	return i.title
}

func (i item) key() string {
	if i.spacer || i.rule {
		return ""
	}
	if i.header {
		return "h:" + i.title
	}
	switch payload := i.payload.(type) {
	case payloadResume:
		return "resume:" + payload.ref.Provider + ":" + payload.ref.Slug
	case payloadTitle:
		if i.role != "" {
			return i.role + ":" + payload.ref.Provider + ":" + payload.ref.Slug
		}
	case payloadSearch:
		return "search"
	case payloadHistory:
		return "history"
	case payloadMore:
		return "more"
	}
	return ""
}

type (
	payloadResume struct {
		ref provider.TitleRef
		ep  int
	}
	payloadTitle struct {
		ref     provider.TitleRef
		epAired int
	}
	payloadMore    struct{} // «показати ще» — наступна сторінка результатів
	payloadSearch  struct{}
	payloadHistory struct{}
	payloadEp      struct{ num int }
	payloadStudio  struct{ src provider.Source }
)
