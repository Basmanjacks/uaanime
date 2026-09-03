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
		m.episodes, m.episodesRef = msg.eps, msg.ref
		// Список приїхав — пульт має його побачити навіть тоді, коли на екран
		// серій ми не заходимо («Продовжити» йде з navigate:false).
		m.publishPlaylist()
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
		m.resetLive()
		// Finish синхронний: журнал зливається в бібліотеку тут, на горутині
		// Update, а не у фоновій команді.
		result, err := m.eng.Finish(msg.reason, m.playTitleID, m.pendingEp)
		result.PinnedStudio = m.playPinned
		m.playTitleID, m.playPinned = "", ""
		// Журнал уже злитий у бібліотеку — публікуємо список із новою позначкою
		// «переглянуто» і без підсвіченої серії.
		m.publishPlaylist()
		if msg.err != nil {
			err = msg.err
		}
		// намір пульта сильніший за налаштування: «наступна» йде далі навіть
		// без автоплею, «стоп» уриває ланцюжок навіть з ним
		chain, requested := false, 0
		switch {
		case m.quitting || err != nil || result.Intent == playback.IntentStop:
		case result.Intent == playback.IntentPlay:
			// Адресний запит: ціль названа явно, тому наступну серію не рахуємо.
			// Ref звіряємо, бо між публікацією списку і запитом ми могли піти на
			// інший тайтл, і номер серії сам по собі вже нічого не означає.
			if result.Requested.Ref.Same(m.ref) {
				requested = result.Requested.Episode
			}
		case result.Intent == playback.IntentNext:
			chain = true
		case result.StopAfter:
			// «досидіти й зупинитись» сильніше за автоплей — і тільки за нього
		case result.Reason == player.EndEOF && m.eng.Autoplay:
			chain = true
		}
		if requested > 0 {
			req := m.nextReq()
			m.pendingEp = requested
			m.status = i18n.TuiResolving
			return m, m.resolveCmd(m.ref, requested, req, m.eng.ResolveHints(m.ref, requested))
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
	m.list, cmd = m.list.Update(msg)
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
	req := m.beginNav()
	m.pendingEp = msg.req.Episode
	m.errText = ""
	m.status = i18n.TuiResolving
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
		return m, m.liveTickCmd(msg.gen)
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
	return m.liveGen
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
	// Статус лишається порожнім навмисно: екран «Грає» тепер керований, і
	// внизу корисніша підказка з клавішами, ніж «плеєр запущено».
	m.status = ""
	if res.PinFallback {
		m.status = fmt.Sprintf(i18n.TuiStudioFallback, m.studioPin(), res.Source.Studio)
	}
	cmd, cancel := m.playCmd(res, titleID)
	m.playCancel = cancel
	// Серія, що грає, підсвічується в списку на телефоні — тому публікуємо вже
	// з виставленим playCancel.
	m.publishPlaylist()
	// Знімок замовляється разом із сесією: перша відповідь запускає цикл, а до
	// неї рядок оцінки просто відсутній.
	return m, tea.Batch(cmd, m.liveSnapshotCmd(m.resetLive()))
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
