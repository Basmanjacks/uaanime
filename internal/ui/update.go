package ui

import (
	"charm.land/bubbles/v2/list"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
)

// ---- update ----

// rejectStale — відповідь на запит, який уже нікому не потрібен: людина встигла
// піти далі, і застосувати таку відповідь означало б смикнути екран під нею.
func (m *Model) rejectStale(req int) bool { return req != m.reqID }

// failNav — навігація не відбулась: відкладений кадр викидаємо, щоб Esc не
// повертав у нікуди, а причину показуємо людською мовою.
func (m *Model) failNav(err error) {
	m.pendingUI = m.closeOverlay()
	m.pending = nil
	m.pendingReq = 0
	m.errText = m.errorText(err)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.update(msg)
	out := updated.(Model)
	if !out.quitting && out.statusKind == statusSuccess && out.statusGen != m.statusGen {
		gen := out.statusGen
		cmd = tea.Batch(cmd, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusExpiredMsg{gen} }))
	}
	if out.pendingUI != nil {
		cmd = tea.Batch(cmd, out.pendingUI)
		out.pendingUI = nil
	}
	return out, guardFilter(cmd, out.filterGen)
}
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case bootstrapMsg:
		m.bootstrapPending = false
		m.seedReleases(msg.seeds)
		var cmds []tea.Cmd
		if r := m.deferredEpisodes; r != nil {
			m.deferredEpisodes = nil
			if r.req == m.reqID {
				cmds = append(cmds, m.episodesCmd(r.ref, r.req, r.navigate))
			}
		}
		if r := m.deferredBookmark; r != nil {
			m.deferredBookmark = nil
			cmds = append(cmds, m.bookmarkBaselineCmd(r.titleID, r.ref, r.provisional))
		}
		cmds = append(cmds, m.libraryEpisodesCmd())
		m.refreshLibraryLists()
		return m, tea.Batch(cmds...)
	case statusExpiredMsg:
		if msg.gen == m.statusGen && m.statusKind == statusSuccess {
			m.status = ""
			m.statusKind = statusInfo
			m.statusGen++
			m.statusKind = statusInfo
		}
		return m, nil
	case filterResultMsg:
		if msg.gen != m.filterGen {
			return m, nil
		}
		return m.update(msg.matches)
	case list.FilterMatchesMsg:
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		if f := m.restoreSelection; f != nil {
			m.selectKey(f.selectedKey, f.cursor)
			m.restoreSelection = nil
		}
		return m, cmd
	case localTickMsg:
		if msg.gen != m.liveGen || m.playCancel == nil {
			return m, nil
		}
		return m, m.localTickCmd()

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
		m.statusKind = statusInfo
		m.statusGen++
		if msg.err != nil {
			m.failNav(msg.err)
			return m, nil
		}
		_ = m.closeOverlay()
		return m, m.applySearchPage(msg)

	case catalogMsg:
		if msg.cards != nil {
			m.catalog[msg.kind] = msg.cards
		}
		m.refreshHome()
		return m, nil

	case libraryEpisodesMsg:
		m.seedReleases(msg.seeds)
		m.refreshLibraryLists()
		return m, nil

	case bookmarkBaselineMsg:
		if m.playCancel != nil {
			m.pendingBaseline = &msg
			return m, nil
		}
		status, kind, gen := m.status, m.statusKind, m.statusGen
		m.applyBookmarkBaseline(msg)
		if m.errText == "" {
			m.status, m.statusKind, m.statusGen = status, kind, gen
		}
		return m, nil

	case episodesDoneMsg:
		if m.rejectStale(msg.req) {
			return m, nil
		}
		if msg.err != nil {
			m.status = ""
			m.statusKind = statusInfo
			m.statusGen++
			m.failNav(msg.err)
			return m, nil
		}
		m.pendingUI = m.closeOverlay()
		m.ref = msg.ref
		m.episodes, m.episodesRef = msg.eps, msg.ref
		m.seedReleases([]playback.ReleaseSeed{{Ref: msg.ref, Episodes: msg.eps}})
		// Список приїхав — пульт має його побачити навіть тоді, коли на екран
		// серій ми не заходимо («Продовжити» йде з navigate:false).
		m.publishPlaylist()
		if !msg.navigate {
			if msg.offline {
				m.status = i18n.MsgOfflineCache
				m.statusKind = statusWarning
				m.statusGen++
			}
			if m.screen == screenEpisodes {
				return m, m.setItems(m.episodeRows(), -1)
			}
			return m, nil
		}
		if title := m.eng.Lib.TitleByRef(msg.ref); title != nil {
			if st := m.eng.Lib.StatusOf(title.ID, msg.eps); st.Kind == library.StatusPlanned {
				// Відкриття ще не початого тайтлу означає ознайомлення з наявними
				// серіями; у перегляді бейдж навмисно очищає лише сам перегляд.
				if err := errors.Join(m.eng.MarkSeen(msg.ref, maxEpisodeNumber(msg.eps)), m.eng.AcknowledgeReleases(msg.ref, msg.eps)); err != nil {
					m.errText = m.errorText(err)
				}
			}
		}
		if msg.offline {
			m.status = i18n.MsgOfflineCache
			m.statusKind = statusWarning
			m.statusGen++
		} else {
			m.status = ""
			m.statusKind = statusInfo
			m.statusGen++
		}
		m.commitPending(msg.req)
		return m, m.showEpisodes()

	case resolvedMsg:
		if m.rejectStale(msg.req) {
			return m, nil
		}
		m.status = ""
		m.statusKind = statusInfo
		m.statusGen++
		if msg.err != nil {
			m.endChain()
			m.failNav(msg.err)
			var cmd tea.Cmd
			if m.screen == screenPlaying {
				if _, ok := m.currentEpisodes(); ok {
					cmd = m.showEpisodes()
				} else {
					m.showHome()
				}
			}
			// showHome чистить errText, тому текст ставимо після переходу.
			m.errText = m.errorText(msg.err)
			return m, cmd
		}
		m.pendingUI = m.closeOverlay()
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
		m.statusKind = statusInfo
		m.statusGen++
		if msg.err != nil {
			m.failNav(msg.err)
			return m, nil
		}
		_ = m.closeOverlay()
		m.commitPending(msg.req)
		m.stack = append(m.stack, m.snapshot())
		m.showStudioChoice(msg.choices)
		return m, nil

	case journalMsg:
		if msg.gen != m.playGen || !msg.open {
			return m, nil
		}
		m.journalWarning = msg.err != nil
		return m, m.journalCmd()

	case playDoneMsg:
		_ = m.closeOverlay()
		if m.pendingBaseline != nil {
			m.applyBookmarkBaseline(*m.pendingBaseline)
			m.pendingBaseline = nil
		}
		m.playCancel = nil
		m.resetLive()
		// Finish синхронний: журнал зливається в бібліотеку тут, на горутині
		// Update, а не у фоновій команді.
		result, err := m.eng.FinishRun(msg.reason, m.playTitleID, m.pendingEp, msg.err)
		result.PinnedStudio = m.playPinned
		m.playTitleID, m.playPinned = "", ""
		// Журнал уже злитий у бібліотеку — публікуємо список із новою позначкою
		// «переглянуто» і без підсвіченої серії.
		m.publishPlaylist()
		err = errors.Join(msg.err, err)
		episodes, _ := m.currentEpisodes()
		if next, ok := playback.ContinueEpisode(result, err, m.quitting, m.ref, m.pendingEp, episodes, m.eng.Autoplay); ok {
			req := m.nextReq()
			m.pendingEp = next
			m.status = i18n.TuiResolving
			m.statusKind = statusLoading
			m.statusGen++
			m.statusKind = statusLoading
			return m, m.resolveCmd(m.ref, next, req, m.eng.ResolveHints(m.ref, next))
		}
		m.endChain()
		// Choose the return screen before assigning feedback: showHome clears
		// old messages, but must not erase this session's finalization failure.
		var cmd tea.Cmd
		if _, ok := m.currentEpisodes(); ok {
			cmd = m.showEpisodes()
		} else {
			m.showHome()
		}
		switch {
		case err != nil:
			m.errText = m.errorText(err)
		case result.Completed:
			m.status = fmt.Sprintf(i18n.MsgEpisodeDone, m.pendingEp)
			m.statusKind = statusSuccess
			m.statusGen++
		case result.PositionSec > 0:
			m.status = fmt.Sprintf(i18n.MsgProgressSaved,
				int(result.PositionSec)/60, int(result.PositionSec)%60)
			m.statusKind = statusSuccess
			m.statusGen++
		}
		// Друга фаза виходу: журнал уже злитий, тепер можна завершувати.
		if m.quitting {
			return m, tea.Quit
		}
		return m, cmd

	case remotePlayMsg:
		return m.updateRemotePlay(msg)

	case liveMsg:
		return m.updateLive(msg)

	case nyaOffMsg:
		m.nya = false
		return m, nil

	case signalMsg:
		return m.requestQuit()
	}

	var cmd tea.Cmd
	if m.screen == screenSearch && m.input.Focused() {
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	query := m.list.FilterValue()
	m.list, cmd = m.list.Update(msg)
	// Paste and other text input messages can change a query without a key.
	if m.list.FilterValue() != query {
		m.filterGen++
	}
	return m, cmd
}

