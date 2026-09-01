// Package ui — TUI на bubbletea v2. Один компонент списку, перевикористаний
// усіма екранами; кожен екран — «заголовок + список + один рядок підказки».
package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

type screen int

const (
	screenHome screen = iota
	screenSearch
	screenEpisodes
	screenStudio
	screenPlaying
	screenHistory
)

type frame struct {
	screen   screen
	items    []item
	cursor   int
	query    string
	ref      provider.TitleRef
	episodes []provider.Episode
	status   string
	// Стан пагінації пошуку: без нього Esc із тайтлу повертав би лише першу
	// сторінку, і «показати ще» починало б рахунок спочатку.
	cards   []provider.TitleCard
	page    int
	hasMore bool
}

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
	iconAccent bool   // іконка в акцентному кольорі
	role       string // розрізняє однаковий тайтл у бібліотеці й каталозі домівки
	payload    any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.meta }

// FilterValue: заголовок секції не має збігатися ні з чим, що вводить людина.
func (i item) FilterValue() string {
	if i.header {
		return "\x00"
	}
	return i.title
}

func (i item) key() string {
	if i.spacer {
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

// Повідомлення асинхронних команд.
type (
	searchDoneMsg struct {
		cards   []provider.TitleCard
		hasMore bool
		page    int // сторінка, яку просили: 1 замінює список, решта — дозаписує
		err     error
		req     int
	}
	episodesDoneMsg struct {
		ref      provider.TitleRef
		eps      []provider.Episode
		err      error
		offline  bool
		req      int
		navigate bool
	}
	resolvedMsg struct {
		res *playback.Resolved
		err error
		req int
	}
	playDoneMsg struct {
		result *playback.Result
		err    error
	}
	// catalogMsg і badgesMsg — пасивні: вони не ведуть нікуди й тому не мають
	// req. Фонове оновлення каталогу не має права ні скасувати навігацію,
	// ні перемалювати екран, на якому людина зараз працює.
	catalogMsg struct {
		kind  provider.CatalogKind
		cards []provider.TitleCard // nil — помилка або мережі немає
	}
	badgesMsg struct {
		counts map[string]int // локальний ID тайтла → скільки нових серій
	}
	bookmarkBaselineMsg struct {
		titleID     string
		ref         provider.TitleRef
		provisional int
		maxEp       int
		err         error
	}
)

type Model struct {
	eng   *playback.Engine
	list  list.Model
	input textinput.Model
	ic    icons

	screen  screen
	w, h    int
	status  string
	errText string

	// контекст поточного тайтлу
	ref        provider.TitleRef
	episodes   []provider.Episode
	pendingEp  int
	stack      []frame
	pending    *frame
	pendingReq int
	reqID      int

	// стан екрана пошуку
	query   string
	cards   []provider.TitleCard
	page    int
	hasMore bool

	// Блоки каталогу й лічильники нових серій живуть у моделі, а не в списку:
	// список перебудовується на кожному переході, а ці дані переживають його.
	catalog     map[provider.CatalogKind][]provider.TitleCard
	badges      map[string]int
	homeSpacers bool

	playCancel      context.CancelFunc
	pendingBaseline *bookmarkBaselineMsg
}

// catalogKinds — порядок блоків каталогу на домівці, він же порядок запитів.
var catalogKinds = []provider.CatalogKind{provider.CatalogTopSeason, provider.CatalogFresh}

const (
	// homeCatalogRows — скільки карток блоку показуємо. Домівка — не каталог:
	// шість рядків читаються одним поглядом, двадцять — це вже окремий екран.
	homeCatalogRows = 6
	// maxBadgeProbes — стеля на кількість тайтлів, які перевіряємо у фоні.
	maxBadgeProbes = 20
	badgeWorkers   = 4
)

func New(eng *playback.Engine) Model {
	ic := themeIcons(os.Getenv("UAANIME_ASCII") == "1")
	l := list.New(nil, rowDelegate{ic: ic}, 0, 0)
	l.Styles = listStyles()
	// Крапки пагінації list.New уже скопіював у Paginator — оновлюємо їх окремо.
	l.Paginator.ActiveDot = l.Styles.ActivePaginationDot.String()
	l.Paginator.InactiveDot = l.Styles.InactivePaginationDot.String()
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(true)
	l.DisableQuitKeybindings()

	in := textinput.New()
	in.Prompt = ic.Search + " "
	in.Placeholder = i18n.TuiSearchPrompt
	in.SetWidth(lipgloss.Width(in.Placeholder))
	in.SetStyles(searchInputStyles())
	in.SetVirtualCursor(false)

	m := Model{
		eng:     eng,
		list:    l,
		input:   in,
		ic:      ic,
		catalog: map[provider.CatalogKind][]provider.TitleCard{},
		badges:  map[string]int{},
	}
	m.loadCachedCatalog()
	m.showHome()
	return m
}

// loadCachedCatalog читає блоки каталогу з диска — будь-якої свіжості й без
// мережі. Перший кадр не має права чекати на HTTP: застарілий топ сезону
// краще за порожню домівку, а фонове оновлення замінить його за секунди.
func (m *Model) loadCachedCatalog() {
	if !m.catalogEnabled() {
		return
	}
	id := m.eng.Provider.ID()
	for _, kind := range catalogKinds {
		if cards, _, found := m.eng.Store.LoadCatalog(id, kind); found && len(cards) > 0 {
			m.catalog[kind] = cards
		}
	}
}

// catalogEnabled: провайдера може не бути зовсім (тести, headless), а той, що
// є, може не вміти каталог — тоді блоків просто немає, без порожніх заголовків.
func (m *Model) catalogEnabled() bool {
	return m.eng != nil && m.eng.Store != nil && m.eng.Provider != nil && m.eng.Provider.Caps().Catalog
}

// Run запускає TUI. Паніка не долітає до користувача: bubbletea відновлює
// термінал, а корінь main має власний recover.
func Run(eng *playback.Engine) error {
	_, err := tea.NewProgram(New(eng)).Run()
	return err
}

// Init стартує фонове оновлення домівки. Жодна з цих команд не блокує перший
// кадр: він уже намальований із того, що лежало на диску.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.catalogEnabled() {
		for _, kind := range catalogKinds {
			cmds = append(cmds, m.catalogCmd(kind))
		}
	}
	if cmd := m.badgesCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// ---- побудова екранів ----

func (m *Model) setItems(items []item, cursor int) tea.Cmd {
	current := m.list.Index()
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	cmd := m.list.SetItems(li)
	if len(items) == 0 {
		return cmd
	}
	if cursor < 0 {
		cursor = current
	}
	cursor = max(0, min(cursor, len(items)-1))
	m.list.Select(cursor)
	return cmd
}

func (m *Model) snapshot() frame {
	items := make([]item, 0, len(m.list.Items()))
	for _, it := range m.list.Items() {
		if it, ok := it.(item); ok {
			items = append(items, it)
		}
	}
	return frame{
		screen:   m.screen,
		items:    items,
		cursor:   m.list.GlobalIndex(),
		query:    m.query,
		ref:      m.ref,
		episodes: append([]provider.Episode(nil), m.episodes...),
		status:   m.status,
		cards:    append([]provider.TitleCard(nil), m.cards...),
		page:     m.page,
		hasMore:  m.hasMore,
	}
}

func (m *Model) nextReq() int {
	m.reqID++
	return m.reqID
}

func (m *Model) commitPending(req int) {
	if m.pending == nil || m.pendingReq != req {
		return
	}
	m.stack = append(m.stack, *m.pending)
	m.pending = nil
	m.pendingReq = 0
}

func (m *Model) back() {
	if m.pending != nil {
		m.pending = nil
		m.pendingReq = 0
		m.nextReq()
		m.status = ""
		return
	}

	m.nextReq()
	if len(m.stack) == 0 {
		m.showHome()
		return
	}

	f := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	if f.screen == screenHome {
		m.showHome()
		if len(m.list.Items()) > 0 {
			// Секції могли перебудуватись, поки нас не було, — сповзаємо
			// із заголовка, якщо збережений індекс потрапив саме на нього.
			m.list.Select(max(0, min(f.cursor, len(m.list.Items())-1)))
			m.skipHeaders(1)
		}
		return
	}

	m.ref = f.ref
	m.episodes = f.episodes
	m.query = f.query
	m.status = f.status
	m.cards = f.cards
	m.page = f.page
	m.hasMore = f.hasMore
	m.setScreen(f.screen)
	// setScreen скидає делегата в однорядковий — картки пошуку повертаємо
	// у двох рядках, інакше після Esc список раптом міняє вигляд.
	if f.screen == screenSearch && len(f.cards) > 0 {
		m.setDelegate(true)
	}
	_ = m.setItems(f.items, -1)
	if len(f.items) > 0 {
		m.list.Select(f.cursor)
	}
	if f.screen == screenSearch {
		m.input.SetValue(f.query)
		m.input.Blur()
	}
}

func (m *Model) setDelegate(twoLine bool) {
	m.list.SetDelegate(rowDelegate{twoLine: twoLine, ic: m.ic})
}

// setScreen — єдине місце, де застосовується конфігурація списку, залежна від
// екрана. Інакше налаштування протікають між екранами: список один на всіх.
func (m *Model) setScreen(s screen) {
	m.screen = s
	m.setDelegate(false)
	m.list.ResetFilter()
	// Домівка — це секції з дій, а не однорідний список; «/» там означає
	// «шукати нове», тому вбудований фільтр вимкнено.
	m.list.SetFilteringEnabled(s != screenHome)
	m.relayout()
}

// firstRow — індекс першого рядка, який можна вибрати. Курсор ніколи не стоїть
// на заголовку секції.
func firstRow(items []item) int {
	for i, it := range items {
		if !it.header {
			return i
		}
	}
	return 0
}

func isHeaderAt(items []list.Item, i int) bool {
	if i < 0 || i >= len(items) {
		return false
	}
	it, ok := items[i].(item)
	return ok && it.header
}

// skipHeaders зсуває курсор далі в напрямку dir, поки той стоїть на заголовку
// секції. Якщо в цьому напрямку рядків більше немає (курсор уперся в край
// списку) — відходимо назад до найближчого рядка у зворотному напрямку.
func (m *Model) skipHeaders(dir int) {
	items := m.list.Items()
	if dir == 0 || len(items) == 0 {
		return
	}
	i := m.list.Index()
	for isHeaderAt(items, i) && i+dir >= 0 && i+dir < len(items) {
		i += dir
	}
	if isHeaderAt(items, i) {
		for j := i - dir; j >= 0 && j < len(items); j -= dir {
			if !isHeaderAt(items, j) {
				i = j
				break
			}
		}
	}
	if !isHeaderAt(items, i) && i != m.list.Index() {
		m.list.Select(i)
	}
}

// navDirection — куди рухався курсор, якщо клавіша належить навігації списку.
// «На початок» рахуємо рухом уперед, «у кінець» — назад: саме туди треба
// зісковзнути із заголовка, що опинився на краю.
func navDirection(key string) int {
	switch key {
	case "up", "k", "left", "h", "pgup", "b", "u", "end", "G":
		return -1
	case "down", "j", "right", "l", "pgdown", "f", "d", "home", "g":
		return 1
	}
	return 0
}

const chromeBase = 4

func (m *Model) chromeHeight() int {
	if m.bannerVisible() {
		return brandChromeHeight
	}
	return chromeBase
}

func (m *Model) listHeight() int {
	n := m.h - m.chromeHeight()
	if m.screen == screenSearch {
		n--
	}
	return max(1, n)
}

func (m *Model) relayout() {
	m.list.SetSize(m.w-2, m.listHeight())
	if m.w > 0 {
		m.input.SetWidth(max(1, m.w-4))
	}
}

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
		gap := 1
		if blocks == 0 {
			gap = 2
		}
		items = sectionGap(items, gap, m.homeSpacers)
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

// showHistory — переглянуті тайтли, згруповані за найсвіжішим прогресом.
func (m *Model) showHistory() {
	m.setScreen(screenHistory)
	sorted := make([]*library.Progress, len(m.eng.Lib.Progress))
	copy(sorted, m.eng.Lib.Progress)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].WatchedAt.After(sorted[j].WatchedAt) })

	type historyGroup struct {
		newest *library.Progress
		count  int
	}
	groups := make([]historyGroup, 0, len(sorted))
	groupByTitle := make(map[string]int, len(sorted))
	for _, p := range sorted {
		if index, ok := groupByTitle[p.TitleID]; ok {
			groups[index].count++
			continue
		}
		groupByTitle[p.TitleID] = len(groups)
		groups = append(groups, historyGroup{newest: p, count: 1})
	}

	now := time.Now()
	var items []item
	for _, group := range groups {
		p := group.newest
		t := m.titleByID(p.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		icon := m.ic.Play
		if p.Completed {
			icon = m.ic.Done
		}
		items = append(items, item{
			icon:  icon,
			title: titleName(t),
			meta: strings.Join([]string{
				fmt.Sprintf(i18n.TuiEpisodeNo, p.Episode),
				i18n.Episodes(group.count),
				humanDate(p.WatchedAt, now),
			}, " · "),
			payload: payloadResume{ref: t.Sources[0], ep: p.Episode},
		})
		if len(items) == 20 {
			break
		}
	}
	_ = m.setItems(items, 0)
}

