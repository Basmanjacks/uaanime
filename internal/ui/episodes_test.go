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
