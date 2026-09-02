package ui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func TestResumeAnyOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		episodesFirst bool
	}{
		{name: "episodes then resolved", episodesFirst: true},
		{name: "resolved then episodes", episodesFirst: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("resume", 1)[0]
			seedTestHistory(&m, []provider.TitleRef{ref})
			selectTestItem(t, &m, func(it item) bool {
				_, ok := it.payload.(payloadResume)
				return ok
			})
			m, _ = pressTestKey(t, m, tea.KeyEnter, "")
			req := m.reqID
			epsMsg := episodesDoneMsg{req: req, ref: ref, eps: testEpisodes(3), navigate: false}
			resMsg := resolvedMsg{req: req, res: &playback.Resolved{
				Ref:     ref,
				Episode: 1,
				Source:  provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 1},
			}}

			if tc.episodesFirst {
				m, _ = updateTestModel(t, m, epsMsg)
				m, _ = updateTestModel(t, m, resMsg)
			} else {
				m, _ = updateTestModel(t, m, resMsg)
				m, _ = updateTestModel(t, m, epsMsg)
			}

			if got := len(m.episodes); got != 3 {
				t.Errorf("episodes populated = %d, want 3", got)
			}
			if m.screen != screenPlaying {
				t.Errorf("screen = %d, want %d", m.screen, screenPlaying)
			}
		})
	}
}

func TestPlayDoneAutoplaysNextEpisodeAfterEOF(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.ref = testRefs("autoplay", 1)[0]
	m.episodes = []provider.Episode{{Number: 1}, {Number: 3}}
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{reason: player.EndEOF})

	if cmd == nil {
		t.Fatal("EOF з автоплеєм не повернув команду resolve")
	}
	if m.screen != screenPlaying {
		t.Fatalf("screen = %d, want playing %d", m.screen, screenPlaying)
	}
	if m.pendingEp != 3 {
		t.Fatalf("pending episode = %d, want 3", m.pendingEp)
	}
	if m.status != i18n.TuiResolving {
		t.Errorf("status = %q, want %q", m.status, i18n.TuiResolving)
	}
}

func TestPlayDoneStopsAutoplayAfterLastEpisode(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 2

	m, cmd := updateTestModel(t, m, playDoneMsg{reason: player.EndEOF})

	if cmd != nil {
		t.Fatal("last episode returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestPlayDoneDoesNotAutoplayAfterQuit(t *testing.T) {
	m := newTestModel(t)
	m.eng.Autoplay = true
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{reason: player.EndQuit})

	if cmd != nil {
		t.Fatal("quit returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestPlayDoneDoesNotAutoplayWhenDisabled(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.pendingEp = 1

	m, cmd := updateTestModel(t, m, playDoneMsg{reason: player.EndEOF})

	if cmd != nil {
		t.Fatal("disabled autoplay returned a resolve command")
	}
	if m.screen != screenEpisodes {
		t.Fatalf("screen = %d, want episodes %d", m.screen, screenEpisodes)
	}
}

func TestResolvedErrorLeavesPlayingScreen(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.episodes = testEpisodes(2)
	m.reqID = 7

	m, _ = updateTestModel(t, m, resolvedMsg{req: 7, err: errors.New("resolve failed")})

	if m.screen == screenPlaying {
		t.Fatal("resolve error left model on playing screen")
	}
	if m.errText == "" {
		t.Fatal("resolve error was not shown")
	}
}

func TestResumeFromHistory(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("history", 2)
	seedTestHistory(&m, refs)
	selectTestItem(t, &m, func(it item) bool {
		_, ok := it.payload.(payloadHistory)
		return ok
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenHistory {
		t.Fatalf("screen after opening history = %d, want %d", m.screen, screenHistory)
	}
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req := m.reqID
	selectedRef := refs[1]
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      req,
		ref:      selectedRef,
		eps:      testEpisodes(2),
		navigate: false,
	})
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:     selectedRef,
		Episode: 2,
		Source:  provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 2},
	}})
	if m.screen != screenPlaying {
		t.Fatalf("screen after history resume = %d, want %d", m.screen, screenPlaying)
	}
	m, _ = updateTestModel(t, m, playDoneMsg{})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after playback = %d, want %d", m.screen, screenEpisodes)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHistory {
		t.Fatalf("screen after leaving episodes = %d, want %d", m.screen, screenHistory)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after leaving history = %d, want %d", m.screen, screenHome)
	}
}

// ---- 2.3: двофазний вихід і синхронний Begin ----

