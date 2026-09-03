package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// collectMsgs виконує команду (у тому числі пакет) паралельно й повертає все,
// що приїхало за timeout. Так тест бачить різницю між знімком без затримки і
// тіком на п'ять секунд, не чекаючи на другий.
func collectMsgs(cmd tea.Cmd, timeout time.Duration) []tea.Msg {
	if cmd == nil {
		return nil
	}
	first := cmd()
	batch, ok := first.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{first}
	}
	cmds := []tea.Cmd(batch)
	out := make(chan tea.Msg, len(cmds))
	for _, c := range cmds {
		go func(c tea.Cmd) {
			if c == nil {
				return
			}
			out <- c()
		}(c)
	}
	deadline := time.After(timeout)
	var msgs []tea.Msg
	for range cmds {
		select {
		case msg := <-out:
			msgs = append(msgs, msg)
		case <-deadline:
			return msgs
		}
	}
	return msgs
}

// Перша команда після startPlayback — знімок стану, і саме без затримки:
// інакше рядок оцінки з'явився б лише через тік.
func TestStartPlaybackAsksForSnapshotImmediately(t *testing.T) {
	m := newTestModel(t)
	m.eng.Live = &playback.Live{}
	ref := testRefs("live-start", 1)[0]
	m.ref = ref
	m.episodes = testEpisodes(2)
	m.screen = screenEpisodes
	m.reqID = 1

	m, cmd := updateTestModel(t, m, resolvedMsg{req: 1, res: &playback.Resolved{
		Ref: ref, Episode: 1,
		Source: provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 1},
	}})
	mustScreen(t, m, screenPlaying)

	var got *liveMsg
	for _, msg := range collectMsgs(cmd, time.Second) {
		if live, ok := msg.(liveMsg); ok {
			got = &live
		}
	}
	if got == nil {
		t.Fatal("startPlayback не замовив знімок Live")
	}
	if got.gen != m.liveGen || got.periodic {
		t.Fatalf("знімок = %+v, want gen %d і не періодичний", *got, m.liveGen)
	}
}

func TestEtaLineShowsFinishTime(t *testing.T) {
	now := time.Date(2026, 9, 3, 21, 21, 0, 0, time.Local)
	for _, tt := range []struct {
		name   string
		paused bool
		want   string
	}{
		{name: "грає", want: fmt.Sprintf(i18n.TuiFinishAt, "21:35")},
		{name: "на паузі", paused: true, want: fmt.Sprintf(i18n.TuiFinishAtPaused, "21:35")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModel(t)
			m.now = func() time.Time { return now }
			m.screen = screenPlaying
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
			m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{
				Playing: true, PositionSec: 600, DurationSec: 1440, Paused: tt.paused,
			}})

			if plain := ansi.Strip(m.View().Content); !strings.Contains(plain, tt.want) {
				t.Fatalf("кадр не містить %q:\n%s", tt.want, plain)
			}
		})
	}
}

// Без відомої тривалості рядка немає — ані на екрані «Грає», ані деінде.
func TestEtaLineHiddenWithoutDuration(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{
		Playing: true, PositionSec: 600,
	}})

	if line := m.etaLine(); line != "" {
		t.Fatalf("рядок без тривалості = %q, want порожній", line)
	}
	if plain := ansi.Strip(m.View().Content); strings.Contains(plain, strings.SplitN(i18n.TuiFinishAt, "%", 2)[0]) {
		t.Fatalf("кадр показує оцінку без тривалості:\n%s", plain)
	}
}

// Клавіші керування (S14) замовляють знімок кожна — але жоден із них не має
// права озброїти другий п'ятисекундний цикл.
func TestKeySnapshotsDoNotStartSecondTick(t *testing.T) {
	m := newTestModel(t)
	m.eng.Live = &playback.Live{}
	m.screen = screenPlaying
	m.playCancel = func() {}
	playing := playback.Snapshot{Playing: true, PositionSec: 10, DurationSec: 1440}

	m, cmd := updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playing})
	if cmd == nil {
		t.Fatal("перший знімок сесії, що грає, має запустити періодичний цикл")
	}
	if !m.liveTicking {
		t.Fatal("цикл не позначено як активний")
	}
	for i := range 5 {
		var extra tea.Cmd
		m, extra = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playing})
		if extra != nil {
			t.Fatalf("знімок від клавіші %d озброїв ще один цикл", i+1)
		}
	}
	// Відповідь тіка — єдине, що переозброює цикл.
	m, cmd = updateTestModel(t, m, liveMsg{periodic: true, gen: m.liveGen, snap: playing})
	if cmd == nil {
		t.Fatal("відповідь тіка не переозброїла цикл")
	}
}

