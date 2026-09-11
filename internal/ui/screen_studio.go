package ui

import (
	"fmt"
	"sort"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// showStudioChoice — пікер релізів. Рядок = пара (студія, тип): студія з
// озвученням і субтитрами дає два рядки, а покриття рахується по парі, тому
// «11/11» більше не бреше про озвучення, якого є 10/11.
//
// playable — пари поточної серії з відомим екстрактором; failed — ті з них,
// чий хост щойно не відповів (закріпити можна, але з попередженням); willPlay —
// та, що обере резолв (nil, коли невідомо). Пари з інших серій теж видно: людина
// має бачити весь вибір тайтлу, а не лише те, що встигло вийти для цієї серії.
func (m *Model) showStudioChoice(playable, failed []provider.Source, willPlay *provider.Source) {
	m.setScreen(screenStudio)
	pin := m.studioPin()
	episodes, _ := m.currentEpisodes()
	coverage, total := library.ReleaseCoverage(episodes)

	type row struct {
		src      provider.Source
		playable bool
		failed   bool
		inMeta   bool // є в метаданих поточної серії (але, можливо, без екстрактора)
		coverage int
	}
	rows := map[provider.Release]*row{}
	pairOf := func(s provider.Source) provider.Release { return provider.Release{Studio: s.Studio, Kind: s.Kind} }
	for _, s := range playable {
		key := pairOf(s)
		if _, ok := rows[key]; ok {
			continue
		}
		rows[key] = &row{src: s, playable: true, inMeta: true, coverage: coverage[key]}
	}
	for _, s := range failed {
		if r, ok := rows[pairOf(s)]; ok {
			r.failed = true
		}
	}
	for key, n := range coverage {
		if _, ok := rows[key]; ok {
			continue
		}
		rows[key] = &row{src: provider.Source{Studio: key.Studio, Kind: key.Kind, Episode: m.pendingEp}, coverage: n}
	}
	for _, ep := range episodes {
		if ep.Number != m.pendingEp {
			continue
		}
		for _, r := range provider.CleanEpisode(ep).Releases {
			if row, ok := rows[r]; ok {
				row.inMeta = true
			}
		}
	}

	ordered := make([]provider.Release, 0, len(rows))
	for key := range rows {
		ordered = append(ordered, key)
	}
	pinned := m.pinnedPair(pin, ordered)
	isWill := func(key provider.Release) bool {
		return willPlay != nil && key == pairOf(*willPlay)
	}
	rank := func(key provider.Release) int {
		switch {
		case key == pinned:
			return 0
		case isWill(key):
			return 1
		case rows[key].playable:
			return 2
		default:
			return 3
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if rows[a].coverage != rows[b].coverage {
			return rows[a].coverage > rows[b].coverage
		}
		if kindRank(a.Kind) != kindRank(b.Kind) {
			return kindRank(a.Kind) < kindRank(b.Kind)
		}
		return a.Studio < b.Studio
	})

	// Явний пін на субтитри — лише коли був вибір: якщо серед рядків немає
	// жодного не-sub релізу, людина не обирала саби замість озвучення, і пін
	// лишається wildcard — озвучення ввімкнеться, щойно з'явиться.
	offersNonSub := false
	for _, key := range ordered {
		if key.Kind != provider.KindSub {
			offersNonSub = true
		}
	}
	items := make([]item, 0, len(ordered))
	for _, key := range ordered {
		r := rows[key]
		pinKind := r.src.Kind
		if pinKind == provider.KindSub && !offersNonSub {
			pinKind = ""
		}
		it := item{
			title:   r.src.Studio,
			meta:    i18n.KindShort(r.src.Kind),
			payload: payloadStudio{src: r.src, pinKind: pinKind, unplayable: !r.playable && r.inMeta},
		}
		// Покриття йде в мету, а не в бейдж: зелений бейдж читається як
		// «все добре», а «3/12» — це попередження, а не похвала. Нуль не
		// показуємо взагалі: список серій міг просто відстати від сайту.
		if r.coverage > 0 && total > 0 {
			it.meta += metaSep + fmt.Sprintf(i18n.TuiStudioCoverage, r.coverage, total)
		}
		switch {
		case key == pinned:
			it.icon, it.iconAccent = m.ic.Done, true
		case isWill(key):
			it.icon, it.iconAccent = m.ic.Play, true
		}
		switch {
		case isWill(key):
			it.badge = i18n.TuiPickWillPlay
		case r.failed:
			it.badge, it.badgeWarn = i18n.TuiPickFailed, true
		case !r.playable && r.inMeta:
			it.badge, it.badgeWarn = i18n.TuiPickUnplayable, true
		case !r.playable:
			it.badge, it.badgeWarn = i18n.TuiPickNotInEpisode, true
		}
		items = append(items, it)
	}
	_ = m.setItems(items, 0)
}

// pinnedPair — який рядок пікера позначити як закріплений. Явний пін — його
// пара; wildcard — тип студії, який Pick обрав би серед УСІХ пар (щоб вето на
// тихі саби діяло так само, як у резолві), а коли Pick іде в іншу студію —
// найкраще озвучення студії піна, бо саме воно ввімкнеться, щойно буде.
func (m *Model) pinnedPair(pin library.Pin, pairs []provider.Release) provider.Release {
	if pin.Studio == "" {
		return provider.Release{}
	}
	if pin.Kind != "" {
		return provider.Release{Studio: pin.Studio, Kind: pin.Kind}
	}
	all := make([]provider.Source, 0, len(pairs))
	for _, key := range pairs {
		all = append(all, provider.Source{Studio: key.Studio, Kind: key.Kind})
	}
	if chosen, _ := library.Pick(all, pin, m.eng.Prefs); chosen != nil && chosen.Studio == pin.Studio {
		return provider.Release{Studio: chosen.Studio, Kind: chosen.Kind}
	}
	best := provider.Release{}
	for _, key := range pairs {
		if key.Studio != pin.Studio || key.Kind == provider.KindSub {
			continue
		}
		if best.Studio == "" || kindRank(key.Kind) < kindRank(best.Kind) {
			best = key
		}
	}
	return best
}

// kindRank дублює порядок бібліотеки для сортування рядків пікера.
func kindRank(k provider.Kind) int {
	switch k {
	case provider.KindDub:
		return 0
	case provider.KindVoiceover:
		return 1
	case provider.KindMulti:
		return 2
	default:
		return 3
	}
}

// currentEpisodes — серії саме поточного тайтлу. m.episodes лишається від
// попереднього, поки не прийде episodesDoneMsg, тож без збігу ref беремо кеш
// із диска (як bookmarkSelected), а не чужий список.
func (m *Model) currentEpisodes() ([]provider.Episode, bool) {
	if len(m.episodes) > 0 && m.episodesRef.Same(m.ref) {
		return m.episodes, true
	}
	if m.eng == nil || m.eng.Store == nil {
		return nil, false
	}
	episodes, _, found := m.eng.Store.LoadEpisodes(m.ref)
	if !found || len(episodes) == 0 {
		return nil, false
	}
	return episodes, true
}

// publishPlaylist віддає пультові список серій поточного тайтлу. Викликається
// лише з горутини Update: усе, що треба з бібліотеки, знімається тут і летить у
// Live копією значень (правило 10). Без списку серій публікувати нічого —
// краще порожньо, ніж серії тайтлу, з якого ми вже пішли.
func (m *Model) publishPlaylist() {
	if m.eng == nil || m.eng.Live == nil {
		return
	}
	live := m.eng.Live
	episodes, ok := m.currentEpisodes()
	if !ok {
		live.ClearPlaylist()
		return
	}
	// Поточна серія є лише поки триває сесія: після playDoneMsg у списку не
	// підсвічується нічого.
	current := 0
	if m.playCancel != nil {
		current = m.pendingEp
	}
	title := m.eng.Lib.TitleByRef(m.ref)
	name := m.ref.Name
	if title != nil && title.Name != "" {
		name = title.Name
	}
	titleID := ""
	if title != nil {
		titleID = title.ID
	}
	rows := playback.BuildEpisodeInfo(episodes, titleID, m.eng.Lib.Progress, current)
	live.SetPlaylist(m.ref, name, rows)
}

// clearPlaylist — пульт більше не показує серій: ми пішли з екрана, що володіє
// тайтлом (див. setScreen).
func (m *Model) clearPlaylist() {
	if m.eng != nil {
		m.eng.Live.ClearPlaylist()
	}
}

// studioPin — пін поточного тайтлу значеннями.
func (m Model) studioPin() library.Pin {
	title := m.eng.Lib.TitleByRef(m.ref)
	if title == nil {
		return library.Pin{}
	}
	return m.pinFor(title.ID)
}

// pinLabel — пін для заголовка: «Рідний Голос · Озв», лише студія для
// wildcard, «авто» без піна.
func (m Model) pinLabel() string {
	pin := m.studioPin()
	switch {
	case pin.Studio == "":
		return i18n.TuiStudioAuto
	case pin.Kind == "":
		return pin.Studio
	default:
		return fmt.Sprintf(i18n.TuiRelPair, pin.Studio, i18n.KindShort(pin.Kind))
	}
}