func testPlayingModel(t *testing.T, m Model, ref provider.TitleRef) Model {
	t.Helper()

	titleID, _, err := m.eng.Begin(&playback.Resolved{
		Ref: ref, Episode: 1,
		Source: provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 1},
	})
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	m.ref = ref
	m.episodes = testEpisodes(2)
	m.pendingEp = 1
	m.playTitleID = titleID
	m.screen = screenPlaying
	return m
}

// TestQuitDuringPlaybackWaitsForFinish — Ctrl+C під час відтворення лише
// скасовує сесію: вихід стається після playDoneMsg, коли журнал уже злитий.
func TestQuitDuringPlaybackWaitsForFinish(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("quit-during-play", 1)[0]
	m = testPlayingModel(t, m, ref)
	titleID := m.playTitleID
	cancelled := false
	m.playCancel = func() { cancelled = true }
	if err := m.eng.Store.WriteJournal(&store.Journal{
		TitleID: m.playTitleID, Episode: 1, PositionSec: 300, DurationSec: 1200, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("WriteJournal: %v", err)
	}

	m, cmd := updateTestModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("ctrl+c під час відтворення повернув команду %T, очікував nil", cmd())
	}
	if !cancelled {
		t.Fatal("ctrl+c не скасував сесію плеєра")
	}
	if !m.quitting {
		t.Fatal("ctrl+c не позначив вихід")
	}

	m, cmd = updateTestModel(t, m, playDoneMsg{reason: player.EndQuit})
	progress := m.eng.Lib.ProgressFor(titleID, 1)
	if progress == nil || progress.PositionSec != 300 {
		t.Fatalf("прогрес після Finish = %+v, очікував 300 с", progress)
	}
	if cmd == nil {
		t.Fatal("playDoneMsg після ctrl+c не повернув команду виходу")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("команда після playDoneMsg = %T, очікував tea.QuitMsg", msg)
	}
}

// TestSignalOnHomeQuitsImmediately — поза відтворенням сигнал = негайний вихід.
func TestSignalOnHomeQuitsImmediately(t *testing.T) {
	m := newTestModel(t)

	m, cmd := updateTestModel(t, m, signalMsg{})
	if m.quitting {
		t.Error("сигнал на домівці почав двофазний вихід")
	}
	if cmd == nil {
		t.Fatal("сигнал на домівці не повернув команду")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("команда = %T, очікував tea.QuitMsg", msg)
	}
}

// TestSignalDuringPlaybackCancelsWithoutQuit — сигнал іде тим самим шляхом,
// що й Ctrl+C: спершу закриваємо плеєр, вихід — після Finish.
func TestSignalDuringPlaybackCancelsWithoutQuit(t *testing.T) {
	m := newTestModel(t)
	m = testPlayingModel(t, m, testRefs("signal-during-play", 1)[0])
	cancelled := false
	m.playCancel = func() { cancelled = true }

	m, cmd := updateTestModel(t, m, signalMsg{})
	if cmd != nil {
		t.Fatalf("сигнал під час відтворення повернув команду %T, очікував nil", cmd())
	}
	if !cancelled || !m.quitting {
		t.Fatalf("сигнал під час відтворення: cancelled=%v quitting=%v", cancelled, m.quitting)
	}
}

// TestStartPlaybackWithoutPlayerShowsError — Begin падає ДО зміни екрана.
func TestStartPlaybackWithoutPlayerShowsError(t *testing.T) {
	m := newTestModel(t)
	m.eng.Player = nil
	ref := testRefs("no-player", 1)[0]
	m.ref = ref
	m.episodes = testEpisodes(2)
	m.screen = screenEpisodes
	m.reqID = 3

	m, cmd := updateTestModel(t, m, resolvedMsg{req: 3, res: &playback.Resolved{
		Ref: ref, Episode: 1,
		Source: provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 1},
	}})

	if m.screen == screenPlaying {
		t.Fatal("без плеєра модель перейшла на екран відтворення")
	}
	if m.errText != i18n.MsgNoPlayer {
		t.Fatalf("errText = %q, want %q", m.errText, i18n.MsgNoPlayer)
	}
	if cmd != nil {
		t.Fatalf("без плеєра повернено команду %T", cmd())
	}
	if len(m.eng.Lib.Titles) != 0 || len(m.eng.Lib.Entries) != 0 {
		t.Fatalf("невдалий Begin змінив бібліотеку: %d тайтлів, %d записів",
			len(m.eng.Lib.Titles), len(m.eng.Lib.Entries))
	}
}