// Затримана відповідь попередньої сесії не має переписати стан нової.
func TestStaleLiveMsgIgnored(t *testing.T) {
	m := newTestModel(t)
	m.eng.Live = &playback.Live{}
	m.screen = screenPlaying
	m.playCancel = func() {}
	stale := m.liveGen
	fresh := m.resetLive()

	m, _ = updateTestModel(t, m, liveMsg{gen: fresh, snap: playback.Snapshot{
		Playing: true, PositionSec: 60, DurationSec: 1440,
	}})
	m, cmd := updateTestModel(t, m, liveMsg{periodic: true, gen: stale, snap: playback.Snapshot{
		Playing: true, PositionSec: 900, DurationSec: 1440,
	}})

	if m.live.PositionSec != 60 {
		t.Fatalf("позиція = %v, want 60 (стара відповідь переписала нову)", m.live.PositionSec)
	}
	if cmd != nil {
		t.Fatal("стара відповідь переозброїла цикл")
	}
}

// Live.set стається в Run уже після Player.Start: перший знімок бачить idle,
// і цикл мусить дочекатися сесії, а не здатися.
func TestLiveRetriesUntilSessionAppears(t *testing.T) {
	m := newTestModel(t)
	m.eng.Live = &playback.Live{}
	m.screen = screenPlaying
	m.playCancel = func() {}
	idle := liveMsg{gen: m.liveGen, snap: playback.Snapshot{VolumePct: playback.VolumeUnknown}}

	m, cmd := updateTestModel(t, m, idle)
	if cmd == nil || m.liveRetries != 1 {
		t.Fatalf("idle-знімок: cmd=%v retries=%d, want повтор", cmd != nil, m.liveRetries)
	}
	for range liveStartTries {
		m, cmd = updateTestModel(t, m, idle)
	}
	if cmd != nil {
		t.Fatalf("повтори не зупинилися після %d спроб", liveStartTries)
	}
	if m.liveTicking {
		t.Fatal("цикл озброєно без сесії")
	}

	// Сесія з'явилася — далі працює звичайний періодичний цикл.
	m.liveRetries = 0
	m, cmd = updateTestModel(t, m, liveMsg{gen: m.liveGen, snap: playback.Snapshot{
		Playing: true, PositionSec: 1, DurationSec: 1440,
	}})
	if cmd == nil || !m.liveTicking {
		t.Fatal("поява сесії не запустила цикл")
	}
}

// Після повернення на список сесії немає: тік із її покоління мертвий, а
// рядок оцінки зникає разом зі станом.
func TestLiveTickStopsAfterPlayback(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("live-stop", 1)[0]
	m = testPlayingModel(t, m, ref)
	m.playCancel = func() {}
	gen := m.liveGen
	m, _ = updateTestModel(t, m, liveMsg{gen: gen, snap: playback.Snapshot{
		Playing: true, PositionSec: 60, DurationSec: 1440,
	}})

	m, _ = updateTestModel(t, m, playDoneMsg{})
	mustScreen(t, m, screenEpisodes)
	if m.live.Playing {
		t.Fatal("стан сесії пережив її завершення")
	}
	m, cmd := updateTestModel(t, m, liveMsg{periodic: true, gen: gen, snap: playback.Snapshot{
		Playing: true, PositionSec: 65, DurationSec: 1440,
	}})
	if cmd != nil {
		t.Fatal("тік завершеної сесії переозброївся")
	}
	if m.live.Playing {
		t.Fatal("тік завершеної сесії оновив стан")
	}
}
