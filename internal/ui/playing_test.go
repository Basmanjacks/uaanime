package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
)

// playInBackground виконує команду startPlayback (пакет «сесія плеєра + знімок
// Live») на власній горутині: сесія блокує до Release, а тест тим часом
// натискає клавіші, як це робив би користувач.
func playInBackground(cmd tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 4)
	go func() {
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			out <- msg
			return
		}
		for _, c := range batch {
			if c != nil {
				out <- c()
			}
		}
	}()
	return out
}

// startJourneyPlayback доводить сценарій до живої сесії: пошук → серії →
// Enter, після якого відтворення лишається у фоні.
func startJourneyPlayback(t *testing.T, m Model, tr *trace, sess *playertest.Session) (Model, <-chan tea.Msg) {
	t.Helper()
	if err := m.eng.PinStudio(journeyRef, "FANVOXUA"); err != nil {
		t.Fatalf("PinStudio: %v", err)
	}
	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)

	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	resolved, ok := cmd().(resolvedMsg)
	if !ok {
		t.Fatal("Enter на серії не дав resolvedMsg")
	}
	m, playCmd := updateTestModel(t, m, resolved)
	mustScreen(t, m, screenPlaying)
	done := playInBackground(playCmd)
	select {
	case <-sess.Sampled: // сесія вже в Live: команди керування дійдуть
	case <-time.After(5 * time.Second):
		t.Fatal("сесія так і не заграла")
	}
	return m, done
}

// Клавіші екрана «Грає» доходять до сесії, а «n» веде ланцюжок далі навіть
// із вимкненим автоплеєм — як і «наступна» з пульта.
func TestJourneyPlayingKeysControlSession(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	m, fp, _ := journeyModel(t, held, playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	m.eng.Autoplay = false
	m.eng.Live = &playback.Live{}
	tr := &trace{}

	m, done := startJourneyPlayback(t, m, tr, held)

	for _, key := range []tea.KeyPressMsg{
		{Code: ' '},
		{Code: tea.KeyRight},
		{Code: tea.KeyRight, Mod: tea.ModShift},
		{Code: '-', Text: "-"},
	} {
		var cmd tea.Cmd
		m, cmd = updateTestModel(t, m, key)
		if cmd == nil {
			t.Fatalf("клавіша %q не замовила знімок стану", key.String())
		}
		if m.errText != "" {
			t.Fatalf("клавіша %q: %s", key.String(), m.errText)
		}
	}
	want := []playertest.Call{
		{Op: "pause"},
		{Op: "seek", Delta: 10},
		{Op: "seek", Delta: 30},
		// 100 − 5: крок відносний, але в сесію йде абсолютний відсоток
		{Op: "volume", Delta: 95},
	}
	if got := held.Calls(); !slices.Equal(got, want) {
		t.Fatalf("команди сесії = %+v, want %+v", got, want)
	}

	m, _ = updateTestModel(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	select {
	case msg := <-done:
		if _, ok := msg.(playDoneMsg); !ok {
			t.Fatalf("«n» дало %T, want playDoneMsg", msg)
		}
		m = deliver(t, m, msg, tr)
	case <-time.After(5 * time.Second):
		t.Fatal("«n» не закрила сесію")
	}

	mustScreen(t, m, screenEpisodes)
	starts := fp.Starts()
	if len(starts) != 2 || !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Fatalf("запуски плеєра = %+v, want другу серію", starts)
	}
	if tr.count(screenPlaying) != 1 {
		t.Errorf("входів на екран відтворення = %d, want 1", tr.count(screenPlaying))
	}
}

// Сесії немає (плеєр закрили самі) — клавіша каже це людською мовою, а не
// сирою помилкою рушія.
func TestPlayingKeyWithoutSessionShowsShortError(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying

	m, cmd := updateTestModel(t, m, tea.KeyPressMsg{Code: ' '})
	if cmd != nil {
		t.Fatal("без сесії клавіша не має нічого замовляти")
	}
	if m.errText != i18n.TuiNotPlaying {
		t.Fatalf("errText = %q, want %q", m.errText, i18n.TuiNotPlaying)
	}
}

// Esc лишається скасуванням сесії: вихід робить playDoneMsg після Finish.
func TestPlayingEscCancelsSession(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	cancelled := false
	m.playCancel = func() { cancelled = true }

	m, cmd := pressTestKey(t, m, tea.KeyEsc, "")
	if !cancelled {
		t.Fatal("Esc не скасував сесію")
	}
	if cmd != nil || m.quitting {
		t.Fatalf("Esc: cmd=%v quitting=%v", cmd != nil, m.quitting)
	}
}

// Підказка «Грає» має дві версії: у 80 колонок влазить повна, у 40 — коротка.
func TestPlayingHintFitsWindow(t *testing.T) {
	for _, tt := range []struct {
		w    int
		want string
	}{
		{w: 120, want: i18n.TuiHintPlaying},
		{w: 80, want: i18n.TuiHintPlaying},
		{w: 40, want: i18n.TuiHintPlayingNarrow},
	} {
		m := newTestModel(t)
		m.screen = screenPlaying
		m.status = "" // startPlayback лишає статус порожнім — унизу підказка
		m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: tt.w, Height: 24})
		plain := ansi.Strip(m.View().Content)
		if !strings.Contains(plain, tt.want) {
			t.Fatalf("%d колонок: підказка не показана цілою:\n%s", tt.w, plain)
		}
	}
}

// Гучність із знімка — єдине підтвердження, що «+»/«−» дійшли до плеєра.
func TestPlayingLineShowsVolume(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{
		Playing: true, PositionSec: 60, DurationSec: 1440, VolumePct: 65,
	}})

	want := fmt.Sprintf(i18n.TuiVolume, 65)
	if plain := ansi.Strip(m.View().Content); !strings.Contains(plain, want) {
		t.Fatalf("кадр не показує гучність:\n%s", plain)
	}
	// Плеєр без звукової доріжки: рядок лишається, повзунка немає.
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{
		Playing: true, PositionSec: 60, DurationSec: 1440, VolumePct: playback.VolumeUnknown,
	}})
	if plain := ansi.Strip(m.View().Content); strings.Contains(plain, want) {
		t.Fatalf("невідома гучність не має показуватись:\n%s", plain)
	}
}