// titleName: тайтли, зіграні headless-командою, ще не мають назви — показуємо слаг.
func titleName(t *library.LocalTitle) string {
	if t.Name != "" {
		return t.Name
	}
	if len(t.Sources) > 0 {
		return t.Sources[0].Slug
	}
	return t.ID
}

func (m *Model) titleByID(id string) *library.LocalTitle {
	for _, t := range m.eng.Lib.Titles {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func stateLabel(s library.State) string {
	switch s {
	case library.StateCompleted:
		return i18n.TuiStateDone
	case library.StatePlanned:
		return i18n.TuiStatePlanned
	default:
		return i18n.TuiStateWatching
	}
}

func (m *Model) showEpisodes() tea.Cmd {
	m.setScreen(screenEpisodes)
	title := m.eng.Lib.TitleByRef(m.ref)
	var items []item
	for _, ep := range m.episodes {
		icon, meta := m.ic.Pending, releasesSummary(ep.Releases)
		badge := ""
		if title != nil {
			if p := m.eng.Lib.ProgressFor(title.ID, ep.Number); p != nil {
				if p.Completed {
					icon, meta, badge = m.ic.Done, "", i18n.TuiEpDone
				} else if p.PositionSec > 0 {
					icon = m.ic.Play
					meta = fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
				}
			}
		}
		items = append(items, item{
			icon:    icon,
			title:   fmt.Sprintf(i18n.TuiEpisodeNo, ep.Number),
			meta:    meta,
			badge:   badge,
			payload: payloadEp{num: ep.Number},
		})
	}
	return m.setItems(items, 0)
}

func releasesSummary(rels []provider.Release) string {
	seen := map[string]bool{}
	var studios []string
	for _, r := range rels {
		if !seen[r.Studio] {
			seen[r.Studio] = true
			studios = append(studios, r.Studio)
		}
	}
	sort.Strings(studios)
	if len(studios) > 3 {
		return fmt.Sprintf("%s, %s, %s %s", studios[0], studios[1], studios[2],
			fmt.Sprintf(i18n.TuiMoreStudios, len(studios)-3))
	}
	if len(studios) == 0 {
		return ""
	}
	out := studios[0]
	for _, s := range studios[1:] {
		out += ", " + s
	}
	return out
}

func (m *Model) showStudioChoice(candidates []provider.Source) {
	m.setScreen(screenStudio)
	seen := map[string]bool{}
	var items []item
	for _, s := range candidates {
		key := s.Studio + "|" + string(s.Kind)
		if seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, item{
			title:   s.Studio,
			meta:    string(s.Kind),
			payload: payloadStudio{src: s},
		})
	}
	_ = m.setItems(items, 0)
}

// searchRows — картки як рядки списку плюс, за потреби, «показати ще».
// Рядки й картки йдуть один в один: індекс картки == індекс рядка, тому
// курсор після довантаження ставиться простою арифметикою.
func (m *Model) searchRows() []item {
	items := make([]item, 0, len(m.cards)+1)
	for _, c := range m.cards {
		items = append(items, item{
			title:     c.Name,
			meta:      cardMeta(c),
			metaParts: cardMetaParts(c),
			badge:     m.titleStateBadge(c.TitleRef),
			payload:   payloadTitle{ref: c.TitleRef, epAired: c.EpAired},
		})
	}
	if m.hasMore {
		items = append(items, item{icon: m.ic.Pending, title: i18n.TuiShowMore, payload: payloadMore{}})
	}
	return items
}

func (m *Model) titleStateBadge(ref provider.TitleRef) string {
	title := m.eng.Lib.TitleByRef(ref)
	if title == nil {
		return ""
	}
	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry == nil || entry.Hidden {
		return ""
	}
	return stateLabel(entry.State)
}

// applySearchPage — перша сторінка замінює результати, наступні дозаписуються.
// Курсор при довантаженні стає на перший нововантажений рядок: людина натиснула
// «показати ще» саме заради нього, повертати її на початок списку — образливо.
func (m *Model) applySearchPage(msg searchDoneMsg) tea.Cmd {
	if msg.page <= 1 {
		m.cards, m.page, m.hasMore = msg.cards, 1, msg.hasMore
		if len(m.cards) == 0 {
			m.hasMore = false
			m.setDelegate(false)
			m.status = i18n.TuiNothingFound
			return m.setItems(nil, 0)
		}
		m.setDelegate(true)
		return m.setItems(m.searchRows(), 0)
	}

	first := len(m.cards)
	m.cards = append(m.cards, msg.cards...)
	m.page, m.hasMore = msg.page, msg.hasMore
	m.setDelegate(true)
	cursor := first
	// first — індекс у повному списку, а Select працює у видимому просторі.
	// Під активним фільтром вони не збігаються — лишаємо курсор на місці.
	if m.list.FilterState() != list.Unfiltered {
		cursor = m.list.Index()
	}
	return m.setItems(m.searchRows(), cursor)
}

// ---- асинхронні команди ----

func (m *Model) searchCmd(q string, page, req int) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		p, err := eng.Provider.Search(ctx, q, page)
		return searchDoneMsg{cards: p.Titles, hasMore: p.HasMore, page: page, err: err, req: req}
	}
}

