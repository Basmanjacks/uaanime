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
	m.episodesRef = m.ref
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
		req:     1,
		ref:     testRefs("cached", 1)[0],
		eps:     testEpisodes(1),
		offline: true,
		purpose: epsOpen,
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
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, m.hint()) {
		t.Fatalf("підказку обрізано у вікні 80 колонок:\n%s", view)
	}
}

// TestEpisodesHeaderRemainingDropsFirst — хвіст заголовка стискається за
// пріоритетом: у вузькому вікні залишок зникає, закріплена озвучка лишається,
// і жоден рядок кадру не ширший за термінал.
func TestEpisodesHeaderEtaDropsFirst(t *testing.T) {
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

	// Хвіст відкидається за цінністю: оцінка часу — похідна від кількості,
	// тому зникає першою; кількість серій і пін лишаються.
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	header = strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
	if strings.Contains(header, "~4 год 24 хв") {
		t.Fatalf("narrow header %q kept the eta", header)
	}
	if !strings.Contains(header, i18n.RemainingEpisodes(11)) {
		t.Fatalf("narrow header %q dropped the remaining count before the eta", header)
	}
	if !strings.Contains(header, pin) {
		t.Fatalf("narrow header %q dropped the studio pin", header)
	}
	assertViewFitsWidth(t, m)

	// Ще вужче — зникає й кількість, але закріплена пара лишається останньою.
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 48, Height: 24})
	header = strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
	if strings.Contains(header, i18n.RemainingEpisodes(11)) {
		t.Fatalf("very narrow header %q kept the remaining count", header)
	}
	if !strings.Contains(header, pin) {
		t.Fatalf("very narrow header %q dropped the studio pin", header)
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

// epWithReleases — серія з готовим набором пар (студія, тип).
func epWithReleases(num int, releases ...provider.Release) provider.Episode {
	return provider.Episode{Number: num, Releases: releases}
}

// seedPinnedTitle — тайтл у бібліотеці з піном (студія, тип) і списком серій
// просто в моделі: рівно те, з чого episodeRows будує рядки.
func seedPinnedTitle(t *testing.T, m *Model, ref provider.TitleRef, pin library.Pin, eps []provider.Episode) {
	t.Helper()

	m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
	m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: pin.Studio, KindPin: pin.Kind}}
	m.ref, m.episodesRef, m.episodes = ref, ref, eps
}

// episodeRow — рядок списку серій за номером серії.
func episodeRow(t *testing.T, m Model, num int) item {
	t.Helper()

	for _, listItem := range m.list.Items() {
		it, ok := listItem.(item)
		if !ok {
			continue
		}
		if p, ok := it.payload.(payloadEp); ok && p.num == num {
			return it
		}
	}
	t.Fatalf("рядка серії %d немає в списку", num)
	return item{}
}

