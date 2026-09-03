package ui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/charmbracelet/x/ansi"
)

func TestEpisodesEscapeClearsAppliedFilterBeforeGoingBack(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("filtered-episodes", 1)[0]
	m.episodes = testEpisodes(3)
	m.showEpisodes()
	m = applyTestListFilter(t, m, "2")

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenEpisodes {
		t.Fatalf("screen after first esc = %d, want episodes %d", m.screen, screenEpisodes)
	}
	if m.list.FilterState() != list.Unfiltered {
		t.Fatalf("filter state after first esc = %s, want unfiltered", m.list.FilterState())
	}

	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("screen after second esc = %d, want home %d", m.screen, screenHome)
	}
}

func TestEpisodesHeaderShowsStudioPinAndFitsNarrowWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		pin  string
		want string
	}{
		{name: "pinned", pin: "Beta", want: "Beta"},
		{name: "automatic", want: i18n.TuiStudioAuto},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("episodes-header", 1)[0]
			m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
			if tc.pin != "" {
				m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: tc.pin}}
			}
			m.ref = ref
			m.screen = screenEpisodes
			m.w = 80

			view := ansi.Strip(m.View().Content)
			if want := fmt.Sprintf(i18n.TuiStudioPinned, tc.want); !strings.Contains(view, want) {
				t.Fatalf("episodes view does not contain %q: %q", want, view)
			}

			m.w = 20
			header := strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
			if got := lipgloss.Width(header); got > 20 {
				t.Fatalf("narrow header width = %d, want <= 20: %q", got, header)
			}
			if strings.Contains(header, "\n") {
				t.Fatalf("narrow header wrapped: %q", header)
			}
		})
	}
}

func TestEpisodesDoneOfflineUsesCacheStatus(t *testing.T) {
	m := newTestModel(t)
	m.reqID = 1

	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      1,
		ref:      testRefs("cached", 1)[0],
		eps:      testEpisodes(1),
		offline:  true,
		navigate: true,
	})

	if m.errText != "" {
		t.Fatalf("errText = %q, want empty", m.errText)
	}
	if m.status != i18n.MsgOfflineCache {
		t.Fatalf("status = %q, want %q", m.status, i18n.MsgOfflineCache)
	}
}

// Підказка екрана серій має влазити в мінімальні 80 колонок цілою: обрізане
// «Esc Назад» — це втрачений вихід із екрана.
func TestEpisodesHintFitsMinimumWidth(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("episodes-hint", 1)[0]
	m.episodes = testEpisodes(3)
	m.showEpisodes()
	m.status, m.errText = "", ""
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	if !strings.Contains(i18n.TuiHintEpisodes, "X ") {
		t.Fatal("підказка не згадує клавішу x")
	}
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, i18n.TuiHintEpisodes) {
		t.Fatalf("підказку обрізано у вікні 80 колонок:\n%s", view)
	}
}

// TestEpisodesHeaderRemainingDropsFirst — хвіст заголовка стискається за
// пріоритетом: у вузькому вікні залишок зникає, закріплена озвучка лишається,
// і жоден рядок кадру не ширший за термінал.
func TestEpisodesHeaderRemainingDropsFirst(t *testing.T) {
	m := newTestModel(t)
	ref := provider.TitleRef{
		Provider: "test",
		Slug:     "remaining-header",
		Name:     "Похорон Фрірен: за межами подорожі життя",
	}
	m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
	m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: "Beta"}}
	m.eng.Lib.Progress = []*library.Progress{
		{TitleID: ref.Slug, Episode: 1, DurationSec: 1440, Completed: true},
	}
	m.ref, m.episodesRef = ref, ref
	m.episodes = testEpisodes(12)
	m.showEpisodes()
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})

	remaining := fmt.Sprintf(i18n.TuiRemainingFmt, i18n.RemainingEpisodes(11), "4 год 24 хв")
	pin := fmt.Sprintf(i18n.TuiStudioPinned, "Beta")
	header := strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
	if !strings.Contains(header, remaining) {
		t.Fatalf("wide header %q does not contain %q", header, remaining)
	}
	if !strings.Contains(header, pin) {
		t.Fatalf("wide header %q does not contain %q", header, pin)
	}
	assertViewFitsWidth(t, m)

	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	header = strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
	if strings.Contains(header, i18n.RemainingEpisodes(11)) {
		t.Fatalf("narrow header %q kept the remaining part", header)
	}
	if !strings.Contains(header, pin) {
		t.Fatalf("narrow header %q dropped the studio pin", header)
	}
	assertViewFitsWidth(t, m)
}

// assertViewFitsWidth — інваріант кадру: перенесення рядка зсунуло б увесь
// екран і сховало нижній рядок.
func assertViewFitsWidth(t *testing.T, m Model) {
	t.Helper()

	for i, line := range strings.Split(m.View().Content, "\n") {
		if got := lipgloss.Width(line); got > m.w {
			t.Fatalf("line %d width = %d, want <= %d: %q", i, got, m.w, ansi.Strip(line))
		}
	}
}
