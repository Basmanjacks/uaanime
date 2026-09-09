package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

func TestJournalWarningSurvivesPlaybackFinalization(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, journalMsg{err: errors.New("disk full"), open: true})
	if !strings.Contains(m.View().Content, "Не вдалося зберегти прогрес") {
		t.Fatal("journal failure is not visible")
	}
	m, _ = updateTestModel(t, m, playDoneMsg{})
	if !strings.Contains(m.View().Content, "Не вдалося зберегти прогрес") {
		t.Fatal("finalization cleared unseen warning")
	}
	m, _ = updateTestModel(t, m, journalMsg{open: true})
	if strings.Contains(m.View().Content, "Не вдалося зберегти прогрес") {
		t.Fatal("recovery did not clear warning")
	}
}

func TestJournalIgnoresOldSession(t *testing.T) {
	m := newTestModel(t)
	m, _ = updateTestModel(t, m, journalMsg{gen: -1, err: errors.New("old"), open: true})
	if strings.Contains(m.View().Content, "Не вдалося зберегти прогрес") {
		t.Fatal("stale failure appeared")
	}
	m, _ = updateTestModel(t, m, journalMsg{err: errors.New("current"), open: true})
	m, _ = updateTestModel(t, m, journalMsg{gen: -1, open: true})
	if !m.journalWarning {
		t.Fatal("stale recovery cleared current warning")
	}
}

func TestJournalCommandReportsFailureRecoveryAndCloses(t *testing.T) {
	dir := t.TempDir()
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1200})
	held.Hold = true
	m, _, _ := journeyModelIn(t, dir, held)
	path := filepath.Join(dir, "state", "current.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	run, cancel := m.playCmd(&playback.Resolved{Episode: 1}, "title")
	defer cancel()
	done := make(chan tea.Msg, 1)
	go func() { done <- run() }()
	receive := func(cmd tea.Cmd) tea.Msg {
		t.Helper()
		result := make(chan tea.Msg, 1)
		go func() { result <- cmd() }()
		select {
		case msg := <-result:
			return msg
		case <-time.After(3 * time.Second):
			t.Fatal("journal subscription did not finish")
			return nil
		}
	}
	var next tea.Cmd
	m, next = updateTestModel(t, m, receive(m.journalCmd()))
	if !m.journalWarning {
		t.Fatal("Run observer did not deliver warning")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m, next = updateTestModel(t, m, receive(next))
	if m.journalWarning {
		t.Fatal("Run observer did not deliver recovery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
	m, next = updateTestModel(t, m, receive(next))
	if next != nil {
		t.Fatal("closed journal subscription rearmed")
	}
}

func TestPlayDoneKeepsRunAndFinishFailures(t *testing.T) {
	// Both diagnostic causes are deliberately visible only in debug mode.
	dir := t.TempDir()
	m, _, st := journeyModelIn(t, dir)
	m.playTitleID, m.pendingEp = "title", 1
	if err := st.WriteJournal(&store.Journal{TitleID: "title", Episode: 1, PositionSec: 20}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "library.json"), 0700); err != nil {
		t.Fatal(err)
	}
	m.opts.Debug = true
	m, _ = updateTestModel(t, m, playDoneMsg{err: errors.New("runtime-marker")})
	if !strings.Contains(m.errText, "runtime-marker") || !strings.Contains(m.errText, "library.json") {
		t.Fatalf("lost run or finalization failure: %q", m.errText)
	}
}
