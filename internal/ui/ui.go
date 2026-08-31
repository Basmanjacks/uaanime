// Package ui — TUI на bubbletea v2. Один компонент списку, перевикористаний
// усіма екранами; кожен екран — «заголовок + список + один рядок підказки».
package ui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
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
	screenUpdates
)

// item — універсальний елемент для всіх екранів; payload каже, що робить Enter.
type item struct {
	title, desc string
	payload     any
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

type (
	payloadResume struct {
		ref provider.TitleRef
		ep  int
	}
	payloadTitle   struct{ ref provider.TitleRef }
	payloadSearch  struct{}
	payloadHistory struct{}
	payloadUpdates struct{}
	payloadEp      struct{ num int }
	payloadStudio  struct{ src provider.Source }
)

// Повідомлення асинхронних команд.
type (
	searchDoneMsg struct {
		refs []provider.TitleRef
		err  error
	}
	episodesDoneMsg struct {
		ref provider.TitleRef
		eps []provider.Episode
		err error
	}
	resolvedMsg struct {
		res *playback.Resolved
		err error
	}
	playDoneMsg struct {
		result *playback.Result
		err    error
	}
)

type Model struct {
	eng   *playback.Engine
	list  list.Model
	input textinput.Model
	ic    icons

	screen        screen
	width, height int
	status        string
	errText       string

	// контекст поточного тайтлу
	ref       provider.TitleRef
	episodes  []provider.Episode
	pendingEp int

	playCancel context.CancelFunc
}

func New(eng *playback.Engine) Model {
	d := list.NewDefaultDelegate()
	l := list.New(nil, d, 0, 0)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	in := textinput.New()
	in.Prompt = "🔎 "
	in.Placeholder = i18n.TuiSearchPrompt

	m := Model{eng: eng, list: l, input: in, ic: themeIcons(os.Getenv("UAANIME_ASCII") == "1")}
	m.showHome()
	return m
}

// Run запускає TUI. Паніка не долітає до користувача: bubbletea відновлює
// термінал, а корінь main має власний recover.
func Run(eng *playback.Engine) error {
	_, err := tea.NewProgram(New(eng)).Run()
	return err
}

func (m Model) Init() tea.Cmd { return nil }

// ---- побудова екранів ----

func (m *Model) setItems(items []item) {
	li := make([]list.Item, len(items))
	for i, it := range items {
		li[i] = it
	}
	m.list.SetItems(li)
	m.list.ResetSelected()
}

func (m *Model) showHome() {
	m.screen = screenHome
	m.errText = ""
	var items []item

	// «Продовжити» — тайтл з найсвіжішим прогресом
	if t, ep, pos := m.latestWatched(); t != nil {
		label := fmt.Sprintf(i18n.TuiContinuePfx, titleName(t), ep)
		desc := ""
		if pos > 0 {
			desc = fmt.Sprintf(i18n.TuiEpAt, int(pos)/60, int(pos)%60)
		}
		items = append(items, item{
			title:   m.ic.Play + " " + label,
			desc:    desc,
			payload: payloadResume{ref: t.Sources[0], ep: ep},
		})
	}
	// решта бібліотеки
	for _, e := range m.eng.Lib.Entries {
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		items = append(items, item{
			title:   titleName(t),
			desc:    stateLabel(e.State),
			payload: payloadTitle{ref: t.Sources[0]},
		})
	}
	items = append(items, item{title: m.ic.Search + " " + i18n.TuiSearchItem, payload: payloadSearch{}})
	if len(m.eng.Lib.Progress) > 0 {
		items = append(items, item{title: i18n.TuiHistoryItem, payload: payloadHistory{}})
	}
	if m.hasWatching() {
		items = append(items, item{title: i18n.TuiUpdatesItem, payload: payloadUpdates{}})
	}
	m.setItems(items)
	if len(items) == 1 {
		m.status = i18n.TuiEmptyLibrary
	} else {
		m.status = ""
	}
}

func (m *Model) latestWatched() (*library.LocalTitle, int, float64) {
	var best *library.Progress
	for _, p := range m.eng.Lib.Progress {
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

func (m *Model) hasWatching() bool {
	for _, e := range m.eng.Lib.Entries {
		if e.State == library.StateWatching {
			return true
		}
	}
	return false
}

// showHistory — переглянуті серії, найсвіжіші згори.
func (m *Model) showHistory() {
	m.screen = screenHistory
	sorted := make([]*library.Progress, len(m.eng.Lib.Progress))
	copy(sorted, m.eng.Lib.Progress)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WatchedAt.After(sorted[j].WatchedAt) })
	var items []item
	for _, p := range sorted {
		t := m.titleByID(p.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		desc := p.WatchedAt.Format("02.01 15:04")
		if p.Completed {
			desc += " · " + i18n.TuiEpDone
		} else {
			desc += " · " + fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
		}
		items = append(items, item{
			title:   fmt.Sprintf("%s · %s", titleName(t), fmt.Sprintf(i18n.TuiEpisode, p.Episode)),
			desc:    desc,
			payload: payloadResume{ref: t.Sources[0], ep: p.Episode},
		})
	}
	m.setItems(items)
}

// updatesDoneMsg — результат перевірки нових серій.
type updatesDoneMsg struct {
	items []item
}

// updatesCmd порівнює кількість серій на провайдері з останньою переглянутою
// для кожного тайтлу зі стану «переглядаєш».
func (m *Model) updatesCmd() tea.Cmd {
	eng := m.eng
	type probe struct {
		t    *library.LocalTitle
		last int
	}
	var probes []probe
	for _, e := range m.eng.Lib.Entries {
		if e.State != library.StateWatching {
			continue
		}
		if t := m.titleByID(e.TitleID); t != nil && len(t.Sources) > 0 {
			probes = append(probes, probe{t: t, last: e.LastEpisode})
		}
	}
	return func() tea.Msg {
		var items []item
		for _, p := range probes {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			eps, _, err := eng.EpisodesCached(ctx, p.t.Sources[0])
			cancel()
			if err != nil {
				continue // недоступний тайтл не ховає оновлення решти
			}
			maxEp := 0
			for _, ep := range eps {
				if ep.Number > maxEp {
					maxEp = ep.Number
				}
			}
			if maxEp > p.last {
				items = append(items, item{
					title:   fmt.Sprintf(i18n.TuiNewEpisodes, titleName(p.t), maxEp-p.last),
					desc:    fmt.Sprintf(i18n.TuiEpisode, p.last+1),
					payload: payloadResume{ref: p.t.Sources[0], ep: p.last + 1},
				})
			}
		}
		return updatesDoneMsg{items: items}
	}
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

func (m *Model) showEpisodes() {
	m.screen = screenEpisodes
	title := m.eng.Lib.TitleByRef(m.ref)
	var items []item
	for _, ep := range m.episodes {
		icon, desc := "  ", releasesSummary(ep.Releases)
		if title != nil {
			if p := m.eng.Lib.ProgressFor(title.ID, ep.Number); p != nil {
				if p.Completed {
					icon = styleDone.Render(m.ic.Done) + " "
					desc = i18n.TuiEpDone
				} else if p.PositionSec > 0 {
					icon = m.ic.Play + " "
					desc = fmt.Sprintf(i18n.TuiEpAt, int(p.PositionSec)/60, int(p.PositionSec)%60)
				}
			}
		}
		items = append(items, item{
			title:   icon + fmt.Sprintf(i18n.TuiEpisode, ep.Number),
			desc:    desc,
			payload: payloadEp{num: ep.Number},
		})
	}
	m.setItems(items)
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
	m.screen = screenStudio
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
			desc:    string(s.Kind),
			payload: payloadStudio{src: s},
		})
	}
	m.setItems(items)
}

