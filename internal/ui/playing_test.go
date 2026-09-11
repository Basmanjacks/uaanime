package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
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
	if err := m.eng.PinStudio(journeyRef, "FANVOXUA", ""); err != nil {
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
		if !strings.Contains(plain, m.hint()) {
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

// «Досидіти й зупинитись» уриває автоплей рівно на одній серії: наступну
// людина запускає сама, а налаштування лишається недоторканим.
func TestJourneyStopAfterEndsChainWithoutTouchingConfig(t *testing.T) {
	dir := t.TempDir()
	held := playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440})
	held.Hold = true
	m, fp, st := journeyModelIn(t, dir,
		held,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	m.eng.Live = &playback.Live{}
	if !m.eng.Autoplay {
		t.Fatal("сценарій має сенс лише з увімкненим автоплеєм")
	}
	// Конфіг на диску — щоб було що звіряти байт-у-байт.
	if err := st.SaveConfig(store.DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	configPath := filepath.Join(dir, "config.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json: %v", err)
	}
	tr := &trace{}

	m, done := startJourneyPlayback(t, m, tr, held)
	m, _ = updateTestModel(t, m, tea.KeyPressMsg{Code: '.', Text: "."})
	if m.status != i18n.TuiStopAfterOn {
		t.Fatalf("статус = %q, want %q", m.status, i18n.TuiStopAfterOn)
	}
	if !m.eng.Live.StopAfter() {
		t.Fatal("«.» не поставила прапорець у Live")
	}
	held.Release()

	select {
	case msg := <-done:
		m = deliver(t, m, msg, tr)
	case <-time.After(5 * time.Second):
		t.Fatal("сесія не завершилась")
	}
	mustScreen(t, m, screenEpisodes)
	if n := len(fp.Starts()); n != 1 {
		t.Fatalf("запусків плеєра = %d, want 1 (автоплей мав зупинитися)", n)
	}

	after, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("config.json змінився (%v):\n%s\n%s", err, before, after)
	}

	// Ланцюжок зупинено, але не заборонено: Enter стартує наступну серію.
	selectTestItem(t, &m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	starts := fp.Starts()
	if len(starts) != 2 || !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Fatalf("запуски плеєра = %+v, want ручний старт серії 2", starts)
	}
}

// Прапорець стосується однієї серії: повторне натискання знімає його.
func TestStopAfterKeyRejectsSessionGap(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m, _ = pressTestKey(t, m, '.', ".")
	if m.eng.Live.Limit().Enabled || m.errText == "" {
		t.Fatal("stop-after accepted with no player session")
	}
}

// Заголовок вікна термінала: під час перегляду — назва й серія, поза ним —
// порожньо (рендерер скине OSC 2 сам).
func TestWindowTitleOnlyWhilePlaying(t *testing.T) {
	m := newTestModel(t)
	if got := m.View().WindowTitle; got != "" {
		t.Fatalf("заголовок на домівці = %q, want порожній", got)
	}

	ref := testRefs("window-title", 1)[0]
	ref.Name = "Фрірен"
	m = testPlayingModel(t, m, ref)
	want := fmt.Sprintf(i18n.TuiWindowTitle, "Фрірен", 1)
	if got := m.View().WindowTitle; got != want {
		t.Fatalf("заголовок під час гри = %q, want %q", got, want)
	}

	m, _ = updateTestModel(t, m, playDoneMsg{})
	mustScreen(t, m, screenEpisodes)
	if got := m.View().WindowTitle; got != "" {
		t.Fatalf("заголовок після перегляду = %q, want порожній", got)
	}
}

// TestStartPlaybackWarnsByDeviation — попередження після Enter будується лише з
// того, з чим робився вибір (Pin/Prefs/Deviation), і розрізняє три різні речі:
// «озвучення ще не вийшло», «субтитрів немає» і «студія недоступна». Відхилення
// між озвученими типами не варте жодного рядка.
func TestStartPlaybackWarnsByDeviation(t *testing.T) {
	voice, sub, dub := provider.KindVoiceover, provider.KindSub, provider.KindDub
	for _, tc := range []struct {
		name      string
		pin       library.Pin
		source    provider.Source
		deviation library.Deviation
		want      string
	}{
		{
			name:      "озвучення ще не вийшло",
			pin:       library.Pin{Studio: "РГ", Kind: voice},
			source:    provider.Source{Studio: "РГ", Kind: sub},
			deviation: library.DeviationKind,
			want:      fmt.Sprintf(i18n.TuiKindNotOutYetLong, "РГ"),
		},
		{
			name:      "дубляж замість озвучення мовчить",
			pin:       library.Pin{Studio: "РГ", Kind: dub},
			source:    provider.Source{Studio: "РГ", Kind: voice},
			deviation: library.DeviationKind,
		},
		{
			name:      "субтитрів студії немає",
			pin:       library.Pin{Studio: "РГ", Kind: sub},
			source:    provider.Source{Studio: "РГ", Kind: voice},
			deviation: library.DeviationKind,
			want:      fmt.Sprintf(i18n.TuiSubsMissing, "РГ"),
		},
		{
			name:   "без піна лишились самі саби",
			source: provider.Source{Studio: "A", Kind: sub},
			want:   i18n.TuiOnlySubsStatus,
		},
		{
			name:      "інша студія та ще й у сабах",
			pin:       library.Pin{Studio: "A", Kind: dub},
			source:    provider.Source{Studio: "B", Kind: sub},
			deviation: library.DeviationStudio,
			want:      fmt.Sprintf(i18n.TuiStudioFallbackSub, "A", "B"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("deviation", 1)[0]
			m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
			m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: tc.pin.Studio, KindPin: tc.pin.Kind}}
			m.ref = ref
			m.screen = screenEpisodes
			m.reqID = 3

			m, _ = updateTestModel(t, m, resolvedMsg{req: 3, res: &playback.Resolved{
				Ref:       ref,
				Episode:   1,
				Source:    tc.source,
				Pin:       tc.pin,
				Deviation: tc.deviation,
			}})
			mustScreen(t, m, screenPlaying)

			view := ansi.Strip(m.View().Content)
			if tc.want == "" {
				if m.status != "" {
					t.Fatalf("статус = %q, want порожній", m.status)
				}
				for _, unwanted := range []string{
					fmt.Sprintf(i18n.TuiKindNotOutYetLong, "РГ"),
					i18n.TuiOnlySubsStatus,
					fmt.Sprintf(i18n.TuiSubsMissing, "РГ"),
					fmt.Sprintf(i18n.TuiStudioFallback, "РГ", "РГ"),
				} {
					if strings.Contains(view, unwanted) {
						t.Fatalf("кадр містить зайве попередження %q:\n%s", unwanted, view)
					}
				}
				return
			}
			if m.status != tc.want {
				t.Fatalf("статус = %q, want %q", m.status, tc.want)
			}
			if !strings.Contains(view, tc.want) {
				t.Fatalf("кадр не містить %q:\n%s", tc.want, view)
			}
		})
	}
}