func (m *Model) episodesCmd(ref provider.TitleRef, req int, navigate bool) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		eps, offline, err := eng.EpisodesCached(ctx, ref)
		return episodesDoneMsg{ref: ref, eps: eps, err: err, offline: offline, req: req, navigate: navigate}
	}
}

func (m *Model) bookmarkBaselineCmd(titleID string, ref provider.TitleRef, provisional int) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		eps, err := eng.EpisodesFresh(ctx, ref)
		return bookmarkBaselineMsg{
			titleID: titleID, ref: ref, provisional: provisional,
			maxEp: maxEpisodeNumber(eps), err: err,
		}
	}
}

func (m *Model) resolveCmd(ref provider.TitleRef, ep, req int) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		res, err := eng.Resolve(ctx, ref, ep, nil)
		return resolvedMsg{res: res, err: err, req: req}
	}
}

// catalogCmd оновлює один блок каталогу у фоні. Помилка мовчазна: домівка вже
// показана, і червоний рядок про недоступний топ сезону нічого не додає.
func (m *Model) catalogCmd(kind provider.CatalogKind) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cards, _, err := eng.CatalogCached(ctx, kind)
		if err != nil {
			return catalogMsg{kind: kind}
		}
		return catalogMsg{kind: kind, cards: cards}
	}
}