// ---- асинхронні команди ----

func (m *Model) searchCmd(q string) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		refs, err := eng.Provider.Search(ctx, q)
		return searchDoneMsg{refs: refs, err: err}
	}
}

func (m *Model) episodesCmd(ref provider.TitleRef) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		eps, _, err := eng.EpisodesCached(ctx, ref)
		return episodesDoneMsg{ref: ref, eps: eps, err: err}
	}
}

func (m *Model) resolveCmd(ref provider.TitleRef, ep int) tea.Cmd {
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		res, err := eng.Resolve(ctx, ref, ep, nil)
		return resolvedMsg{res: res, err: err}
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
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width-2, msg.Height-5)
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKeys(msg)

	case searchDoneMsg:
		m.status = ""
		if msg.err != nil {
			m.errText = fmt.Sprintf(i18n.MsgProviderFailed, msg.err)
			return m, nil
		}
		if len(msg.refs) == 0 {
			m.status = i18n.TuiNothingFound
			m.setItems(nil)
			return m, nil
		}
		var items []item
		for _, r := range msg.refs {
			items = append(items, item{title: r.Name, payload: payloadTitle{ref: r}})
		}
		m.setItems(items)
		return m, nil

	case updatesDoneMsg:
		m.status = ""
		if len(msg.items) == 0 {
			m.status = i18n.TuiNoUpdates
		}
		m.setItems(msg.items)
		return m, nil

	case episodesDoneMsg:
		m.status = ""
		if msg.err != nil {
			m.errText = fmt.Sprintf(i18n.MsgProviderFailed, msg.err)
			return m, nil
		}
		m.ref = msg.ref
		m.episodes = msg.eps
		m.showEpisodes()
		return m, nil

	case resolvedMsg:
		m.status = ""
		if msg.err != nil {
			m.errText = fmt.Sprintf(i18n.MsgProviderFailed, msg.err)
			return m, nil
		}
		res := msg.res
		// одноразове питання: кілька студій і жодного піна
		title := m.eng.Lib.TitleByRef(m.ref)
		pinned := title != nil && m.eng.Lib.EntryFor(title.ID).StudioPin != ""
		if len(res.Candidates) > 1 && !pinned {
			m.showStudioChoice(res.Candidates)
			return m, nil
		}
		return m.startPlayback(res)

	case playDoneMsg:
		m.playCancel = nil
		if msg.err != nil {
			m.errText = fmt.Sprintf(i18n.MsgPlayerFailed, msg.err)
		} else if msg.result.Completed {
			m.status = fmt.Sprintf(i18n.MsgEpisodeDone, m.pendingEp)
		} else if msg.result.PositionSec > 0 {
			m.status = fmt.Sprintf(i18n.MsgProgressSaved,
				int(msg.result.PositionSec)/60, int(msg.result.PositionSec)%60)
		}
		// повертаємось на екран серій з оновленими станами
		if len(m.episodes) > 0 {
			m.showEpisodes()
		} else {
			m.showHome()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m Model) startPlayback(res *playback.Resolved) (tea.Model, tea.Cmd) {
	m.pendingEp = res.Episode
	m.screen = screenPlaying
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

	// під час фільтрації всі клавіші належать списку
	if m.screen != screenSearch && m.list.SettingFilter() {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	switch m.screen {
	case screenHome:
		switch key {
		case "q":
			return m, tea.Quit
		case "enter":
			return m.openSelected()
		}

	case screenSearch:
		switch key {
		case "esc":
			m.input.Blur()
			m.showHome()
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
				return m, m.searchCmd(q)
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

	case screenEpisodes, screenHistory, screenUpdates:
		switch key {
		case "esc":
			m.showHome()
			return m, nil
		case "enter":
			return m.openSelected()
		}

	case screenStudio:
		switch key {
		case "esc":
			m.showEpisodes()
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
	return m, cmd
}

func (m Model) openSelected() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, nil
	}
	m.errText = ""
	switch p := it.payload.(type) {
	case payloadSearch:
		m.screen = screenSearch
		m.setItems(nil)
		m.status = ""
		m.input.SetValue("")
		return m, m.input.Focus()
	case payloadHistory:
		m.showHistory()
		return m, nil
	case payloadUpdates:
		m.screen = screenUpdates
		m.setItems(nil)
		m.status = i18n.TuiCheckingUpdates
		return m, m.updatesCmd()
	case payloadResume:
		m.ref = p.ref
		m.pendingEp = p.ep
		m.status = i18n.TuiResolving
		// серії підтягнемо у фоні, щоб після перегляду показати список
		return m, tea.Batch(m.resolveCmd(p.ref, p.ep), m.episodesCmd(p.ref))
	case payloadTitle:
		m.ref = p.ref
		m.status = i18n.TuiSearching
		return m, m.episodesCmd(p.ref)
	case payloadEp:
		m.pendingEp = p.num
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, p.num)
	case payloadStudio:
		if err := m.eng.PinStudio(m.ref, p.src.Studio); err != nil {
			m.errText = err.Error()
			return m, nil
		}
		m.status = i18n.TuiResolving
		return m, m.resolveCmd(m.ref, m.pendingEp)
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
	case screenUpdates:
		title = i18n.TuiUpdatesItem
	default:
		title = i18n.TuiAppTitle
	}

	body := styleTitle.Render(title) + "\n"
	if m.screen == screenSearch {
		body += "  " + m.input.View() + "\n"
	}
	// порожній список не рендеримо: bubbles показує англійське «No items.»
	if m.screen != screenPlaying && len(m.list.Items()) > 0 {
		body += m.list.View()
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
	case screenHistory, screenUpdates:
		return i18n.TuiHintList
	default:
		return i18n.TuiHintHome
	}
}
