package ui

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// nyaSequences — на що реагує пасхалка; обидві розкладки, бо «ня» набирають
// тією, яка зараз увімкнена.
var nyaSequences = []string{"nya", "ня"}

const (
	// easterMaxRunes — скільки останніх рун тримаємо. Буфер існує лише щоб
	// впізнати суфікс, тому росте не далі за найдовшу послідовність із запасом.
	easterMaxRunes = 8
	nyaDuration    = 2 * time.Second
)

func (m Model) updateKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Пасхалка нічого не перехоплює: клавіша лише запам'ятовується й далі йде
	// звичайним шляхом, інакше «n» на домівці перестало б гортати список.
	easter := m.noteEaster(key)
	if easter != nil {
		model, cmd := m.handleKey(msg, key)
		return model, tea.Batch(cmd, easter)
	}
	return m.handleKey(msg, key)
}

// noteEaster дописує одно-рунну клавішу з домівки в буфер і повертає команду,
// яка погасить кота, — або nil, якщо гасити нема чого. Перевіряємо суфікс, а
// не рівність: послідовність набирають посеред іншого тицяння по списку.
func (m *Model) noteEaster(key string) tea.Cmd {
	if m.screen != screenHome || utf8.RuneCountInString(key) != 1 {
		return nil
	}
	m.easter += strings.ToLower(key)
	if runes := []rune(m.easter); len(runes) > easterMaxRunes {
		m.easter = string(runes[len(runes)-easterMaxRunes:])
	}
	for _, seq := range nyaSequences {
		if strings.HasSuffix(m.easter, seq) {
			m.nya = true
			return tea.Tick(nyaDuration, func(time.Time) tea.Msg { return nyaOffMsg{} })
		}
	}
	return nil
}