// badgesCmd рахує нові серії для тайтлів у перегляді й запланованих. Один спільний
// дедлайн на всі перевірки й обмежений паралелізм: двадцять послідовних
// запитів тривали б довше, ніж людина дивиться на домівку, а двадцять
// одночасних виглядали б для сайту як атака.
func (m *Model) badgesCmd() tea.Cmd {
	if m.eng == nil || m.eng.Provider == nil || m.eng.Lib == nil {
		return nil
	}
	type probe struct {
		id       string
		ref      provider.TitleRef
		baseline int
	}
	var probes []probe
	for _, e := range m.eng.Lib.Entries {
		if e.Hidden || (e.State != library.StateWatching && e.State != library.StatePlanned) {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		probes = append(probes, probe{
			id:       t.ID,
			ref:      t.Sources[0],
			baseline: max(e.LastEpisode, e.KnownEpisodes),
		})
		if len(probes) == maxBadgeProbes {
			break
		}
	}
	if len(probes) == 0 {
		return nil
	}

	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		counts := make(map[string]int, len(probes))
		var mu sync.Mutex
		var wg sync.WaitGroup
		jobs := make(chan probe)
		for range badgeWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range jobs {
					eps, _, err := eng.EpisodesCached(ctx, p.ref)
					if err != nil {
						continue // недоступний тайтл не ховає бейджі решти
					}
					mu.Lock()
					counts[p.id] = newEpisodeCount(eps, p.baseline)
					mu.Unlock()
				}
			}()
		}
		for _, p := range probes {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		return badgesMsg{counts: counts}
	}
}

