package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
)

const testRemoteURL = "http://vitaliis-macbook-pro.local:51234/r/0123456789abcdef0123456789abcdef"

// Пульт просить наступну серію, поки автоплей вимкнено: намір користувача
// сильніший за налаштування, і ланцюжок іде тим самим шляхом playDoneMsg.
func TestJourneyRemoteNextPlaysNextEpisodeWithoutAutoplay(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	m, fp, _ := journeyModel(t,
		held,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	m.eng.Autoplay = false
	live := &playback.Live{}
	m.eng.Live = live
	tr := &trace{}

	// пульт живе на власній горутині, як net/http; pump тим часом блокується
	// всередині Run
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			snap, err := live.Snapshot()
			if err != nil {
				done <- err
				return
			}
			if snap.Playing {
				done <- live.Next()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		done <- fmt.Errorf("пульт не дочекався сесії")
	}()

	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenStudio)
	m = press(t, m, tr, tea.KeyEnter, "")

	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	mustScreen(t, m, screenEpisodes)
	starts := fp.Starts()
	if len(starts) != 2 {
		t.Fatalf("запусків плеєра = %d, want 2 (сліди: %v)", len(starts), tr.screens)
	}
	if !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Errorf("Start[1] = %+v, want серія 2", starts[1])
	}
	if tr.count(screenPlaying) != 1 {
		t.Errorf("входів на екран відтворення = %d, want 1", tr.count(screenPlaying))
	}
	if m.status != fmt.Sprintf(i18n.MsgProgressSaved, 0, 30) {
		t.Errorf("статус = %q, want прогрес 00:30 серії 2", m.status)
	}
}

func TestPlayingFrameShowsRemoteURL(t *testing.T) {
	for _, tt := range []struct {
		w, h     int
		wantURL  bool
		wantHint bool
	}{
		{80, 24, false, false},
		{120, 40, true, false},
		{40, 12, false, true},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			m := newTestModel(t)
			m.remote.URL = testRemoteURL
			m.screen = screenPlaying
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: tt.w, Height: tt.h})

			content := m.View().Content
			plain := ansi.Strip(content)
			for i, line := range strings.Split(content, "\n") {
				if w := lipgloss.Width(line); w > tt.w {
					t.Errorf("рядок %d ширший за вікно (%d > %d): %q", i, w, tt.w, ansi.Strip(line))
				}
			}
			// 80 колонок: повний «Пульт: <url>» не влазить, голий URL — влазить
			if got := strings.Contains(plain, testRemoteURL); got == tt.wantHint {
				t.Errorf("кадр %dx%d: адреса пульта = %v (вузьке вікно має ховати її цілком):\n%s", tt.w, tt.h, got, plain)
			}
			if got := strings.Contains(plain, fmt.Sprintf(i18n.TuiRemote, testRemoteURL)); got != tt.wantURL {
				t.Errorf("кадр %dx%d: повний рядок пульта = %v, want %v", tt.w, tt.h, got, tt.wantURL)
			}
			if got := strings.Contains(plain, i18n.TuiRemoteNarrow); got != tt.wantHint {
				t.Errorf("кадр %dx%d: вузька підказка = %v, want %v:\n%s", tt.w, tt.h, got, tt.wantHint, plain)
			}
		})
	}
}

func TestPlayingFrameWithoutRemote(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if plain := ansi.Strip(m.View().Content); strings.Contains(plain, i18n.TuiRemoteNarrow) || strings.Contains(plain, "/r/") {
		t.Errorf("без пульта рядка бути не має:\n%s", plain)
	}
}
