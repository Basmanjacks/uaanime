package ui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"errors"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
	"time"
)

func TestUsabilityBudgetOverridesNeverAndClosesHelpOnEOF(t *testing.T) {
	for _, budget := range []int{1, 2} {
		t.Run(string(rune('0'+budget)), func(t *testing.T) {
			held := playertest.NewSession(player.EndEOF, []float64{1440}, []float64{1440})
			held.Hold = true
			m, fp, _ := journeyModel(t, held, playertest.NewSession(player.EndEOF, []float64{1440}, []float64{1440}), playertest.NewSession(player.EndEOF, []float64{1440}, []float64{1440}))
			m.eng.Autoplay = false
			tr := &trace{}
			m, done := startJourneyPlayback(t, m, tr, held)
			m, _ = pressTestKey(t, m, 'B', "B")
			m.list.Select(budget)
			m, _ = pressTestKey(t, m, tea.KeyEnter, "")
			if m.eng.Live.Limit().Remaining != budget {
				t.Fatal("B did not apply")
			}
			m, _ = pressTestKey(t, m, '?', "?")
			if m.overlay != overlayHelp {
				t.Fatal("help not opened")
			}
			held.Release()
			select {
			case msg := <-done:
				m = deliver(t, m, msg, tr)
			case <-time.After(5 * time.Second):
				t.Fatal("EOF hung")
			}
			if len(fp.Starts()) != budget || m.overlay != overlayNone || m.chainActive || m.eng.Live.Limit().Enabled {
				t.Fatalf("starts %d overlay%d chain%v limit%+v", len(fp.Starts()), m.overlay, m.chainActive, m.eng.Live.Limit())
			}
		})
	}
}
func TestUsabilityHelpPlaybackKeysAndBudgetOwnsKeys(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{60}, []float64{1440})
	held.Hold = true
	m, _, _ := journeyModel(t, held)
	m.eng.Autoplay = false
	m, done := startJourneyPlayback(t, m, &trace{}, held)
	defer held.Release()
	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if len(held.Calls()) != 0 {
		t.Fatal("help arrow sought player")
	}
	m, _ = pressTestKey(t, m, ' ', "")
	if len(held.Calls()) != 1 {
		t.Fatal("help Space did not reach player")
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	m, _ = pressTestKey(t, m, 'B', "B")
	before := len(held.Calls())
	for _, key := range []rune{'n', ' ', '.', '?'} {
		m, _ = pressTestKey(t, m, key, string(key))
	}
	if len(held.Calls()) != before {
		t.Fatal("budget leaked playback keys")
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	select {
	case msg := <-done:
		m, _ = updateTestModel(t, m, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung")
	}
	if m.chainActive {
		t.Fatal("stop retained chain")
	}
}
func TestUsabilityLocalClockTruthClampAndFreeze(t *testing.T) {
	m := newTestModel(t)
	now := time.Unix(100, 0)
	m.now = func() time.Time { return now }
	m.playCancel = func() {}
	m.screen = screenPlaying
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{Playing: true, PositionSec: 10, DurationSec: 14}})
	now = now.Add(2 * time.Second)
	if m.displayPosition() != 12 {
		t.Fatal("clock did not advance")
	}
	now = now.Add(20 * time.Second)
	if m.displayPosition() != 14 {
		t.Fatal("clock exceeded duration")
	}
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{Playing: true, PositionSec: 20}})
	now = now.Add(20 * time.Second)
	if m.displayPosition() != 25 {
		t.Fatal("clock exceeded poll grace")
	}
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, err: errors.New("poll")})
	now = now.Add(time.Minute)
	if m.displayPosition() != 25 {
		t.Fatal("poll error failed to freeze")
	}
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{Playing: true, Paused: true, PositionSec: 4}})
	now = now.Add(time.Minute)
	if m.displayPosition() != 4 {
		t.Fatal("paused clock advanced")
	}
}
func TestUsabilityOverlayAndPlayingRenderBounds(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {120, 36}} {
		for _, kind := range []overlayKind{overlayNone, overlayHelp, overlayBudget} {
			m := newTestModel(t)
			m.ic = themeIcons(true)
			m.screen = screenPlaying
			m.status = ""
			m.pendingEp = 7
			m.remote.URL = "http://remote.invalid/r/token"
			m.live = playback.Snapshot{Playing: true, Paused: true, Studio: strings.Repeat("Studio", 10), PositionSec: 123, SessionLimited: true, SessionRemaining: 2}
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			if kind != overlayNone {
				m.openOverlay(kind)
			}
			plain := ansi.Strip(m.View().Content)
			if lipgloss.Height(plain) > size[1] {
				t.Fatalf("%v overlay%d height%d", size, kind, lipgloss.Height(plain))
			}
			for _, line := range strings.Split(plain, "\n") {
				if lipgloss.Width(line) > size[0] {
					t.Fatalf("%v overlay%d width%d", size, kind, lipgloss.Width(line))
				}
			}
			if kind != overlayNone && strings.Contains(plain, m.remote.URL) {
				t.Fatal("overlay rendered remote address")
			}
			if kind == overlayNone && !strings.Contains(plain, "2") {
				t.Fatal("narrow playback hid active budget")
			}
			m.journalWarning = true
			if !strings.Contains(ansi.Strip(m.View().Content), truncate(i18n.MsgJournalFailed, size[0]-2)) {
				t.Fatal("journal warning hidden")
			}
		}
	}
}
func TestUsabilityEscapeCancelsResolveGap(t *testing.T) {
	m := newTestModel(t)
	m.ref = journeyRef
	m.episodesRef = m.ref
	m.episodes = testEpisodes(3)
	m.screen = screenPlaying
	m.beginChain(m.ref)
	m.reqID = 9
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.chainActive || m.screen != screenEpisodes || m.reqID == 9 {
		t.Fatal("Escape left resolve gap active")
	}
	m, _ = updateTestModel(t, m, resolvedMsg{req: 9, res: &playback.Resolved{Ref: journeyRef, Episode: 2}})
	if m.playCancel != nil {
		t.Fatal("late resolve started canceled chain")
	}
}
func TestUsabilityHelpHomeDoesNotResetRemoteGeneration(t *testing.T) {
	m := newTestModel(t)
	before := m.eng.Live.CurrentGen()
	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.eng.Live.CurrentGen() != before {
		t.Fatal("closing help reset remote playlist")
	}
}
func TestUsabilityBudgetExplainsCurrentEpisodeAndNarrowShowsBudget(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.status = ""
	m.pendingEp = 7
	m.live = playback.Snapshot{Playing: true, Paused: true, PositionSec: 65, SessionLimited: true, SessionRemaining: 12}
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 40, Height: 12})
	if !strings.Contains(ansi.Strip(m.View().Content), "12") {
		t.Fatal("narrow paused playback clipped budget")
	}
	m.openOverlay(overlayBudget)
	plain := strings.Join(strings.Fields(ansi.Strip(m.View().Content)), " ")
	if !strings.Contains(plain, i18n.TuiBudgetNote) {
		t.Fatal("budget explanation missing")
	}
}
func TestUsabilityManualNextReplacesBudgetSlotAndBudgetClosesOnEOF(t *testing.T) {
	first := playertest.NewSession(player.EndQuit, []float64{20}, []float64{1440})
	first.Hold = true
	second := playertest.NewSession(player.EndEOF, []float64{1440}, []float64{1440})
	second.Hold = true
	m, fp, _ := journeyModel(t, first, second, playertest.NewSession(player.EndEOF, []float64{1440}, []float64{1440}))
	m.eng.Autoplay = false
	m, done := startJourneyPlayback(t, m, &trace{}, first)
	defer first.Release()
	defer second.Release()
	m, _ = pressTestKey(t, m, 'B', "B")
	m.list.Select(2)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	m, _ = pressTestKey(t, m, 'n', "n")
	var resolve tea.Cmd
	select {
	case msg := <-done:
		m, resolve = updateTestModel(t, m, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("manual next hung")
	}
	if m.eng.Live.Limit().Remaining != 2 {
		t.Fatal("manual next consumed budget")
	}
	msg := resolve()
	m, cmd := updateTestModel(t, m, msg)
	nextDone := playInBackground(cmd)
	select {
	case <-second.Sampled:
	case <-time.After(5 * time.Second):
		t.Fatal("second did not start")
	}
	m, _ = pressTestKey(t, m, 'B', "B")
	if m.list.Index() != 2 {
		t.Fatal("B did not select remaining budget")
	}
	second.Release()
	select {
	case msg := <-nextDone:
		m = deliver(t, m, msg, &trace{})
	case <-time.After(5 * time.Second):
		t.Fatal("EOF with B hung")
	}
	if len(fp.Starts()) != 3 || m.overlay != overlayNone || m.chainActive {
		t.Fatalf("starts%d overlay%d chain%v", len(fp.Starts()), m.overlay, m.chainActive)
	}
}
func TestUsabilityStopAfterAtomicConvenience(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{0}, []float64{1440})
	held.Hold = true
	m, _, _ := journeyModel(t, held)
	m, done := startJourneyPlayback(t, m, &trace{}, held)
	defer held.Release()
	m, _ = pressTestKey(t, m, 'B', "B")
	m.list.Select(2)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	m, _ = pressTestKey(t, m, '.', ".")
	if l := m.eng.Live.Limit(); !l.Enabled || l.Remaining != 1 {
		t.Fatalf("dot at2=%+v", l)
	}
	m, _ = pressTestKey(t, m, '.', ".")
	if m.eng.Live.Limit().Enabled {
		t.Fatal("dot at1 did not turn off")
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	select {
	case msg := <-done:
		_, _ = updateTestModel(t, m, msg)
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung")
	}
}

func TestUsabilityAcceptedNextClosesHelpBeforePlayerDone(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440})
	held.Hold = true
	m, _, _ := journeyModel(t, held)
	m.eng.Autoplay = false
	m, done := startJourneyPlayback(t, m, &trace{}, held)
	defer held.Release()
	m, _ = pressTestKey(t, m, '?', "?")
	m, _ = pressTestKey(t, m, ' ', "")
	if m.overlay != overlayHelp {
		t.Fatal("pause closed help")
	}
	m, _ = pressTestKey(t, m, 'N', "N")
	if m.overlay != overlayNone {
		t.Error("accepted Next kept help open until playDone")
	}
	select {
	case msg := <-done:
		m, _ = updateTestModel(t, m, msg)
		m.endChain()
	case <-time.After(5 * time.Second):
		t.Fatal("Next did not finish player")
	}
}

func TestUsabilityRejectedNextRetainsHelp(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m.openOverlay(overlayHelp)
	m, _ = pressTestKey(t, m, 'n', "n")
	if m.overlay != overlayHelp || m.errText != i18n.TuiNotPlaying {
		t.Fatalf("rejected Next overlay%d error%q", m.overlay, m.errText)
	}
}