func (m *Model) playCmd(res *playback.Resolved) (tea.Cmd, context.CancelFunc) {
	eng := m.eng
	ctx, cancel := context.WithCancel(context.Background())
	return func() tea.Msg {
		result, err := eng.Play(ctx, res)
		return playDoneMsg{result: result, err: err}
	}, cancel
}

// ---- update ----

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		oldHomeSpacers := m.homeSpacers
		m.w, m.h = msg.Width, msg.Height
		m.relayout()
		if m.screen == screenHome && oldHomeSpacers != (m.list.Height() >= 16) {
			m.refreshHome()
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKeys(msg)

	case searchDoneMsg:
		if msg.req != m.reqID {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.pending = nil
			m.pendingReq = 0
			m.errText = errText(msg.err)
			return m, nil
		}
		return m, m.applySearchPage(msg)

	case catalogMsg:
		if msg.cards != nil {
			m.catalog[msg.kind] = msg.cards
		}
		m.refreshHome()
		return m, nil

	case badgesMsg:
		for id, n := range msg.counts {
			m.badges[id] = n
		}
		m.refreshHome()
		return m, nil

	case bookmarkBaselineMsg:
		if m.playCancel != nil {
			m.pendingBaseline = &msg
			return m, nil
		}
		status := m.status
		m.applyBookmarkBaseline(msg)
		if m.errText == "" {
			m.status = status
		}
		return m, nil

	case episodesDoneMsg:
		if msg.req != m.reqID {
			return m, nil
		}
		if msg.err != nil {
			m.status = ""
			m.pending = nil
			m.pendingReq = 0
			m.errText = errText(msg.err)
			return m, nil
		}
		m.ref = msg.ref
		m.episodes = msg.eps
		if !msg.navigate {
			if msg.offline {
				m.status = i18n.MsgOfflineCache
			}
			return m, nil
		}
		if title := m.eng.Lib.TitleByRef(msg.ref); title != nil {
			if entry := m.eng.Lib.EntryLookup(title.ID); entry != nil && entry.State == library.StatePlanned {
				// Відкриття запланованого тайтлу означає ознайомлення з наявними
				// серіями; у перегляді бейдж навмисно очищає лише сам перегляд.
				if err := m.eng.MarkSeen(msg.ref, maxEpisodeNumber(msg.eps)); err != nil {
					m.errText = err.Error()
				}
				m.badges[title.ID] = 0
			}
		}
		if msg.offline {
			m.status = i18n.MsgOfflineCache
		} else {
			m.status = ""
		}
		m.commitPending(msg.req)
		return m, m.showEpisodes()

	case resolvedMsg:
		if msg.req != m.reqID {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.pending = nil
			m.pendingReq = 0
			text := errText(msg.err)
			var cmd tea.Cmd
			if m.screen == screenPlaying {
				if len(m.episodes) > 0 {
					cmd = m.showEpisodes()
				} else {
					m.showHome()
				}
			}
			m.errText = text
			return m, cmd
		}
		m.commitPending(msg.req)
		res := msg.res
		// Одноразове питання: кілька студій і жодного піна. Після EOF автоплей
		// вже має пін від першого Play, тому посеред ланцюжка сюди не потрапить.
		title := m.eng.Lib.TitleByRef(m.ref)
		var entry *library.Entry
		if title != nil {
			entry = m.eng.Lib.EntryLookup(title.ID)
		}
		pinned := entry != nil && entry.StudioPin != ""
		if len(res.Candidates) > 1 && !pinned {
			m.stack = append(m.stack, m.snapshot())
			m.showStudioChoice(res.Candidates)
			return m, nil
		}
		return m.startPlayback(res)

	case playDoneMsg:
		if m.pendingBaseline != nil {
			m.applyBookmarkBaseline(*m.pendingBaseline)
			m.pendingBaseline = nil
		}
		m.playCancel = nil
		if msg.err == nil && msg.result != nil && msg.result.Reason == player.EndEOF && m.eng.Autoplay {
			if next, ok := playback.NextEpisodeNumber(m.episodes, m.pendingEp); ok {
				req := m.nextReq()
				m.pendingEp = next
				m.status = i18n.TuiResolving
				return m, m.resolveCmd(m.ref, next, req)
			}
		}
		if msg.err != nil {
			m.errText = fmt.Sprintf(i18n.MsgPlayerFailed, msg.err)
		} else if msg.result != nil && msg.result.Completed {
			m.status = fmt.Sprintf(i18n.MsgEpisodeDone, m.pendingEp)
		} else if msg.result != nil && msg.result.PositionSec > 0 {
			m.status = fmt.Sprintf(i18n.MsgProgressSaved,
				int(msg.result.PositionSec)/60, int(msg.result.PositionSec)%60)
		}
		// повертаємось на екран серій з оновленими станами
		var cmd tea.Cmd
		if len(m.episodes) > 0 {
			cmd = m.showEpisodes()
		} else {
			m.showHome()
		}
		return m, cmd
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) applyBookmarkBaseline(msg bookmarkBaselineMsg) {
	if msg.err != nil {
		return
	}
	_ = m.eng.ReconcileKnown(msg.ref, msg.provisional, msg.maxEp)
	delete(m.badges, msg.titleID)
	if title := m.titleByID(msg.titleID); title != nil {
		if entry := m.eng.Lib.EntryLookup(title.ID); entry != nil {
			m.badges[title.ID] = m.newEpisodes(title, entry)
		}
	}
	m.refreshHome()
}

