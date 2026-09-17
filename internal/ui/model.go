// Package ui — TUI на bubbletea v2. Один компонент списку, перевикористаний
// усіма екранами; кожен екран — «заголовок + список + один рядок підказки».
package ui

import (
	"context"
	"math/rand/v2"
	"os"
	"slices"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

type screen int

const (
	screenHome screen = iota
	screenSearch
	screenEpisodes
	screenStudio
	screenPlaying
	screenHistory
	screenSettings
	screenSettingValue
	screenBookmarks
	// Екрани завантажень. Вибір якості й папка — транзитні (кадр у стеку),
	// «Завантаження» — повноцінний екран зі списку домівки.
	screenDownloadQuality
	screenDownloads
	screenDownloadDir
)

// RemoteInfo — стан веб-пульта після (пере)запуску. Err — фатально: пульт не
// піднявся, URL порожній. Warn — пульт працює, але remote.json не записався:
// адреса може змінитися після перезапуску.
type RemoteInfo struct {
	URL       string
	AltURL    string
	Ephemeral bool // збережений порт був зайнятий — закладка цього разу не спрацює
	SavedPort int
	Err       error
	Warn      error
}

// Options — те, що TUI отримує від cmd і не може дістати сам: конфіг, стан
// пульта і два хуки, які живуть по той бік пакетної межі (детекція плеєра —
// шов для тестів cmd; перезапуск пульта — слухач і remote.json).
type Options struct {
	Debug   bool
	Cfg     *store.Config // nil → store.DefaultConfig() (тести)
	DataDir string        // каталог даних для екрана «Про»; "" → store.DataDir()
	Remote  RemoteInfo
	// RestartRemote перезапускає пульт під новий режим ("on"|"open"|"off").
	// nil = пульт недоступний (тести) — зберігається лише конфіг.
	RestartRemote func(mode string) RemoteInfo
	// DetectPlayer — player.Detect за швом cmd; nil → без перевірки наявності.
	DetectPlayer func(id string) (player.Player, bool, error)
	// Downloads — черга завантажень на диск; nil = завантаження недоступні
	// (тести без мережевого стенду). Менеджер належить cmd: він же його
	// закриває на виході разом із пультом.
	Downloads *download.Manager
	// Fetcher — той самий HTTP-клієнт завантажувача, що й у менеджера: план
	// якостей будується в UI, а транспорт (а з ним фікстури й тестові стенди)
	// живе в cmd. nil при непорожньому Downloads → власний із дефолтним
	// транспортом.
	Fetcher *httpx.Fetcher
}

// view — стан екрана, який переживає перехід і повертається разом із кадром
// стека. Одна структура замість десятка полів, продубльованих у frame: додати
// поле й забути скопіювати його назад тут неможливо.
type view struct {
	// контекст поточного тайтлу
	ref provider.TitleRef
	// episodesRef — чиї серії лежать в episodes. Перехід на інший тайтл не
	// чистить episodes, а resolvedMsg може випередити episodesDoneMsg, тож без
	// цієї мітки список попереднього тайтлу зійшов би за поточний.
	episodesRef provider.TitleRef
	episodes    []provider.Episode
	status      string

	// стан екрана пошуку
	query string
	// Стан пагінації пошуку: без нього Esc із тайтлу повертав би лише першу
	// сторінку, і «показати ще» починало б рахунок спочатку.
	cards   []provider.TitleCard
	page    int
	hasMore bool

	// settingID — яке налаштування відкрито на екрані значень; у view, щоб
	// заголовок пережив кадр стека.
	settingID settingID

	// dlTitle — заголовок екрана вибору якості («Завантажити серію 6 · Озв ·
	// FanVoxUA»). У view, бо будується з відповіді резолву, якої після
	// повернення в кадр уже немає.
	dlTitle string
}

// clone — копія, яку не зачепить наступний пошук: слайси в моделі
// дозаписуються на місці, тому кадр стека мусить володіти своїми.
func (v view) clone() view {
	v.episodes = slices.Clone(v.episodes)
	v.cards = slices.Clone(v.cards)
	return v
}

type Model struct {
	bootstrapRefs    []provider.TitleRef
	bootstrapPending bool
	deferredEpisodes *episodeRequest
	deferredBookmark *bookmarkRequest
	badgeScheduled   *atomic.Bool
	newsDisabled     map[string]bool
	baselineWarning  bool

	// Фонове й ручне оновлення. refreshEvery — крок циклу (0 вимикає; шов для
	// тестів). refreshGen — покоління актуальності: beginNav збільшує його, і
	// відповідь старого покоління не торкається екрана. refreshBusy — покоління
	// операції, що ще виконується (0 — жодної): знімає його лише її власна
	// відповідь, бо після переходу команда все ще пише кеш. freshBefore —
	// сума бейджів до ручного оновлення, щоб сказати «+2 нові серії» тим самим
	// числом, що й рядки домівки.
	refreshEvery time.Duration
	refreshGen   int
	refreshBusy  int
	freshBefore  int

	initialRemote    bool
	overlay          overlayKind
	overlayFrame     *frame
	historyAll       []item
	historyShown     int
	historyFiltering bool
	filterGen        int
	restoreSelection *frame
	pendingUI        tea.Cmd
	undo             *playback.WatchedUndo
	statusKind       statusKind
	statusGen        int
	liveAt           time.Time
	liveFrozen       bool
	localTicking     bool
	chainActive      bool

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

	// Блоки каталогу живуть у моделі, а не в списку: список перебудовується на
	// кожному переході, а картки переживають його. epsScratch — навпаки,
	// одноразовий кеш читань з диска на одну перебудову екрана.
	catalog     map[provider.CatalogKind][]provider.TitleCard
	epsScratch  map[string][]provider.Episode
	homeSpacers bool

	// searches — нещодавні запити, як їх бачить екран пошуку. Не у view: історія
	// спільна для всіх кадрів стека, а джерело істини — файл на диску.
	searches []string

	// Стан активної сесії. titleID і pinned дає Begin на горутині Update:
	// фонова команда не має права читати бібліотеку, щоб дізнатися їх.
	playCancel      context.CancelFunc
	playTitleID     string
	playPinned      string
	playGen         int
	journalEvents   <-chan error
	journalWarning  bool
	quitting        bool
	pendingBaseline *bookmarkBaselineMsg

	// Вікно в сесію, що грає: останній знімок Live і покоління, яким
	// відсікаються відповіді попередньої сесії (Snapshot під VLC може висіти
	// секунди). liveTicking — чи вже крутиться періодичний цикл; liveRetries —
	// скільки разів ми перепитували сесію, яка ще не встигла з'явитися.
	live        playback.Snapshot
	liveGen     int
	liveTicking bool
	liveRetries int
	// now — шов для тестів оцінки часу завершення серії.
	now func() time.Time
	// randN — вибір рулетки; шов для тестів, бо інакше «випадково» перевірити
	// нічого не можна.
	randN func(n int) int
	// easter — хвіст набраного на домівці, nya — чи показувати кота. Обидва
	// поза view: пасхалка живе на домівці й не має переживати переходи в кадрі
	// стека.
	easter string
	nya    bool
	// Черга завантажень. dl == nil — завантаження недоступні (тести): D каже
	// про це й нічого не робить. downloads — останній знімок менеджера за
	// ключем «provider:slug:серія» (той самий, яким дедуплікує сам менеджер);
	// savedScratch — одноразовий кеш читань папки завантажень із життєвим
	// циклом epsScratch плюс скидання при зміні папки й на кожному Done.
	// quitArmed — перше інтерактивне «вийти» під час завантаження лише
	// попередило.
	dl           *download.Manager
	fetcher      *httpx.Fetcher
	downloads    map[string]download.Progress
	savedScratch map[string]map[int][]download.SavedFile
	quitArmed    bool

	// Налаштування та пульт: cfg — той самий покажчик, що в cmd; remote —
	// поточна адреса для екрана «Грає» та «Налаштування» ("" = вимкнено).
	cfg    *store.Config
	remote RemoteInfo
	opts   Options
}

// catalogKinds — порядок блоків каталогу на домівці, він же порядок запитів.
var catalogKinds = []provider.CatalogKind{provider.CatalogTopSeason, provider.CatalogFresh}

const (
	// homeCatalogRows — скільки карток блоку показуємо. Домівка — не каталог:
	// шість рядків читаються одним поглядом, двадцять — це вже окремий екран.
	homeCatalogRows = 6
	// homeContinueRows — скільки тайтлів пропонуємо продовжити. Три — це те,
	// між чим людина справді обирає; довший список уже дублює закладки.
	homeContinueRows = 3
	// maxBadgeProbes — стеля на кількість тайтлів, чий список серій оновлюємо у фоні.
	maxBadgeProbes = 20
	badgeWorkers   = 4
)

func New(eng *playback.Engine, opts Options) Model {
	initialRemote := eng.Live != nil
	if eng.Live == nil {
		eng.Live = &playback.Live{}
	}
	if opts.Cfg == nil {
		opts.Cfg = store.DefaultConfig()
	}
	if opts.DataDir == "" {
		opts.DataDir, _ = store.DataDir()
	}
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

	fetcher := opts.Fetcher
	if fetcher == nil && opts.Downloads != nil {
		fetcher = download.NewFetcher(nil)
	}

	m := Model{
		badgeScheduled: &atomic.Bool{},
		refreshEvery:   refreshInterval,
		initialRemote:  initialRemote,
		eng:            eng,
		list:           l,
		input:          in,
		ic:             ic,
		catalog:        map[provider.CatalogKind][]provider.TitleCard{},
		epsScratch:     map[string][]provider.Episode{},
		dl:             opts.Downloads,
		fetcher:        fetcher,
		downloads:      map[string]download.Progress{},
		savedScratch:   map[string]map[int][]download.SavedFile{},
		cfg:            opts.Cfg,
		remote:         opts.Remote,
		opts:           opts,
		now:            time.Now,
		randN:          rand.IntN,
	}
	m.prepareBootstrap()
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
func Run(ctx context.Context, eng *playback.Engine, opts Options) error {
	m := New(eng, opts)
	p := tea.NewProgram(m, tea.WithoutSignalHandler())
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
	if cmd := m.bootstrapCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	} else if cmd := m.libraryEpisodesCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.remoteRequestCmd(); m.initialRemote && cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.refreshTickCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if cmd := m.downloadEventsCmd(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