// updateRemotePlay — тап по рядку списку серій на телефоні, що прийшов у
// простої. Скринька переозброюється завжди, а сам запит виконується, лише якщо
// він і досі про цей тайтл: між постановкою в канал і цим кадром TUI міг
// опублікувати інший список або взагалі піти з екрана серій. Застарілий запит
// відкидаємо мовчки — людина вже дивиться на інше, і рядок помилки лише
// смикнув би екран.
func (m Model) updateRemotePlay(msg remotePlayMsg) (tea.Model, tea.Cmd) {
	rearm := m.remoteRequestCmd()
	if m.eng.Live == nil || msg.req.Gen != m.eng.Live.CurrentGen() || !msg.req.Ref.Same(m.ref) {
		return m, rearm
	}
	// Під час гри адресний запит іде через закриття сесії й повертається в
	// playDoneMsg; сюди він потрапляє лише тоді, коли плеєра немає.
	if m.playCancel != nil {
		return m, rearm
	}
	m.pendingUI = m.closeOverlay()
	m.beginChain(m.ref)
	req := m.beginNav()
	m.pendingEp = msg.req.Episode
	m.errText = ""
	m.status = i18n.TuiResolving
	m.statusKind = statusLoading
	m.statusGen++
	return m, tea.Batch(rearm,
		m.resolveCmd(m.ref, msg.req.Episode, req, m.eng.ResolveHints(m.ref, msg.req.Episode)))
}