func (m Model) startPlayback(res *playback.Resolved) (tea.Model, tea.Cmd) {
	if m.screen == screenStudio && len(m.stack) > 0 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.pendingEp = res.Episode
	m.setScreen(screenPlaying)
	m.status = i18n.TuiPlaying
	cmd, cancel := m.playCmd(res)
	m.playCancel = cancel
	return m, cmd
}

func (m Model) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		if m.playCancel != nil {
			m.playCancel()
		}
		return m, tea.Quit
	}
	if key == "esc" && m.pending != nil && m.screen != screenPlaying {
		m.back()
		return m, nil
	}

	// під час фільтрації всі клавіші належать списку
	if m.screen != screenSearch && m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	if (key == "m" || key == "M") && !m.list.SettingFilter() && (m.screen != screenSearch || !m.input.Focused()) {
		switch m.screen {
		case screenHome, screenSearch, screenEpisodes:
			return m.bookmarkSelected()
		}
	}

	switch m.screen {
	case screenHome:
		switch key {
		case "q", "Q":
			return m, tea.Quit
		case "enter":
			return m.openSelected()
		case "/":
			// На домівці фільтрувати нічого: «/» — це той самий «Пошук нового».
			return m.openSearch()
		}

	case screenSearch:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			// Enter у полі вводу — шукати; Enter на результаті — відкрити
			if m.input.Focused() {
				q := m.input.Value()
				if q == "" {
					return m, nil
				}
				m.input.Blur()
				m.status = i18n.TuiSearching
				m.pending = nil
				m.pendingReq = 0
				req := m.nextReq()
				m.query = q
				m.page, m.hasMore = 0, false
				return m, m.searchCmd(q, 1, req)
			}
			return m.openSelected()
		default:
			if m.input.Focused() {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			if key == "/" {
				m.input.SetValue("")
				return m, m.input.Focus()
			}
		}

	case screenEpisodes, screenHistory:
		switch key {
		case "esc":
			if m.list.FilterState() == list.FilterApplied {
				m.list.ResetFilter()
				return m, nil
			}
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		}

	case screenStudio:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		}

	case screenPlaying:
		if key == "esc" && m.playCancel != nil {
			m.playCancel()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	// Курсор ніколи не зупиняється на заголовку секції: він не робить нічого,
	// тож зупинка на ньому виглядає як зависання списку.
	m.skipHeaders(navDirection(key))
	return m, cmd
}