func (m Model) handleKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return m.requestQuit()
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
	if (key == "s" || key == "S") && !m.list.SettingFilter() && (m.screen != screenSearch || !m.input.Focused()) && m.screen == screenEpisodes {
		it, ok := m.list.SelectedItem().(item)
		if !ok {
			return m, nil
		}
		p, ok := it.payload.(payloadEp)
		if !ok {
			return m, nil
		}
		m.pendingEp = p.num
		req := m.nextReq()
		m.status = i18n.TuiResolving
		return m, m.studiosCmd(m.ref, p.num, req)
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
		case ",":
			m, _ = m.openSettings()
			return m, nil
		}

	case screenSettings:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		case "left", "h":
			m.cycleSetting(-1)
			return m, nil
		case "right", "l":
			m.cycleSetting(1)
			return m, nil
		}

	case screenSettingValue:
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			if it, ok := m.list.SelectedItem().(item); ok {
				if p, ok := it.payload.(payloadSettingValue); ok {
					return m.pickSettingValue(p), nil
				}
			}
			return m, nil
		}

	case screenSearch:
		// Поле вводу і список ділять екран, тому фокус передається явно: поки
		// поле активне, решта клавіш — це текст запиту, а не команди списку.
		if m.input.Focused() {
			switch key {
			case "esc":
				m.back()
				return m, nil
			case "enter":
				return m.runSearch(m.input.Value())
			case "down", "tab":
				if m.selectFirstRow() {
					m.input.Blur()
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		switch key {
		case "esc":
			m.back()
			return m, nil
		case "enter":
			return m.openSelected()
		case "up":
			// Вихід угору з першого рядка — назад у поле, зі збереженим запитом:
			// інакше з рядка історії немає куди повернутись, окрім «/».
			if i := m.firstRowIndex(); i < 0 || m.list.Index() <= i {
				return m, m.input.Focus()
			}
		case "/":
			m.input.SetValue("")
			return m, m.input.Focus()
		case "x", "X":
			return m.forgetSelectedQuery()
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
		case "x", "X":
			// Історія лишає «x» списку: позначати там нічого.
			if m.screen == screenEpisodes {
				return m.toggleWatched()
			}
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
		return m.playingKey(key)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	// Курсор ніколи не зупиняється на заголовку секції: він не робить нічого,
	// тож зупинка на ньому виглядає як зависання списку.
	m.skipHeaders(navDirection(key))
	return m, cmd
}

// Кроки керування плеєром. Десять секунд — «прослухав репліку», тридцять —
// «пропустив опенінг»; гучність рухається п'ятірками, бо менший крок на слух
// не відрізняється.
const (
	seekStep    = 10.0
	seekStepBig = 30.0
	volumeStep  = 5.0
)

// playingKey — керування плеєром з екрана «Грає». Усі команди йдуть через
// Live: воно async-safe і бібліотеки не торкається (правило 10). Esc, як і
// раніше, лише скасовує сесію — вихід робить playDoneMsg після Finish.
func (m Model) playingKey(key string) (tea.Model, tea.Cmd) {
	if key == "esc" {
		if m.playCancel != nil {
			m.playCancel()
		}
		return m, nil
	}
	live := m.eng.Live
	if key == "." {
		// Прапорець живе в Live — спільне джерело істини для TUI і пульта.
		// Конфіг не чіпаємо: це бажання на цю серію, а не налаштування.
		on := !live.StopAfter()
		live.SetStopAfter(on)
		m.errText = ""
		m.status = i18n.TuiStopAfterOff
		if on {
			m.status = i18n.TuiStopAfterOn
		}
		return m, m.liveSnapshotCmd(m.liveGen)
	}
	var err error
	switch key {
	case "space":
		err = live.TogglePause()
	case "left":
		err = live.Seek(-seekStep)
	case "right":
		err = live.Seek(seekStep)
	case "shift+left":
		err = live.Seek(-seekStepBig)
	case "shift+right":
		err = live.Seek(seekStepBig)
	case "n", "N":
		err = live.Next()
	case "+", "=":
		err = live.AddVolume(volumeStep)
	case "-":
		err = live.AddVolume(-volumeStep)
	default:
		return m, nil
	}
	if err != nil {
		if errors.Is(err, playback.ErrNotPlaying) {
			m.errText = i18n.TuiNotPlaying
		} else {
			m.errText = i18n.ErrorText(err)
		}
		return m, nil
	}
	m.errText = ""
	// Знімок без затримки: рядок стану має відповісти на клавішу одразу, а не
	// на наступному тіку.
	return m, m.liveSnapshotCmd(m.liveGen)
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
		m.errText = provider.CleanText(err.Error())
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

// toggleWatched — клавіша «x» на екрані серій. Перемикач простий: завершена
// серія скидається, будь-яка інша (включно з недодивленою) стає переглянутою.
func (m Model) toggleWatched() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok || it.header {
		return m, nil
	}
	p, ok := it.payload.(payloadEp)
	if !ok {
		return m, nil
	}

	watched := true
	if title := m.eng.Lib.TitleByRef(m.ref); title != nil {
		if pr := m.eng.Lib.ProgressFor(title.ID, p.num); pr != nil && pr.Completed {
			watched = false
		}
	}

	m.errText = ""
	if err := m.eng.SetWatched(m.ref, p.num, watched); err != nil {
		m.errText = provider.CleanText(err.Error())
		return m, nil
	}
	if watched {
		m.status = fmt.Sprintf(i18n.TuiEpMarked, p.num)
	} else {
		m.status = fmt.Sprintf(i18n.TuiEpUnmarked, p.num)
	}
	// Пульт показує ті самі позначки, що й список: нова публікація — щоб
	// телефон не лишився з попереднім станом серії.
	m.publishPlaylist()
	// Index(), не GlobalIndex(): рядки перебудовуються ті самі й у тому ж
	// порядку, тож видима позиція під фільтром не змінюється.
	return m, m.setItems(m.episodeRows(), m.list.Index())
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

// runSearch — єдиний шлях запуску пошуку: Enter у полі й Enter на рядку історії
// мусять робити те саме, включно зі станом пагінації.
func (m Model) runSearch(q string) (tea.Model, tea.Cmd) {
	if q == "" {
		return m, nil
	}
	m.input.SetValue(q)
	m.input.Blur()
	m.status = i18n.TuiSearching
	req := m.beginNav()
	m.query = q
	m.page, m.hasMore = 0, false
	return m, m.searchCmd(q, 1, req)
}

// forgetSelectedQuery — «x» на рядку історії. На картці результату не робить
// нічого: там ця мнемоніка нічому не відповідає.
func (m Model) forgetSelectedQuery() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, nil
	}
	p, ok := it.payload.(payloadQuery)
	if !ok {
		return m, nil
	}
	rest, err := m.eng.Store.RemoveSearch(p.q)
	if err != nil {
		m.errText = provider.CleanText(err.Error())
		return m, nil
	}
	m.searches = rest
	rows := m.recentRows()
	cmd := m.setItems(rows, m.list.Index())
	if len(rows) == 0 {
		// Списку більше немає — фокус мусить повернутись у поле, інакше клавіші
		// нікуди не діваються.
		return m, tea.Batch(cmd, m.input.Focus())
	}
	m.skipHeaders(1)
	return m, cmd
}

// openSearch — вхід на екран пошуку; спільний для «Пошуку нового» і клавіші «/».
func (m Model) openSearch() (tea.Model, tea.Cmd) {
	m.stack = append(m.stack, m.snapshot())
	m.setScreen(screenSearch)
	m.loadSearches()
	rows := m.recentRows()
	_ = m.setItems(rows, firstRow(rows))
	m.errText = ""
	m.status = ""
	m.query = ""
	m.cards, m.page, m.hasMore = nil, 0, false
	m.input.SetValue("")
	return m, m.input.Focus()
}

// openTitle — перехід на екран серій тайтлу. Спільний для картки й рулетки:
// звідки взявся ref, далі вже не має значення.
func (m Model) openTitle(ref provider.TitleRef) (tea.Model, tea.Cmd) {
	snap := m.snapshot()
	req := m.beginNav()
	m.pending = &snap
	m.pendingReq = req
	m.ref = ref
	m.status = i18n.TuiSearching
	return m, m.episodesCmd(ref, req, true)
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
	case payloadQuery:
		return m.runSearch(p.q)
	case payloadHistory:
		m.stack = append(m.stack, m.snapshot())
		m.showHistory()
		return m, nil
	case payloadSettings:
		m, _ = m.openSettings()
		return m, nil
	case payloadSetting:
		if len(m.settingValues(p.id)) < 2 {
			return m, nil
		}
		m.stack = append(m.stack, m.snapshot())
		m.showSettingValue(p.id)
		m.errText, m.status = "", ""
		return m, nil
	case payloadResume:
		snap := m.snapshot()
		req := m.beginNav()
		m.pending = &snap
		m.pendingReq = req
		m.ref = p.ref
		m.pendingEp = p.ep
		m.status = i18n.TuiResolving
		// серії підтягнемо у фоні, щоб після перегляду показати список
		return m, tea.Batch(
			m.resolveCmd(p.ref, p.ep, req, m.eng.ResolveHints(p.ref, p.ep)),
			m.episodesCmd(p.ref, req, false))
	case payloadTitle:
		return m.openTitle(p.ref)
	case payloadRoulette:
		refs := m.rouletteCandidates()
		if len(refs) == 0 {
			m.status = i18n.TuiRouletteEmpty
			return m, nil
		}
		return m.openTitle(refs[m.randN(len(refs))])
	case payloadMore:
		// Довантаження — теж навігаційна дія: свій req, старі відповіді летять у смітник.
		req := m.beginNav()
		m.status = i18n.TuiSearching
		return m, m.searchCmd(m.query, m.page+1, req)
	case payloadEp:
		req := m.beginNav()
		m.pendingEp = p.num
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, p.num, req, m.eng.ResolveHints(m.ref, p.num))
	case payloadStudio:
		if err := m.eng.PinStudio(m.ref, p.src.Studio); err != nil {
			m.errText = provider.CleanText(err.Error())
			return m, nil
		}
		req := m.beginNav()
		m.status = i18n.TuiResolving
		// підказки знімаються ПІСЛЯ PinStudio: новий пін має потрапити у вибір
		return m, m.resolveCmd(m.ref, m.pendingEp, req, m.eng.ResolveHints(m.ref, m.pendingEp))
	}
	return m, nil
}
