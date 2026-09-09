package ui

import (
	"fmt"
	"sort"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// showHome — домівка як три секції: що продовжити, що вже в бібліотеці, і
// куди піти далі. Секція без жодного рядка не показується взагалі: порожній
// заголовок читається як помилка, а не як структура.
func (m *Model) showHome() {
	m.setScreen(screenHome)
	m.epsScratch = map[string][]provider.Episode{}
	m.errText = ""
	m.homeSpacers = m.list.Height() >= 16
	var items []item
	own := 0 // рядки з власними тайтлами: за ними судимо, чи бібліотека порожня

	// «Продовжити» — тайтли з найсвіжішим прогресом
	if rows := m.continueRows(homeContinueRows); len(rows) > 0 {
		items = append(items, item{header: true, title: i18n.TuiBlockContinue})
		items = append(items, rows...)
		own += len(rows)
	}

	lib := m.bookmarkRows()
	if len(lib) > 0 {
		if len(items) > 0 {
			items = sectionGap(items, 1, m.homeSpacers)
		}
		items = append(items, item{header: true, title: i18n.TuiBlockLibrary})
		items = append(items, lib...)
		own += len(lib)
	}

	if len(items) > 0 {
		items = sectionGap(items, 1, m.homeSpacers)
	}
	items = append(items,
		item{header: true, title: i18n.TuiBlockMore},
		item{icon: m.ic.Search, title: i18n.TuiSearchItem, payload: payloadSearch{}},
		item{icon: m.ic.Spark, title: i18n.TuiRouletteItem, payload: payloadRoulette{}})
	if len(m.eng.Lib.Progress) > 0 {
		items = append(items, item{title: i18n.TuiHistoryItem, payload: payloadHistory{}})
	}
	items = append(items, item{icon: m.ic.Settings, title: i18n.TuiSettingsItem, payload: payloadSettings{}})

	items = append(items, m.catalogRows()...)

	_ = m.setItems(items, firstRow(items))
	if own == 0 {
		m.status = i18n.TuiEmptyLibrary
	} else {
		m.status = ""
	}
}

func sectionGap(items []item, n int, enabled bool) []item {
	if !enabled {
		return items
	}
	for range n {
		items = append(items, item{header: true, spacer: true})
	}
	return items
}

// bookmarkRows — секція «Закладки» в порядку корисності: спершу те, де вийшли
// нові серії, далі — те, що дивилися найсвіжіше. Порядок додавання в закладки
// нікому нічого не каже, а от «є що подивитись» — це те, заради чого сюди
// заходять.
func (m *Model) bookmarkRows() []item {
	type row struct {
		it        item
		fresh     bool
		left      bool // є що дивитися: переглянуте осідає вниз секції
		watchedAt time.Time
	}
	watchedAt := m.watchedAtByTitle()

	var rows []row
	for _, e := range m.eng.Lib.Entries {
		if e.Hidden {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		status := m.titleStatus(t)
		meta, badge := statusMeta(status)
		rows = append(rows, row{
			it: item{
				title:   titleName(t),
				meta:    meta,
				badge:   badge,
				role:    "lib",
				payload: payloadTitle{ref: t.Sources[0]},
			},
			fresh:     status.Fresh > 0,
			left:      status.Kind != library.StatusDone,
			watchedAt: watchedAt[e.TitleID],
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.fresh != b.fresh {
			return a.fresh
		}
		if a.left != b.left {
			return a.left
		}
		if !a.watchedAt.Equal(b.watchedAt) {
			return a.watchedAt.After(b.watchedAt)
		}
		return a.it.title < b.it.title
	})

	items := make([]item, len(rows))
	for i, r := range rows {
		items[i] = r.it
	}
	return items
}

// watchedAtByTitle — коли кожен тайтл дивилися востаннє. Один прохід журналом
// на побудову домівки: за цим часом сортуються і «Продовжити», і закладки.
func (m *Model) watchedAtByTitle() map[string]time.Time {
	at := make(map[string]time.Time, len(m.eng.Lib.Progress))
	for _, p := range m.eng.Lib.Progress {
		if p.WatchedAt.After(at[p.TitleID]) {
			at[p.TitleID] = p.WatchedAt
		}
	}
	return at
}

// continueRows — до limit тайтлів, які дивилися найсвіжіше, по одному рядку на
// тайтл: у «Продовжити» цікава наступна серія, а не історія переглядів.
func (m *Model) continueRows(limit int) []item {
	watchedAt := m.watchedAtByTitle()
	ids := make([]string, 0, len(watchedAt))
	for id := range watchedAt {
		// Прибраний із бібліотеки тайтл не висить у «Продовжити»:
		// прогрес лишається в журналі й повернеться разом із тайтлом.
		if e := m.eng.Lib.EntryLookup(id); e != nil && e.Hidden {
			continue
		}
		ids = append(ids, id)
	}
	// ID як другий ключ: обхід мапи випадковий, і без нього тайтли з однаковою
	// міткою часу мінялися б місцями між кадрами.
	sort.Slice(ids, func(i, j int) bool {
		if !watchedAt[ids[i]].Equal(watchedAt[ids[j]]) {
			return watchedAt[ids[i]].After(watchedAt[ids[j]])
		}
		return ids[i] < ids[j]
	})

	var rows []item
	for _, id := range ids {
		if len(rows) == limit {
			break
		}
		t := m.titleByID(id)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		// Список серій, а не самий журнал: без нього «остання завершена + 1»
		// пропонувала серію, якої на сайті ще немає.
		ep, pos, ok := m.eng.Lib.ResumeIn(id, m.titleEpisodes(t))
		if !ok {
			continue
		}
		at := ""
		if pos > 0 {
			at = fmt.Sprintf(i18n.TuiEpAt, int(pos)/60, int(pos)%60)
		}
		rows = append(rows, item{
			icon:       m.ic.Play,
			title:      fmt.Sprintf(i18n.TuiContinuePfx, titleName(t), ep),
			meta:       at,
			iconAccent: true,
			payload:    payloadResume{ref: t.Sources[0], ep: ep},
		})
	}
	return rows
}

// rouletteCandidates — з чого рулетка тягне тайтл. Спершу «у планах»: це те,
// що людина відклала собі сама, і саме там вибір найболючіший. Планів немає —
// беремо картки каталогу, бо «нема з чого обирати» на порожній бібліотеці
// технічно правда, але як відповідь марна.
func (m *Model) rouletteCandidates() []provider.TitleRef {
	var refs []provider.TitleRef
	for _, e := range m.eng.Lib.Entries {
		if e.Hidden {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 || m.titleStatus(t).Kind != library.StatusPlanned {
			continue
		}
		refs = append(refs, t.Sources[0])
	}
	if len(refs) > 0 {
		return refs
	}
	// Порядок обходу — catalogKinds, а не мапа: випадковість має бути в
	// randN, а не в порядку ключів, інакше однаковий n давав би різні тайтли.
	for _, kind := range catalogKinds {
		for _, c := range m.catalog[kind] {
			refs = append(refs, c.TitleRef)
		}
	}
	return refs
}

// catalogRows — блоки каталогу як хвіст домівки: спершу те, що вже дивишся,
// і лише потім те, що можна почати. Порожній блок не показується взагалі.
func (m *Model) catalogRows() []item {
	var items []item
	blocks := 0
	for _, kind := range catalogKinds {
		cards := m.catalog[kind]
		if len(cards) == 0 {
			continue
		}
		if blocks == 0 {
			items = sectionGap(items, 1, m.homeSpacers)
			items = append(items, item{header: true, rule: true, title: i18n.TuiBlockCatalog})
			items = sectionGap(items, 1, m.homeSpacers)
		} else {
			items = sectionGap(items, 1, m.homeSpacers)
		}
		items = append(items, item{header: true, title: catalogBlockTitle(kind)})
		for i, c := range cards {
			if i == homeCatalogRows {
				break
			}
			items = append(items, item{
				title:   c.Name,
				meta:    cardMeta(c),
				role:    "cat:" + string(kind),
				payload: payloadTitle{ref: c.TitleRef, epAired: c.EpAired},
			})
		}
		blocks++
	}
	return items
}

func catalogBlockTitle(kind provider.CatalogKind) string {
	if kind == provider.CatalogFresh {
		return i18n.TuiBlockFresh
	}
	return i18n.TuiBlockTop
}

// titleEpisodes — список серій тайтла з кешу на диску. Кеш пише фонова
// команда на кожному старті, тож числа стоять уже в першому кадрі. Мапа
// scratch живе рівно одну перебудову екрана: без неї один showHome читав би
// той самий файл із трьох різних місць.
func (m *Model) titleEpisodes(t *library.LocalTitle) []provider.Episode {
	if eps, ok := m.epsScratch[t.ID]; ok {
		return eps
	}
	var eps []provider.Episode
	if m.eng.Store != nil && len(t.Sources) > 0 {
		if cached, _, found := m.eng.Store.LoadEpisodes(t.Sources[0]); found {
			eps = cached
		}
	}
	if m.epsScratch != nil {
		m.epsScratch[t.ID] = eps
	}
	return eps
}

// titleStatus — стан тайтла для рядка списку: рахується з журналу і списку
// серій, ніде не зберігається.
func (m *Model) titleStatus(t *library.LocalTitle) library.Status {
	return m.eng.Lib.StatusOf(t.ID, m.titleEpisodes(t))
}

// statusMeta — підпис рядка бібліотеки й бейдж новинок. Одне джерело чисел із
// заголовком екрана серій: «залишилась 1 серія» там і тут означає те саме.
func statusMeta(s library.Status) (meta, badge string) {
	if s.Fresh > 0 {
		badge = i18n.NewEpisodes(s.Fresh)
	}
	switch {
	case s.Kind == library.StatusDone:
		return i18n.TuiStateDone, ""
	case s.Kind == library.StatusPlanned:
		if s.Total == 0 {
			return i18n.TuiStatePlanned, badge
		}
		return fmt.Sprintf(i18n.TuiStatePlannedWith, i18n.Episodes(s.Total)), badge
	case s.Total == 0:
		return i18n.TuiStateWatching, badge
	default:
		return i18n.RemainingEpisodes(s.Remaining), badge
	}
}

// refreshHome перебудовує домівку після фонового оновлення, лишаючи курсор
// там, де він стояв. На інших екранах модель лише запам'ятовує нові дані:
// перемалювати чужий список фоновим повідомленням — це вкрасти в людини те,
// на що вона зараз дивиться.
func (m *Model) refreshHome() {
	if m.screen != screenHome {
		return
	}
	cursor, errText := m.list.GlobalIndex(), m.errText
	selectedKey := ""
	if selected, ok := m.list.SelectedItem().(item); ok {
		selectedKey = selected.key()
	}
	m.showHome()
	m.errText = errText
	if len(m.list.Items()) == 0 {
		return
	}
	if selectedKey != "" {
		for index, listItem := range m.list.Items() {
			if it, ok := listItem.(item); ok && it.key() == selectedKey {
				m.list.Select(index)
				return
			}
		}
	}
	m.list.Select(max(0, min(cursor, len(m.list.Items())-1)))
	m.skipHeaders(1)
}
