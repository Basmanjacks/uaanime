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
	m.rebuildHome()
}

func (m *Model) rebuildHome() {
	m.epsScratch = map[string][]provider.Episode{}
	m.resetSavedScratch()
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
		items = append(items, lib[:min(homeBookmarkRows, len(lib))]...)
		items = append(items, item{title: fmt.Sprintf(i18n.TuiAllBookmarks, len(lib)), payload: payloadBookmarks{}})
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
	items = append(items, item{title: i18n.TuiDlItem, badge: m.downloadHomeBadge(), payload: payloadDownloads{}})
	items = append(items, item{icon: m.ic.Settings, title: i18n.TuiSettingsItem, payload: payloadSettings{}})

	items = append(items, m.catalogRows()...)

	_ = m.setItems(items, firstRow(items))
	if own == 0 {
		m.status = i18n.TuiEmptyLibrary
		m.statusKind = statusInfo
		m.statusGen++
	} else {
		m.status = ""
		m.statusKind = statusInfo
		m.statusGen++
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
		meta, badge, warn := statusMeta(status)
		if !m.newsDisabled[e.TitleID] {
			news := m.eng.Lib.PreferredFresh(e.TitleID, m.titleEpisodes(t), m.eng.Prefs)
			if n := news.Preferred + news.SubOnly; n > 0 {
				badge, warn = newsBadge(news)
				if m.screen == screenBookmarks && status.Fresh != n {
					meta += metaSep + i18n.NewEpisodes(status.Fresh)
				}
			}
		}
		rows = append(rows, row{
			it: item{
				title:     titleName(t),
				meta:      meta,
				badge:     badge,
				badgeWarn: warn,
				role:      "lib",
				payload:   payloadTitle{ref: t.Sources[0]},
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
		episodes := m.titleEpisodes(t)
		ep, pos, ok := m.eng.Lib.ResumeIn(id, episodes)
		if !ok {
			continue
		}
		it := item{
			icon:       m.ic.Play,
			title:      fmt.Sprintf(i18n.TuiContinuePfx, titleName(t), ep),
			iconAccent: true,
			payload:    payloadResume{ref: t.Sources[0], ep: ep},
		}
		if pos > 0 {
			it.meta = fmt.Sprintf(i18n.TuiEpAt, int(pos)/60, int(pos)%60)
		}
		// У якій озвучці відкриється плеєр — видно ще до Enter. Токен типу
		// в бейджі живе найдовше при обрізанні; саби без запиту — червоним.
		pin := m.pinFor(id)
		if chosen := m.willPlay(episodes, ep, pin); chosen != nil {
			if it.meta != "" {
				it.meta += metaSep
			}
			it.meta += chosen.Studio
			it.badge = i18n.KindShort(chosen.Kind)
			it.badgeWarn = chosen.Kind == provider.KindSub && !library.WantsSub(pin, m.eng.Prefs)
		}
		rows = append(rows, it)
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

// libraryFreshTotal — сума «нових серій» по видимих записах бібліотеки з тим
// самим StatusOf, що й бейджі рядків: різниця до/після ручного оновлення й є
// чесним «+N нових серій» у статусі. Scratch скидається — кеш на диску щойно
// перезаписано.
func (m *Model) libraryFreshTotal() int {
	m.epsScratch = map[string][]provider.Episode{}
	m.resetSavedScratch()
	total := 0
	// Той самий набір, що й оновлюється (visibleRefs): дубль запису на один
	// тайтл рахується один раз, як і запитується.
	for _, ref := range m.visibleRefs() {
		if t := m.eng.Lib.TitleByRef(ref); t != nil {
			total += m.titleStatus(t).Fresh
		}
	}
	return total
}

// titleStatus — стан тайтла для рядка списку: рахується з журналу і списку
// серій, ніде не зберігається.
func (m *Model) titleStatus(t *library.LocalTitle) library.Status {
	return m.eng.Lib.StatusOf(t.ID, m.titleEpisodes(t))
}

// statusMeta — підпис рядка бібліотеки й бейдж новинок. Одне джерело чисел із
// заголовком екрана серій: «залишилась 1 серія» там і тут означає те саме.
// Серії лише з субтитрами ніколи не ховаються за загальним «+N нових»: саме
// через це людина вмикала серію й отримувала саби.
func statusMeta(s library.Status) (meta, badge string, warn bool) {
	if s.Fresh > 0 {
		switch {
		case s.FreshSubOnly == s.Fresh:
			badge, warn = fmt.Sprintf(i18n.TuiFreshOnlySubs, s.Fresh), true
		case s.FreshSubOnly > 0:
			badge = fmt.Sprintf(i18n.TuiFreshMixed, i18n.NewEpisodes(s.Fresh-s.FreshSubOnly), s.FreshSubOnly)
		default:
			badge = i18n.NewEpisodes(s.Fresh)
		}
	}
	switch {
	case s.Kind == library.StatusDone:
		return i18n.TuiStateDone, "", false
	case s.Kind == library.StatusPlanned:
		if s.Total == 0 {
			return i18n.TuiStatePlanned, badge, warn
		}
		return fmt.Sprintf(i18n.TuiStatePlannedWith, i18n.Episodes(s.Total)), badge, warn
	case s.Total == 0:
		return i18n.TuiStateWatching, badge, warn
	default:
		return i18n.RemainingEpisodes(s.Remaining), badge, warn
	}
}

// newsBadge — бейдж новинок бажаної студії. Один лічильник не затуляє інший:
// «+1 у Рідний Голос · +1 лише в субтитрах» чесніший за будь-який із половин.
func newsBadge(news library.FreshNews) (badge string, warn bool) {
	switch {
	case news.SubOnly == 0:
		return fmt.Sprintf(i18n.TuiStudioNews, news.Preferred, news.Studio), false
	case news.Preferred == 0:
		return fmt.Sprintf(i18n.TuiFreshOnlySubs, news.SubOnly), true
	default:
		return fmt.Sprintf(i18n.TuiFreshMixed, fmt.Sprintf(i18n.TuiStudioNews, news.Preferred, news.Studio), news.SubOnly), false
	}
}

// refreshHome перебудовує домівку після фонового оновлення, лишаючи курсор
// там, де він стояв. На інших екранах модель лише запам'ятовує нові дані:
// перемалювати чужий список фоновим повідомленням — це вкрасти в людини те,
// на що вона зараз дивиться.
func (m *Model) refreshHome() {
	if m.overlay != overlayNone || m.screen != screenHome {
		return
	}
	cursor, errText := m.list.Index(), m.errText
	status, kind, gen := m.status, m.statusKind, m.statusGen
	selectedKey := ""
	if selected, ok := m.list.SelectedItem().(item); ok {
		selectedKey = selected.key()
	}
	m.showHome()
	m.errText = errText
	if status != "" && kind != statusInfo {
		m.status, m.statusKind, m.statusGen = status, kind, gen
	}
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
