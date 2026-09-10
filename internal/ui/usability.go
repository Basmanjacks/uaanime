package ui

import (
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"fmt"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"strings"
	"time"
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlayBudget
)

type statusKind int

const (
	statusInfo statusKind = iota
	statusSuccess
	statusLoading
	statusWarning
)

type statusExpiredMsg struct{ gen int }
type localTickMsg struct{ gen int }
type action struct{ key, label string }

func (m Model) actions() []action {
	if m.overlay == overlayBudget {
		return []action{{"Enter", i18n.TuiActionPick}, {"Esc", i18n.TuiActionBack}}
	}
	if m.overlay == overlayHelp {
		return []action{{"Esc", i18n.TuiHelpClose}, {"↑/↓", i18n.TuiActionMove}}
	}
	var a []action
	switch m.screen {
	case screenPlaying:
		a = []action{{"Space", i18n.TuiActionPause}, {"Esc", i18n.TuiActionStop}, {"?", i18n.TuiActionHelp}, {"←/→", i18n.TuiActionSeek}, {"Shift+←/→", i18n.TuiActionSeekBig}, {"N", i18n.TuiActionNext}, {"+/−", i18n.TuiActionVolume}, {".", i18n.TuiActionStopAfter}, {"B", i18n.TuiActionBudget}}
	case screenEpisodes:
		a = []action{{"Enter", i18n.TuiActionPlay}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}, {"/", i18n.TuiActionFilter}, {"M", i18n.TuiActionBookmark}, {"S", i18n.TuiActionStudio}, {"X", i18n.TuiActionWatched}, {"W", i18n.TuiActionBulk}}
	case screenSettings:
		a = []action{{"Enter", i18n.TuiActionPick}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}, {"←/→", i18n.TuiActionChange}}
	case screenSettingValue, screenStudio:
		a = []action{{"Enter", i18n.TuiActionPick}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}}
	case screenSearch:
		a = []action{{"Enter", i18n.TuiActionOpen}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}, {"/", i18n.TuiActionSearchInput}}
		if len(m.cards) == 0 {
			a = append(a, action{"X", i18n.TuiActionForgetQuery})
		} else {
			a = append(a, action{"M", i18n.TuiActionBookmark})
		}
	case screenHome:
		a = []action{{"Enter", i18n.TuiActionOpen}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}, {"/", i18n.TuiActionSearch}, {"M", i18n.TuiActionBookmark}, {",", i18n.TuiActionSettings}, {"Q", i18n.TuiActionQuit}}
	default:
		a = []action{{"Enter", i18n.TuiActionOpen}, {"Esc", i18n.TuiActionBack}, {"?", i18n.TuiActionHelp}}
		if m.list.FilteringEnabled() {
			a = append(a, action{"/", i18n.TuiActionFilter})
		}
		if m.screen == screenBookmarks || m.screen == screenSearch {
			a = append(a, action{"M", i18n.TuiActionBookmark})
		}
	}
	if m.eng.WatchedUndoAvailable(m.undo) {
		a = append(a, action{"U", i18n.TuiActionUndo})
	}
	return a
}
func (m Model) actionHint() string {
	var parts []string
	for _, a := range m.actions() {
		s := a.key + " " + a.label
		next := strings.Join(append(parts, s), " · ")
		if m.w > 0 && lipgloss.Width(next) > m.w-2 {
			break
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}
func (m *Model) openOverlay(kind overlayKind) {
	f := m.snapshot()
	m.overlayFrame = &f
	actions := m.actions()
	m.overlay = kind
	m.filterGen++
	m.list.ResetFilter()
	m.list.SetFilteringEnabled(false)
	m.setDelegate(false)
	var rows []item
	cursor := 0
	if kind == overlayHelp {
		for _, a := range actions {
			rows = append(rows, item{title: a.key + "  " + a.label})
		}
	} else {
		rows = append(rows, item{title: i18n.TuiBudgetOff, payload: payloadBudget{}})
		for n := 1; n <= playback.MaxSessionLimit; n++ {
			rows = append(rows, item{title: fmt.Sprint(n), payload: payloadBudget{n}})
		}
		limit := m.eng.Live.Limit()
		if limit.Enabled {
			cursor = limit.Remaining
		}
	}
	_ = m.setItems(rows, cursor)
	m.relayout()
}
func (m *Model) closeOverlay() tea.Cmd {
	if m.overlay == overlayNone {
		return nil
	}
	f := m.overlayFrame
	m.overlay = overlayNone
	m.overlayFrame = nil
	m.filterGen++
	// Only list presentation is restored. Playback and navigation may have advanced.
	if f == nil {
		return nil
	}
	m.setDelegate(m.screen == screenSearch && len(m.cards) > 0)
	m.list.SetFilteringEnabled(m.screen != screenHome && m.screen != screenSettings && m.screen != screenSettingValue)
	m.relayout()
	status, kind, gen, errText := m.status, m.statusKind, m.statusGen, m.errText
	cmd := m.restoreRows(*f)
	m.status, m.statusKind, m.statusGen, m.errText = status, kind, gen, errText
	return cmd
}
func (m Model) overlayKey(msg tea.KeyPressMsg, key string) (tea.Model, tea.Cmd) {
	if key == "esc" || key == "?" && m.overlay == overlayHelp {
		return m, m.closeOverlay()
	}
	if m.overlay == overlayBudget {
		if key == "enter" {
			if it, ok := m.list.SelectedItem().(item); ok {
				if p, ok := it.payload.(payloadBudget); ok {
					if err := m.eng.Live.SetSessionLimit(p.n); err != nil {
						m.errText = m.errorText(err)
						return m, nil
					}
					cmd := m.closeOverlay()
					return m, tea.Batch(cmd, m.liveSnapshotCmd(m.liveGen))
				}
			}
		}
	} else if m.screen == screenPlaying {
		switch key {
		case "space", "n", "N", "+", "=", "-", ".":
			return m.playingKey(key)
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}
func (m *Model) selectedKey() string {
	if it, ok := m.list.SelectedItem().(item); ok {
		return it.key()
	}
	return ""
}
func (m *Model) selectKey(key string, fallback int) {
	rows := m.list.VisibleItems()
	for i, row := range rows {
		if it, ok := row.(item); ok && key != "" && it.key() == key && !it.header {
			m.list.Select(i)
			return
		}
	}
	if len(rows) > 0 {
		m.list.Select(max(0, min(fallback, len(rows)-1)))
		m.skipHeaders(1)
	}
}
func (m *Model) showBookmarks() {
	m.setScreen(screenBookmarks)
	m.epsScratch = map[string][]provider.Episode{}
	_ = m.setItems(m.bookmarkRows(), 0)
	m.status = ""
}
func (m *Model) success(text string) tea.Cmd {
	m.status = text
	m.statusKind = statusSuccess
	m.statusGen++
	return nil
}
func (m *Model) beginChain(ref provider.TitleRef) { m.eng.Live.BeginChain(ref); m.chainActive = true }
func (m *Model) endChain()                        { m.eng.Live.EndChain(); m.chainActive = false }
func (m *Model) freezeLive() {
	m.live.PositionSec = m.displayPosition()
	m.liveAt = m.now()
	m.liveFrozen = true
}
func (m Model) displayPosition() float64 {
	pos := m.live.PositionSec
	if m.live.Playing && !m.live.Paused && !m.liveFrozen && !m.liveAt.IsZero() {
		pos += max(0, min(m.now().Sub(m.liveAt).Seconds(), 5))
	}
	if m.live.DurationSec > 0 {
		pos = min(pos, m.live.DurationSec)
	}
	return pos
}
func (m Model) localTickCmd() tea.Cmd {
	gen := m.liveGen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return localTickMsg{gen} })
}
func (m Model) watchedBefore() (tea.Model, tea.Cmd) {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return m, nil
	}
	p, ok := it.payload.(payloadEp)
	if !ok {
		return m, nil
	}
	eps, ok := m.currentEpisodes()
	if !ok {
		return m, nil
	}
	n, token, err := m.eng.SetWatchedBefore(m.ref, p.num, eps)
	if err != nil {
		m.errText = m.errorText(err)
		return m, nil
	}
	m.errText = ""
	if n == 0 {
		return m, m.success(i18n.TuiBulkEmpty)
	}
	m.undo = token
	m.publishPlaylist()
	return m, tea.Batch(m.setItems(m.episodeRows(), -1), m.success(fmt.Sprintf(i18n.TuiBulkDone, n)))
}
func (m Model) undoWatched() (tea.Model, tea.Cmd) {
	if !m.eng.WatchedUndoAvailable(m.undo) {
		return m, nil
	}
	if err := m.eng.UndoWatched(m.undo); err != nil {
		m.errText = m.errorText(err)
		return m, nil
	}
	m.undo = nil
	m.errText = ""
	f := m.snapshot()
	cmd := m.restoreRows(f)
	m.publishPlaylist()
	return m, tea.Batch(cmd, m.success(i18n.TuiBulkUndo))
}

type filterResultMsg struct {
	gen     int
	matches list.FilterMatchesMsg
}

func guardFilter(cmd tea.Cmd, gen int) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		switch v := msg.(type) {
		case list.FilterMatchesMsg:
			return filterResultMsg{gen, v}
		case tea.BatchMsg:
			for i, c := range v {
				v[i] = guardFilter(c, gen)
			}
			return v
		}
		return msg
	}
}

func (m Model) budgetNoteLines() []string {
	width := m.w - 2
	if width <= 0 {
		width = contentCap
	}
	var lines []string
	line := ""
	for _, word := range strings.Fields(i18n.TuiBudgetNote) {
		next := word
		if line != "" {
			next = line + " " + word
		}
		if line != "" && lipgloss.Width(next) > width {
			lines = append(lines, line)
			line = word
		} else {
			line = next
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