// TestEpisodeRowsShowReleasePairs — одиниця вибору в списку серій це пара
// (студія, тип): рядок каже, ЩО гратиме, і червоніє лише тоді, коли це саби,
// яких людина не просила.
func TestEpisodeRowsShowReleasePairs(t *testing.T) {
	const studio = "РГ"
	voice, sub, dub := provider.KindVoiceover, provider.KindSub, provider.KindDub

	t.Run("пін на озвучення", func(t *testing.T) {
		m := newTestModel(t)
		ref := testRefs("pairs-pinned", 1)[0]
		seedPinnedTitle(t, &m, ref, library.Pin{Studio: studio, Kind: voice}, []provider.Episode{
			epWithReleases(9, provider.Release{Studio: studio, Kind: voice}),
			epWithReleases(10,
				provider.Release{Studio: "kafori", Kind: voice},
				provider.Release{Studio: studio, Kind: sub},
				provider.Release{Studio: studio, Kind: voice}),
			epWithReleases(11, provider.Release{Studio: studio, Kind: sub}),
			epWithReleases(12),
		})
		m.eng.Lib.Progress = []*library.Progress{{TitleID: ref.Slug, Episode: 9, Completed: true}}
		m.showEpisodes()

		for _, tc := range []struct {
			num       int
			icon      string
			meta      string
			badge     string
			badgeWarn bool
		}{
			{num: 9, icon: m.ic.Done, badge: i18n.TuiEpDone},
			{
				num:  10,
				icon: m.ic.Pending,
				meta: i18n.KindShort(voice) + metaSep + studio + metaSep +
					fmt.Sprintf(i18n.TuiMoreVoiced, 1) + metaSep + fmt.Sprintf(i18n.TuiMoreSubs, 1),
			},
			{
				num:       11,
				icon:      m.ic.Pending,
				meta:      i18n.KindShort(sub) + metaSep + studio,
				badge:     i18n.TuiKindNotOutYet,
				badgeWarn: true,
			},
			{num: 12, icon: m.ic.Pending},
		} {
			row := episodeRow(t, m, tc.num)
			if row.meta != tc.meta {
				t.Errorf("серія %d: meta = %q, want %q", tc.num, row.meta, tc.meta)
			}
			if row.badge != tc.badge || row.badgeWarn != tc.badgeWarn {
				t.Errorf("серія %d: бейдж = (%q, %t), want (%q, %t)",
					tc.num, row.badge, row.badgeWarn, tc.badge, tc.badgeWarn)
			}
			if row.icon != tc.icon {
				t.Errorf("серія %d: іконка = %q, want %q", tc.num, row.icon, tc.icon)
			}
		}

		// Номери вирівняні під найдовший: «Серія  9» стоїть під «Серія 10».
		if want := fmt.Sprintf(i18n.TuiEpisodeNoPad, 2, 9); episodeRow(t, m, 9).title != want {
			t.Errorf("назва серії 9 = %q, want %q", episodeRow(t, m, 9).title, want)
		}
	})

	t.Run("без піна питає про студію", func(t *testing.T) {
		m := newTestModel(t)
		ref := testRefs("pairs-nopin", 1)[0]
		seedPinnedTitle(t, &m, ref, library.Pin{}, []provider.Episode{
			epWithReleases(1,
				provider.Release{Studio: "kafori", Kind: dub},
				provider.Release{Studio: studio, Kind: dub}),
		})
		m.showEpisodes()

		row := episodeRow(t, m, 1)
		if row.badge != i18n.TuiPickWillChooseStudio || row.badgeWarn {
			t.Fatalf("бейдж = (%q, %t), want (%q, false)", row.badge, row.badgeWarn, i18n.TuiPickWillChooseStudio)
		}
	})

	t.Run("явний пін на саби не попереджає", func(t *testing.T) {
		m := newTestModel(t)
		ref := testRefs("pairs-subpin", 1)[0]
		seedPinnedTitle(t, &m, ref, library.Pin{Studio: studio, Kind: sub}, []provider.Episode{
			epWithReleases(11, provider.Release{Studio: studio, Kind: sub}),
		})
		m.showEpisodes()

		row := episodeRow(t, m, 11)
		if row.badge != "" || row.badgeWarn {
			t.Fatalf("бейдж явного піна на саби = (%q, %t), want порожній", row.badge, row.badgeWarn)
		}
		if want := i18n.KindShort(sub) + metaSep + studio; row.meta != want {
			t.Fatalf("meta = %q, want %q", row.meta, want)
		}
	})

	t.Run("почата серія показує позицію", func(t *testing.T) {
		m := newTestModel(t)
		ref := testRefs("pairs-started", 1)[0]
		seedPinnedTitle(t, &m, ref, library.Pin{Studio: studio, Kind: voice}, []provider.Episode{
			epWithReleases(10,
				provider.Release{Studio: "kafori", Kind: voice},
				provider.Release{Studio: studio, Kind: sub},
				provider.Release{Studio: studio, Kind: voice}),
		})
		m.eng.Lib.Progress = []*library.Progress{{TitleID: ref.Slug, Episode: 10, PositionSec: 600}}
		m.showEpisodes()

		row := episodeRow(t, m, 10)
		want := i18n.KindShort(voice) + metaSep + studio + metaSep + fmt.Sprintf(i18n.TuiEpAt, 10, 0)
		if row.meta != want {
			t.Fatalf("meta початої серії = %q, want %q", row.meta, want)
		}
		if row.icon != m.ic.Play {
			t.Fatalf("іконка = %q, want %q", row.icon, m.ic.Play)
		}
	})
}

// TestEpisodesHeaderShowsPinPair — заголовок екрана серій називає закріплену
// пару цілком: wildcard-пін лишається самою студією, бо тип у ньому не
// зафіксовано.
func TestEpisodesHeaderShowsPinPair(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pin    library.Pin
		want   string
		absent string
	}{
		{
			name: "пара",
			pin:  library.Pin{Studio: "РГ", Kind: provider.KindVoiceover},
			want: fmt.Sprintf(i18n.TuiStudioPinned,
				fmt.Sprintf(i18n.TuiRelPair, "РГ", i18n.KindShort(provider.KindVoiceover))),
		},
		{
			name:   "wildcard",
			pin:    library.Pin{Studio: "РГ"},
			want:   fmt.Sprintf(i18n.TuiStudioPinned, "РГ"),
			absent: metaSep + i18n.KindShort(provider.KindVoiceover),
		},
		{
			name: "без піна",
			want: fmt.Sprintf(i18n.TuiStudioPinned, i18n.TuiStudioAuto),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("header-pair", 1)[0]
			seedPinnedTitle(t, &m, ref, tc.pin, testEpisodes(1))
			m.screen = screenEpisodes
			m.w = 80

			header := strings.SplitN(ansi.Strip(m.View().Content), "\n", 2)[0]
			if !strings.Contains(header, tc.want) {
				t.Fatalf("заголовок %q не містить %q", header, tc.want)
			}
			if tc.absent != "" && strings.Contains(header, tc.absent) {
				t.Fatalf("заголовок %q містить зайве %q", header, tc.absent)
			}
		})
	}
}