func (m Model) bookmarkSelected() (tea.Model, tea.Cmd) {
	var ref provider.TitleRef
	baseline := 0

	if m.screen == screenEpisodes {
		ref = m.ref
		baseline = maxEpisodeNumber(m.episodes)
	} else {
		it, ok := m.list.SelectedItem().(item)
		if !ok || it.header {
			return m, nil
		}
		switch payload := it.payload.(type) {
		case payloadTitle:
			ref, baseline = payload.ref, payload.epAired
		case payloadResume:
			ref = payload.ref
		default:
			return m, nil
		}
	}

	// Нуль у картці означає «невідомо», тому локальний кеш дає кращу
	// базову лінію без мережевого запиту й без запуску фонового узгодження.
	if baseline == 0 && m.eng != nil && m.eng.Store != nil {
		if episodes, _, found := m.eng.Store.LoadEpisodes(ref); found {
			baseline = maxEpisodeNumber(episodes)
		}
	}

	m.errText = ""
	result, err := m.eng.Bookmark(ref, baseline)
	if err != nil {
		m.errText = err.Error()
		return m, nil
	}
	var refreshCmd tea.Cmd
	switch m.screen {
	case screenHome:
		m.refreshHome()
	case screenSearch:
		// setItems завершується list.Select, а той працює у видимому
		// (відфільтрованому) просторі — тому тут саме Index(), не GlobalIndex():
		// searchRows перебудовує ті самі назви в тому ж порядку, і видима
		// позиція під фільтром не змінюється.
		refreshCmd = m.setItems(m.searchRows(), m.list.Index())
	}
	if result == library.BookmarkAdded {
		m.status = i18n.TuiBookmarkAdded
		title := m.eng.Lib.TitleByRef(ref)
		if title == nil {
			return m, refreshCmd
		}
		return m, tea.Batch(refreshCmd, m.bookmarkBaselineCmd(title.ID, ref, baseline))
	} else {
		m.status = i18n.TuiBookmarkRemoved
	}
	return m, refreshCmd
}

func maxEpisodeNumber(episodes []provider.Episode) int {
	maximum := 0
	for _, episode := range episodes {
		if episode.Number > maximum {
			maximum = episode.Number
		}
	}
	return maximum
}