// updateLive — єдиний власник періодичного циклу знімків. Правила циклу:
// відповідь чужого покоління не застосовується взагалі (Snapshot під VLC може
// висіти секунди, і відповідь попередньої серії переписала б стан нової);
// цикл переозброюється лише з відповіді тіка, а знімки від клавіш нічого не
// озброюють — інакше кожне натискання додавало б паралельний цикл.
func (m Model) updateLive(msg liveMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.liveGen {
		return m, nil
	}
	if msg.err == nil {
		m.live = msg.snap
		m.liveAt = m.now()
		m.liveFrozen = false
	} else {
		m.freezeLive()
	}
	// Поза сесією нічого не переозброюємо: цикл живе рівно стільки, скільки гра.
	if m.playCancel == nil {
		return m, nil
	}
	if msg.periodic {
		return m, m.liveTickCmd(msg.gen)
	}
	if msg.err == nil && msg.snap.Playing {
		if m.liveTicking {
			return m, nil
		}
		m.liveTicking = true
		m.localTicking = true
		return m, tea.Batch(m.liveTickCmd(msg.gen), m.localTickCmd())
	}
	// Сесії ще немає: Live.set стається після Player.Start. Перепитуємо, поки
	// вона не з'явиться, але не нескінченно.
	if m.liveTicking || m.liveRetries >= liveStartTries {
		return m, nil
	}
	m.liveRetries++
	return m, m.liveRetryCmd(msg.gen)
}

// resetLive відкриває нове покоління вікна в сесію: усі відповіді Snapshot, що
// ще летять від попередньої, стають недійсними, а рядок оцінки зникає, поки
// нова сесія не відповість.
func (m *Model) resetLive() int {
	m.liveGen++
	m.live = playback.Snapshot{}
	m.liveTicking, m.liveRetries = false, 0
	m.localTicking = false
	m.liveAt = time.Time{}
	m.liveFrozen = true
	return m.liveGen
}

func (m *Model) applyBookmarkBaseline(msg bookmarkBaselineMsg) {
	if msg.err != nil {
		return
	}
	m.seedReleases([]playback.ReleaseSeed{{Ref: msg.ref, Episodes: msg.eps}})
	_ = m.eng.ReconcileKnown(msg.ref, msg.provisional, msg.maxEp)
	m.refreshLibraryLists()
}

func (m Model) startPlayback(res *playback.Resolved) (tea.Model, tea.Cmd) {
	// Begin пише бібліотеку, тому виконується тут, до запуску команди.
	// Помилка (немає плеєра, не записалась бібліотека) лишає екран як був.
	titleID, pinned, err := m.eng.Begin(res)
	if err != nil {
		m.endChain()
		m.errText = m.errorText(err)
		return m, nil
	}
	if !m.chainActive {
		m.beginChain(m.ref)
	}
	if m.screen == screenStudio && len(m.stack) > 0 {
		m.stack = m.stack[:len(m.stack)-1]
	}
	m.pendingEp = res.Episode
	m.playTitleID, m.playPinned = titleID, pinned
	m.playGen++
	m.journalWarning = false
	m.setScreen(screenPlaying)
	// Статус лишається порожнім навмисно: екран «Грає» тепер керований, і
	// внизу корисніша підказка з клавішами, ніж «плеєр запущено».
	m.status = ""
	m.statusKind = statusInfo
	m.statusGen++
	if res.PinFallback {
		m.status = fmt.Sprintf(i18n.TuiStudioFallback, m.studioPin(), res.Source.Studio)
		m.statusKind = statusWarning
		m.statusGen++
	}
	cmd, cancel := m.playCmd(res, titleID)
	m.playCancel = cancel
	// Серія, що грає, підсвічується в списку на телефоні — тому публікуємо вже
	// з виставленим playCancel.
	m.publishPlaylist()
	// Знімок замовляється разом із сесією: перша відповідь запускає цикл, а до
	// неї рядок оцінки просто відсутній.
	return m, tea.Batch(cmd, m.liveSnapshotCmd(m.resetLive()), m.journalCmd())
}

// requestQuit — двофазний вихід. Під час відтворення Ctrl+C і сигнал лише
// скасовують сесію: сам вихід робить обробник playDoneMsg, коли Finish уже
// злив журнал. Інакше вихід гонився б із завершенням плеєра.
func (m Model) requestQuit() (tea.Model, tea.Cmd) {
	m.endChain()
	m.freezeLive()
	if m.playCancel != nil {
		m.quitting = true
		m.playCancel()
		return m, nil
	}
	return m, tea.Quit
}
