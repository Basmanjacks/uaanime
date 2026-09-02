package ui

import (
	"fmt"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// showHome — домівка як три секції: що продовжити, що вже в бібліотеці, і
// куди піти далі. Секція без жодного рядка не показується взагалі: порожній
// заголовок читається як помилка, а не як структура.
func (m *Model) showHome() {
	m.setScreen(screenHome)
	m.errText = ""
	m.homeSpacers = m.list.Height() >= 16
	var items []item
	own := 0 // рядки з власними тайтлами: за ними судимо, чи бібліотека порожня

	// «Продовжити» — тайтл з найсвіжішим прогресом
	if t, ep, pos := m.latestWatched(); t != nil {
		at := ""
		if pos > 0 {
			at = fmt.Sprintf(i18n.TuiEpAt, int(pos)/60, int(pos)%60)
		}
		items = append(items,
			item{header: true, title: i18n.TuiBlockContinue},
			item{
				icon:       m.ic.Play,
				title:      fmt.Sprintf(i18n.TuiContinuePfx, titleName(t), ep),
				meta:       at,
				iconAccent: true,
				payload:    payloadResume{ref: t.Sources[0], ep: ep},
			})
		own++
	}

	var lib []item
	for _, e := range m.eng.Lib.Entries {
		if e.Hidden {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		badge := ""
		if n := m.newEpisodes(t, e); n > 0 {
			badge = i18n.NewEpisodes(n)
		}
		lib = append(lib, item{
			title:   titleName(t),
			meta:    stateLabel(e.State),
			badge:   badge,
			role:    "lib",
			payload: payloadTitle{ref: t.Sources[0]},
		})
	}
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
		item{icon: m.ic.Search, title: i18n.TuiSearchItem, payload: payloadSearch{}})
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

func (m *Model) latestWatched() (*library.LocalTitle, int, float64) {
	var best *library.Progress
	for _, p := range m.eng.Lib.Progress {
		// Прибраний із бібліотеки тайтл не висить у «Продовжити»:
		// прогрес лишається в журналі й повернеться разом із тайтлом.
		if e := m.eng.Lib.EntryLookup(p.TitleID); e != nil && e.Hidden {
			continue
		}
		if best == nil || p.WatchedAt.After(best.WatchedAt) {
			best = p
		}
	}
	if best == nil {
		return nil, 0, 0
	}
	t := m.titleByID(best.TitleID)
	if t == nil {
		return nil, 0, 0
	}
	ep, pos, ok := m.eng.Lib.Resume(best.TitleID)
	if !ok {
		return nil, 0, 0
	}
	return t, ep, pos
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

// newEpisodes — скільки серій вийшло після базової лінії тайтлу. Фонова
// перевірка має пріоритет; поки її немає, рахуємо з кешу на диску, щоб бейдж
// стояв уже в першому кадрі, а не з'являвся через секунду після нього.
func (m *Model) newEpisodes(t *library.LocalTitle, e *library.Entry) int {
	if e.State != library.StateWatching && e.State != library.StatePlanned {
		return 0
	}
	if n, ok := m.badges[t.ID]; ok {
		return n
	}
	if m.eng.Store == nil || len(t.Sources) == 0 {
		return 0
	}
	eps, _, found := m.eng.Store.LoadEpisodes(t.Sources[0])
	if !found {
		return 0
	}
	return newEpisodeCount(eps, max(e.LastEpisode, e.KnownEpisodes))
}

func newEpisodeCount(eps []provider.Episode, baseline int) int {
	numbers := make(map[int]struct{})
	for _, ep := range eps {
		if ep.Number > baseline {
			numbers[ep.Number] = struct{}{}
		}
	}
	return len(numbers)
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