// openSearch — вхід на екран пошуку; спільний для «Пошуку нового» і клавіші «/».
func (m Model) openSearch() (tea.Model, tea.Cmd) {
	m.stack = append(m.stack, m.snapshot())
	m.setScreen(screenSearch)
	_ = m.setItems(nil, 0)
	m.errText = ""
	m.status = ""
	m.query = ""
	m.cards, m.page, m.hasMore = nil, 0, false
	m.input.SetValue("")
	return m, m.input.Focus()
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.header {
		return m, nil
	}
	m.errText = ""
	switch p := it.payload.(type) {
	case payloadSearch:
		return m.openSearch()
	case payloadHistory:
		m.stack = append(m.stack, m.snapshot())
		m.showHistory()
		return m, nil
	case payloadResume:
		snap := m.snapshot()
		m.pending = nil
		m.pendingReq = 0
		req := m.nextReq()
		m.pending = &snap
		m.pendingReq = req
		m.ref = p.ref
		m.pendingEp = p.ep
		m.status = i18n.TuiResolving
		// серії підтягнемо у фоні, щоб після перегляду показати список
		return m, tea.Batch(m.resolveCmd(p.ref, p.ep, req), m.episodesCmd(p.ref, req, false))
	case payloadTitle:
		snap := m.snapshot()
		m.pending = nil
		m.pendingReq = 0
		req := m.nextReq()
		m.pending = &snap
		m.pendingReq = req
		m.ref = p.ref
		m.status = i18n.TuiSearching
		return m, m.episodesCmd(p.ref, req, true)
	case payloadMore:
		// Довантаження — теж навігаційна дія: свій req, старі відповіді летять у смітник.
		m.pending = nil
		m.pendingReq = 0
		req := m.nextReq()
		m.status = i18n.TuiSearching
		return m, m.searchCmd(m.query, m.page+1, req)
	case payloadEp:
		m.pending = nil
		m.pendingReq = 0
		req := m.nextReq()
		m.pendingEp = p.num
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, p.num, req)
	case payloadStudio:
		if err := m.eng.PinStudio(m.ref, p.src.Studio); err != nil {
			m.errText = err.Error()
			return m, nil
		}
		m.pending = nil
		m.pendingReq = 0
		req := m.nextReq()
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, m.pendingEp, req)
	}
	return m, nil
}

// ---- view ----

func (m Model) View() tea.View {
	var title string
	switch m.screen {
	case screenSearch:
		title = i18n.TuiSearchTitle
	case screenEpisodes, screenPlaying:
		if t := m.eng.Lib.TitleByRef(m.ref); t != nil && t.Name != "" {
			title = t.Name
		} else if m.ref.Name != "" {
			title = m.ref.Name
		} else {
			title = i18n.TuiAppTitle
		}
	case screenStudio:
		title = i18n.TuiStudioTitle
	case screenHistory:
		title = i18n.TuiHistoryItem
	default:
		title = i18n.TuiAppTitle
	}

	var body string
	if m.screen == screenHome {
		if m.bannerVisible() {
			body = m.brandHeader()
		} else {
			body = m.brandFallbackTitle() + "\n"
		}
	} else {
		body = styleTitle.Render(title) + "\n"
	}
	if m.screen == screenSearch {
		body += "  " + m.input.View() + "\n"
	}
	if m.screen != screenPlaying {
		listView := m.list.View()
		if len(m.list.Items()) == 0 {
			// Не даємо bubbles показати англійське «No items.» і тримаємо
			// геометрію сталою навіть до появи результатів.
			listView = lipgloss.NewStyle().Height(m.listHeight()).Render(styleStatus.Render(""))
		}
		body += listView + "\n"
	}

	switch {
	case m.errText != "":
		body += styleErr.Render(m.errText)
	case m.status != "":
		body += styleStatus.Render(m.status)
	default:
		body += styleHint.Render(m.hint())
	}

	v := tea.NewView(body)
	if m.screen == screenSearch {
		if c := m.input.Cursor(); c != nil {
			c.X += 2
			c.Y += 2
			v.Cursor = c
		}
	}
	v.AltScreen = true
	return v
}

func (m Model) hint() string {
	switch m.screen {
	case screenSearch:
		return i18n.TuiHintSearch
	case screenEpisodes:
		return i18n.TuiHintEpisodes
	case screenStudio:
		return i18n.TuiHintStudio
	case screenHistory:
		return i18n.TuiHintList
	default:
		return i18n.TuiHintHome
	}
}
