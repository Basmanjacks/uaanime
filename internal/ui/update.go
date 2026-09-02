package ui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// ---- update ----

// rejectStale — відповідь на запит, який уже нікому не потрібен: людина встигла
// піти далі, і застосувати таку відповідь означало б смикнути екран під нею.
func (m *Model) rejectStale(req int) bool { return req != m.reqID }

// failNav — навігація не відбулась: відкладений кадр викидаємо, щоб Esc не
// повертав у нікуди, а причину показуємо людською мовою.
func (m *Model) failNav(err error) {
	m.pending = nil
	m.pendingReq = 0
	m.errText = i18n.ErrorText(err)
}

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
		if m.rejectStale(msg.req) {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.failNav(msg.err)
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
		if m.rejectStale(msg.req) {
			return m, nil
		}
		if msg.err != nil {
			m.status = ""
			m.failNav(msg.err)
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
					m.errText = provider.CleanText(err.Error())
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
		if m.rejectStale(msg.req) {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.failNav(msg.err)
			var cmd tea.Cmd
			if m.screen == screenPlaying {
				if len(m.episodes) > 0 {
					cmd = m.showEpisodes()
				} else {
					m.showHome()
				}
			}
			// showHome чистить errText, тому текст ставимо після переходу.
			m.errText = i18n.ErrorText(msg.err)
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

	case studiosMsg:
		if m.rejectStale(msg.req) {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.failNav(msg.err)
			return m, nil
		}
		m.commitPending(msg.req)
		m.stack = append(m.stack, m.snapshot())
		m.showStudioChoice(msg.choices)
		return m, nil

	case playDoneMsg:
		if m.pendingBaseline != nil {
			m.applyBookmarkBaseline(*m.pendingBaseline)
			m.pendingBaseline = nil
		}
		m.playCancel = nil
		// Finish синхронний: журнал зливається в бібліотеку тут, на горутині
		// Update, а не у фоновій команді.
		result, err := m.eng.Finish(msg.reason, m.playTitleID, m.pendingEp)
		result.PinnedStudio = m.playPinned
		m.playTitleID, m.playPinned = "", ""
		if msg.err != nil {
			err = msg.err
		}
		// намір пульта сильніший за налаштування: «наступна» йде далі навіть
		// без автоплею, «стоп» уриває ланцюжок навіть з ним
		chain := false
		switch {
		case m.quitting || err != nil || result.Intent == playback.IntentStop:
		case result.Intent == playback.IntentNext:
			chain = true
		case result.Reason == player.EndEOF && m.eng.Autoplay:
			chain = true
		}
		if chain {
			if next, ok := playback.NextEpisodeNumber(m.episodes, m.pendingEp); ok {
				req := m.nextReq()
				m.pendingEp = next
				m.status = i18n.TuiResolving
				return m, m.resolveCmd(m.ref, next, req, m.eng.ResolveHints(m.ref, next))
			}
		}
		switch {
		case err != nil:
			m.errText = fmt.Sprintf(i18n.MsgPlayerFailed, err)
		case result.Completed:
			m.status = fmt.Sprintf(i18n.MsgEpisodeDone, m.pendingEp)
		case result.PositionSec > 0:
			m.status = fmt.Sprintf(i18n.MsgProgressSaved,
				int(result.PositionSec)/60, int(result.PositionSec)%60)
		}
		// повертаємось на екран серій з оновленими станами
		var cmd tea.Cmd
		if len(m.episodes) > 0 {
			cmd = m.showEpisodes()
		} else {
			m.showHome()
		}
		// Друга фаза виходу: журнал уже злитий, тепер можна завершувати.
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd

	case signalMsg:
		return m.requestQuit()
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
	// Begin пише бібліотеку, тому виконується тут, до запуску команди.
	// Помилка (немає плеєра, не записалась бібліотека) лишає екран як був.
	titleID, pinned, err := m.eng.Begin(res)
	if err != nil {
		m.errText = i18n.ErrorText(err)
		return m, nil
	}
	if m.screen == screenStudio && len(m.stack) > 0 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.pendingEp = res.Episode
	m.playTitleID, m.playPinned = titleID, pinned
	m.setScreen(screenPlaying)
	if res.PinFallback {
		m.status = fmt.Sprintf(i18n.TuiStudioFallback, m.studioPin(), res.Source.Studio)
	} else {
		m.status = i18n.TuiPlaying
	}
	cmd, cancel := m.playCmd(res, titleID)
	m.playCancel = cancel
	return m, cmd
}

// requestQuit — двофазний вихід. Під час відтворення Ctrl+C і сигнал лише
// скасовують сесію: сам вихід робить обробник playDoneMsg, коли Finish уже
// злив журнал. Інакше вихід гонився б із завершенням плеєра.
func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	if m.playCancel != nil {
		m.quitting = true
		m.playCancel()
		return m, nil
	}
	return m, tea.Quit
}
