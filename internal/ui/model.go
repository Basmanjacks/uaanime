// Package ui — TUI на bubbletea v2. Один компонент списку, перевикористаний
// усіма екранами; кожен екран — «заголовок + список + один рядок підказки».
package ui

import (
	"context"
	"os"
	"slices"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
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

// view — стан екрана, який переживає перехід і повертається разом із кадром
// стека. Одна структура замість десятка полів, продубльованих у frame: додати
// поле й забути скопіювати його назад тут неможливо.
type view struct {
	// контекст поточного тайтлу
	ref      provider.TitleRef
	episodes []provider.Episode
	status   string

	// стан екрана пошуку
	query string
	// Стан пагінації пошуку: без нього Esc із тайтлу повертав би лише першу
	// сторінку, і «показати ще» починало б рахунок спочатку.
	cards   []provider.TitleCard
	page    int
	hasMore bool
}

// clone — копія, яку не зачепить наступний пошук: слайси в моделі
// дозаписуються на місці, тому кадр стека мусить володіти своїми.
func (v view) clone() view {
	v.episodes = slices.Clone(v.episodes)
	v.cards = slices.Clone(v.cards)
	return v
}

type Model struct {
	eng   *playback.Engine
	list  list.Model
	input textinput.Model
	ic    icons

	view
	screen  screen
	w, h    int
	errText string

	pendingEp  int
	stack      []frame
	pending    *frame
	pendingReq int
	reqID      int

	// Блоки каталогу й лічильники нових серій живуть у моделі, а не в списку:
	// список перебудовується на кожному переході, а ці дані переживають його.
	catalog     map[provider.CatalogKind][]provider.TitleCard
	badges      map[string]int
	homeSpacers bool

	// Стан активної сесії. titleID і pinned дає Begin на горутині Update:
	// фонова команда не має права читати бібліотеку, щоб дізнатися їх.
	playCancel      context.CancelFunc
	playTitleID     string
	playPinned      string
	quitting        bool
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
//
// WithoutSignalHandler обов'язковий: вбудований обробник bubbletea v2.0.9
// (tea.go:662-684) на SIGTERM кладе QuitMsg у чергу і завершує цикл ДО
// playDoneMsg, тобто до Finish — журнал не злився б, а плеєр лишився б сиротою.
// Сигнали ловить ctx викликача і заходять у модель як signalMsg.
func Run(ctx context.Context, eng *playback.Engine) error {
	p := tea.NewProgram(New(eng), tea.WithoutSignalHandler())
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			p.Send(signalMsg{})
		case <-done: // програма вже вийшла — горутина не має висіти вічно
		}
	}()
	_, err := p.Run()
	close(done)
	return err
}

// signalMsg — SIGINT/SIGTERM, доставлений у модель тим самим шляхом, що й
// клавіші: власник сигналів має бути один, інакше вихід гониться з Finish.
type signalMsg struct{}

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
