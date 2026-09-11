package ui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/charmbracelet/x/ansi"
)

func TestStudioKeyOpensChoicesAndMarksPinnedStudio(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("studio-key", 1)[0]
	choices := []provider.Source{
		{Studio: "Alpha", Kind: provider.KindDub, Episode: 1, Embed: "https://video.invalid/alpha"},
		{Studio: "Beta", Kind: provider.KindVoiceover, Episode: 1, Embed: "https://video.invalid/beta"},
	}
	m.eng.Provider = sourcesStub(choices)
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
	m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: "Beta"}}
	m.ref = ref
	m.episodesRef = ref
	m.episodes = testEpisodes(1)
	m.showEpisodes()

	m, cmd := pressTestKey(t, m, 's', "s")
	if cmd == nil {
		t.Fatal("studio key returned no command")
	}
	if m.pendingEp != 1 || m.status != i18n.TuiResolving {
		t.Fatalf("studio request state = (episode %d, status %q)", m.pendingEp, m.status)
	}
	msg, ok := cmd().(studiosMsg)
	if !ok {
		t.Fatalf("studio command message = %T, want studiosMsg", msg)
	}
	m, _ = updateTestModel(t, m, msg)

	if m.screen != screenStudio {
		t.Fatalf("screen = %d, want studio %d", m.screen, screenStudio)
	}
	for _, listItem := range m.list.Items() {
		it := listItem.(item)
		src := it.payload.(payloadStudio).src
		if it.meta != i18n.KindShort(src.Kind) {
			t.Errorf("studio %q meta = %q, want %q", src.Studio, it.meta, i18n.KindShort(src.Kind))
		}
		if src.Studio == "Beta" {
			if it.icon != m.ic.Done || !it.iconAccent {
				t.Errorf("pinned studio marker = (%q, %t), want (%q, true)", it.icon, it.iconAccent, m.ic.Done)
			}
		} else if it.icon != "" || it.iconAccent {
			t.Errorf("unpinned studio marker = (%q, %t), want default", it.icon, it.iconAccent)
		}
	}
}

// coverageEpisodes — total серій, у яких студія покриває перші counts[studio].
func coverageEpisodes(total int, counts map[string]int) []provider.Episode {
	eps := make([]provider.Episode, total)
	for i := range eps {
		eps[i] = provider.Episode{Number: i + 1}
		for _, studio := range slices.Sorted(maps.Keys(counts)) {
			if i < counts[studio] {
				eps[i].Releases = append(eps[i].Releases,
					provider.Release{Studio: studio, Kind: provider.KindDub})
			}
		}
	}
	return eps
}

func studioMeta(t *testing.T, m Model, studio string) string {
	t.Helper()

	for _, listItem := range m.list.Items() {
		it, ok := listItem.(item)
		if ok && it.title == studio {
			return it.meta
		}
	}
	t.Fatalf("studio row %q not found", studio)
	return ""
}

// TestStudioCoverage — вибір озвучки показує, скільки серій має студія: різниця
// між «є всі» і «є три з дванадцяти» вирішує вибір.
func TestStudioCoverage(t *testing.T) {
	ref := testRefs("coverage", 1)[0]
	candidates := []provider.Source{
		{Studio: "Alpha", Kind: provider.KindDub, Episode: 1},
		{Studio: "Beta", Kind: provider.KindDub, Episode: 1},
	}
	episodes := coverageEpisodes(3, map[string]int{"Alpha": 3, "Beta": 1})
	dub := i18n.KindShort(provider.KindDub)

	t.Run("from the model", func(t *testing.T) {
		m := newTestModel(t)
		m.ref, m.episodes, m.episodesRef = ref, episodes, ref
		m.showStudioChoice(candidates, nil, nil)

		if got, want := studioMeta(t, m, "Alpha"), dub+metaSep+"3/3"; got != want {
			t.Errorf("Alpha meta = %q, want %q", got, want)
		}
		if got, want := studioMeta(t, m, "Beta"), dub+metaSep+"1/3"; got != want {
			t.Errorf("Beta meta = %q, want %q", got, want)
		}
	})

	t.Run("from the disk cache", func(t *testing.T) {
		m := newTestModel(t)
		if err := m.eng.Store.SaveEpisodes(ref, episodes); err != nil {
			t.Fatalf("save episodes: %v", err)
		}
		m.ref = ref
		m.showStudioChoice(candidates, nil, nil)

		if got, want := studioMeta(t, m, "Alpha"), dub+metaSep+"3/3"; got != want {
			t.Errorf("Alpha meta = %q, want %q", got, want)
		}
	})

	t.Run("without any episode list", func(t *testing.T) {
		m := newTestModel(t)
		m.ref = ref
		m.showStudioChoice(candidates, nil, nil)

		if got := studioMeta(t, m, "Alpha"); got != dub {
			t.Errorf("Alpha meta without episodes = %q, want %q", got, dub)
		}
	})
}

// TestStudioCoverageIgnoresPreviousTitle — «Продовжити» шле resolve і episodes
// паралельно, тож вибір озвучки може відкритися, поки в моделі ще лежать серії
// попереднього тайтлу. Показати їх покриття означало б збрехати.
func TestStudioCoverageIgnoresPreviousTitle(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("coverage-nav", 2)
	previous, current := refs[0], refs[1]
	seedTestLibrary(&m, refs)

	m.ref = previous
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:     m.reqID,
		ref:     previous,
		eps:     coverageEpisodes(3, map[string]int{"Alpha": 3}),
		purpose: epsOpen,
	})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after episodes = %d, want episodes %d", m.screen, screenEpisodes)
	}

	m.showHome()
	selectTestItem(t, &m, func(it item) bool {
		p, ok := it.payload.(payloadResume)
		return ok && p.ref == current
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.ref != current {
		t.Fatalf("ref after continue = %+v, want %+v", m.ref, current)
	}

	// resolvedMsg випередив episodesDoneMsg нового тайтлу.
	candidates := []provider.Source{
		{Studio: "Alpha", Kind: provider.KindDub, Episode: 1},
		{Studio: "Beta", Kind: provider.KindDub, Episode: 1},
	}
	m, _ = updateTestModel(t, m, resolvedMsg{req: m.reqID, res: &playback.Resolved{
		Ref:        current,
		Episode:    1,
		Source:     candidates[0],
		Candidates: candidates,
		Playable:   candidates,
	}})
	if m.screen != screenStudio {
		t.Fatalf("screen = %d, want studio %d", m.screen, screenStudio)
	}
	if got := studioMeta(t, m, "Alpha"); got != i18n.KindShort(provider.KindDub) {
		t.Errorf("Alpha meta = %q, want no coverage from the previous title", got)
	}
}

func TestStudiosErrorLeavesNavigationUnchanged(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenEpisodes
	m.stack = []frame{{screen: screenHome}}
	m.reqID = 4
	m.status = i18n.TuiResolving

	m, _ = updateTestModel(t, m, studiosMsg{req: 4, err: errs.ErrNoStream})

	if m.screen != screenEpisodes || len(m.stack) != 1 || m.stack[0].screen != screenHome {
		t.Fatalf("navigation changed after studio error: screen=%d stack=%+v", m.screen, m.stack)
	}
	if m.errText != i18n.ErrorText(errs.ErrNoStream) {
		t.Fatalf("error text = %q, want %q", m.errText, i18n.ErrorText(errs.ErrNoStream))
	}
}

func TestStaleStudiosMsgIgnored(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenEpisodes
	m.stack = []frame{{screen: screenHome}}
	m.reqID = 9
	m.status = i18n.TuiResolving

	m, _ = updateTestModel(t, m, studiosMsg{
		req:     8,
		choices: []provider.Source{{Studio: "Stale", Kind: provider.KindDub}},
	})

	if m.screen != screenEpisodes || len(m.stack) != 1 || m.status != i18n.TuiResolving {
		t.Fatalf("stale studios message changed model: screen=%d stack=%d status=%q", m.screen, len(m.stack), m.status)
	}
}

func TestResolvedPinFallbackShowsStudioStatus(t *testing.T) {
	m := newTestModel(t)
	ref := testRefs("pin-fallback", 1)[0]
	m.eng.Lib.Titles = []*library.LocalTitle{{ID: ref.Slug, Name: ref.Name, Sources: []provider.TitleRef{ref}}}
	m.eng.Lib.Entries = []*library.Entry{{TitleID: ref.Slug, StudioPin: "Pinned"}}
	m.ref = ref
	m.screen = screenEpisodes
	m.reqID = 3

	m, _ = updateTestModel(t, m, resolvedMsg{req: 3, res: &playback.Resolved{
		Ref:       ref,
		Episode:   1,
		Source:    provider.Source{Studio: "Fallback", Kind: provider.KindDub},
		Pin:       library.Pin{Studio: "Pinned"},
		Deviation: library.DeviationStudio,
	}})

	want := fmt.Sprintf(i18n.TuiStudioFallback, "Pinned", "Fallback")
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, want) {
		t.Fatalf("playing view does not contain fallback status %q: %q", want, view)
	}
}

func TestStudioTransient(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, req := launchTestSearch(t, m, "studio")
	cards := testCards("studio", 1)
	ref := cards[0].TitleRef
	m, _ = updateTestModel(t, m, searchDoneMsg{req: req, page: 1, cards: cards})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:     req,
		ref:     ref,
		eps:     testEpisodes(2),
		purpose: epsOpen,
	})
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	candidates := []provider.Source{
		{Studio: "Alpha", Kind: provider.KindDub, Episode: 1},
		{Studio: "Beta", Kind: provider.KindVoiceover, Episode: 1},
	}
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:        ref,
		Episode:    1,
		Source:     candidates[0],
		Candidates: candidates,
	}})
	if m.screen != screenStudio {
		t.Fatalf("screen after multiple candidates = %d, want %d", m.screen, screenStudio)
	}

	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	req = m.reqID
	m, _ = updateTestModel(t, m, resolvedMsg{req: req, res: &playback.Resolved{
		Ref:     ref,
		Episode: 1,
		Source:  candidates[0],
	}})
	if m.screen != screenPlaying {
		t.Fatalf("screen after studio choice resolved = %d, want %d", m.screen, screenPlaying)
	}

	m, _ = updateTestModel(t, m, playDoneMsg{})
	if m.screen != screenEpisodes {
		t.Fatalf("screen after playback = %d, want %d", m.screen, screenEpisodes)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenSearch {
		t.Fatalf("screen after leaving episodes = %d, want search %d", m.screen, screenSearch)
	}
}

// pairCoverageEpisodes — 11 серій, у яких пари покривають різні відрізки:
// РГ озвучує 1–10 і субтитрує 1–11, kafori озвучує 1–10, inariokami — 1–8.
// Саме різниця «11/11 сабів проти 10/11 озвучення» і є сенсом пар у пікері.
func pairCoverageEpisodes() []provider.Episode {
	eps := make([]provider.Episode, 11)
	for i := range eps {
		number := i + 1
		eps[i] = provider.Episode{Number: number}
		eps[i].Releases = append(eps[i].Releases, provider.Release{Studio: "РГ", Kind: provider.KindSub})
		if number <= 10 {
			eps[i].Releases = append(eps[i].Releases,
				provider.Release{Studio: "РГ", Kind: provider.KindVoiceover},
				provider.Release{Studio: "kafori", Kind: provider.KindVoiceover})
		}
		if number <= 8 {
			eps[i].Releases = append(eps[i].Releases,
				provider.Release{Studio: "inariokami", Kind: provider.KindVoiceover})
		}
	}
	return eps
}

// studioRows — рядки пікера як зрізи значень, у порядку списку.
func studioRows(t *testing.T, m Model) []item {
	t.Helper()

	rows := make([]item, 0, len(m.list.Items()))
	for _, listItem := range m.list.Items() {
		it, ok := listItem.(item)
		if !ok {
			t.Fatalf("рядок пікера %T не ui.item", listItem)
		}
		rows = append(rows, it)
	}
	return rows
}

// pairSource — джерело пари для playable-списку.
func pairSource(studio string, kind provider.Kind, ep int) provider.Source {
	return provider.Source{Studio: studio, Kind: kind, Episode: ep}
}

// TestStudioChoiceListsPairsWithCoverageAndMarkers — пікер показує пари
// (студія, тип) з чесним покриттям і каже, що з них гратиме, а чого в цій
// серії просто немає.
func TestStudioChoiceListsPairsWithCoverageAndMarkers(t *testing.T) {
	voice, sub := provider.KindVoiceover, provider.KindSub
	pinned := library.Pin{Studio: "РГ", Kind: voice}

	// seed — модель із тайтлом, піном і списком серій пар.
	seed := func(t *testing.T, pin library.Pin, episodes []provider.Episode, ep int) Model {
		t.Helper()

		m := newTestModel(t)
		ref := testRefs("pair-picker", 1)[0]
		seedPinnedTitle(t, &m, ref, pin, episodes)
		m.pendingEp = ep
		return m
	}

	t.Run("пара піна поза серією", func(t *testing.T) {
		m := seed(t, pinned, pairCoverageEpisodes(), 11)
		playable := []provider.Source{pairSource("РГ", sub, 11)}
		m.showStudioChoice(playable, nil, &playable[0])

		rows := studioRows(t, m)
		if len(rows) != 4 {
			t.Fatalf("рядків пікера = %d, want 4: %+v", len(rows), rows)
		}
		for i, want := range []struct {
			studio    string
			meta      string
			icon      string
			accent    bool
			badge     string
			badgeWarn bool
		}{
			{
				studio: "РГ", meta: i18n.KindShort(voice) + metaSep + "10/11",
				icon: m.ic.Done, accent: true, badge: i18n.TuiPickNotInEpisode, badgeWarn: true,
			},
			{
				studio: "РГ", meta: i18n.KindShort(sub) + metaSep + "11/11",
				icon: m.ic.Play, accent: true, badge: i18n.TuiPickWillPlay,
			},
			{
				studio: "kafori", meta: i18n.KindShort(voice) + metaSep + "10/11",
				badge: i18n.TuiPickNotInEpisode, badgeWarn: true,
			},
			{
				studio: "inariokami", meta: i18n.KindShort(voice) + metaSep + "8/11",
				badge: i18n.TuiPickNotInEpisode, badgeWarn: true,
			},
		} {
			row := rows[i]
			if row.title != want.studio || row.meta != want.meta {
				t.Errorf("рядок %d = (%q, %q), want (%q, %q)", i, row.title, row.meta, want.studio, want.meta)
			}
			if row.icon != want.icon || row.iconAccent != want.accent {
				t.Errorf("рядок %d іконка = (%q, %t), want (%q, %t)", i, row.icon, row.iconAccent, want.icon, want.accent)
			}
			if row.badge != want.badge || row.badgeWarn != want.badgeWarn {
				t.Errorf("рядок %d бейдж = (%q, %t), want (%q, %t)", i, row.badge, row.badgeWarn, want.badge, want.badgeWarn)
			}
		}
	})

	t.Run("пін і гратиме збігаються", func(t *testing.T) {
		m := seed(t, pinned, pairCoverageEpisodes(), 10)
		playable := []provider.Source{
			pairSource("РГ", voice, 10),
			pairSource("РГ", sub, 10),
			pairSource("kafori", voice, 10),
			pairSource("inariokami", voice, 10),
		}
		m.showStudioChoice(playable, nil, &playable[0])

		rows := studioRows(t, m)
		if len(rows) != 4 {
			t.Fatalf("рядків пікера = %d, want 4", len(rows))
		}
		// ✓ піна сильніший за ▶: людина шукає очима саме закріплену пару.
		if rows[0].title != "РГ" || rows[0].meta != i18n.KindShort(voice)+metaSep+"10/11" {
			t.Fatalf("перший рядок = (%q, %q), want закріплену пару РГ·Озв", rows[0].title, rows[0].meta)
		}
		if rows[0].icon != m.ic.Done || !rows[0].iconAccent {
			t.Errorf("іконка закріпленої пари = (%q, %t), want (%q, true)", rows[0].icon, rows[0].iconAccent, m.ic.Done)
		}
		if rows[0].badge != i18n.TuiPickWillPlay || rows[0].badgeWarn {
			t.Errorf("бейдж закріпленої пари = (%q, %t), want (%q, false)", rows[0].badge, rows[0].badgeWarn, i18n.TuiPickWillPlay)
		}
		for i, row := range rows {
			if row.badgeWarn {
				t.Errorf("рядок %d (%q · %q) попереджає, хоча всі пари відтворювані", i, row.title, row.meta)
			}
		}
	})

	t.Run("wildcard-пін позначає озвучення", func(t *testing.T) {
		m := seed(t, library.Pin{Studio: "РГ"}, pairCoverageEpisodes(), 10)
		playable := []provider.Source{
			pairSource("РГ", voice, 10),
			pairSource("РГ", sub, 10),
			pairSource("kafori", voice, 10),
			pairSource("inariokami", voice, 10),
		}
		m.showStudioChoice(playable, nil, &playable[0])

		for _, row := range studioRows(t, m) {
			if row.title != "РГ" {
				continue
			}
			pinnedIcon := row.icon == m.ic.Done
			if got := row.meta == i18n.KindShort(voice)+metaSep+"10/11"; got != pinnedIcon {
				t.Errorf("✓ на рядку (%q · %q) = %t, want лише на озвученні", row.title, row.meta, pinnedIcon)
			}
		}
	})

	t.Run("пара без екстрактора не пінується", func(t *testing.T) {
		episodes := pairCoverageEpisodes()
		episodes[9].Releases = append(episodes[9].Releases,
			provider.Release{Studio: "X", Kind: provider.KindDub})
		m := seed(t, pinned, episodes, 10)
		playable := []provider.Source{
			pairSource("РГ", voice, 10),
			pairSource("РГ", sub, 10),
			pairSource("kafori", voice, 10),
			pairSource("inariokami", voice, 10),
		}
		m.showStudioChoice(playable, nil, &playable[0])

		var row item
		selectTestItem(t, &m, func(it item) bool {
			if it.title != "X" {
				return false
			}
			row = it
			return true
		})
		if row.badge != i18n.TuiPickUnplayable || !row.badgeWarn {
			t.Fatalf("бейдж пари без екстрактора = (%q, %t), want (%q, true)", row.badge, row.badgeWarn, i18n.TuiPickUnplayable)
		}
		if p, ok := row.payload.(payloadStudio); !ok || !p.unplayable {
			t.Fatalf("payload = %+v, want payloadStudio{unplayable: true}", row.payload)
		}

		m, _ = pressTestKey(t, m, tea.KeyEnter, "")
		if m.status != i18n.TuiPickUnplayable {
			t.Fatalf("статус після Enter = %q, want %q", m.status, i18n.TuiPickUnplayable)
		}
		entry := m.eng.Lib.EntryLookup(m.ref.Slug)
		if entry == nil || entry.StudioPin != "РГ" || entry.KindPin != voice {
			t.Fatalf("пін після Enter = %+v, want незмінений (РГ, %s)", entry, voice)
		}
	})
}

// TestForcedPickerSubsOnlyPinsWildcard — вимушене питання на серії, де є лише
// субтитри, не є свідомим вибором сабів: пін лишається wildcard, і озвучення
// ввімкнеться, щойно з'явиться. Явний sub-пін — тільки коли був вибір типу.
func TestForcedPickerSubsOnlyPinsWildcard(t *testing.T) {
	ref := testRefs("forced-subs", 1)[0]
	subsOnly := []provider.Episode{{Number: 1, Releases: []provider.Release{
		{Studio: "AniUA", Kind: provider.KindSub}, {Studio: "SubUA", Kind: provider.KindSub},
	}}}
	m := newTestModel(t)
	seedPinnedTitle(t, &m, ref, library.Pin{}, subsOnly)
	m.eng.Provider = sourcesStub([]provider.Source{
		{Studio: "AniUA", Kind: provider.KindSub, Episode: 1, Embed: "https://video.invalid/a"},
		{Studio: "SubUA", Kind: provider.KindSub, Episode: 1, Embed: "https://video.invalid/b"},
	})
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	m.pendingEp = 1
	playable := []provider.Source{pairSource("AniUA", provider.KindSub, 1), pairSource("SubUA", provider.KindSub, 1)}
	m.showStudioChoice(playable, nil, &playable[0])

	for _, it := range studioRows(t, m) {
		if p := it.payload.(payloadStudio); p.pinKind != "" {
			t.Fatalf("рядок %s: pinKind = %q, want wildcard (вибору типу не було)", it.title, p.pinKind)
		}
	}
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	entry := m.eng.Lib.EntryLookup(ref.Slug)
	if entry == nil || entry.StudioPin != "AniUA" || entry.KindPin != "" {
		t.Fatalf("пін після Enter = %+v, want AniUA/wildcard", entry)
	}

	// А коли поряд є озвучення — вибір сабів явний.
	both := []provider.Source{pairSource("AniUA", provider.KindSub, 1), pairSource("DZUSKA", provider.KindDub, 1)}
	m.showStudioChoice(both, nil, &both[1])
	for _, it := range studioRows(t, m) {
		p := it.payload.(payloadStudio)
		if p.src.Kind == provider.KindSub && p.pinKind != provider.KindSub {
			t.Fatalf("рядок %s: pinKind = %q, want sub (був вибір)", it.title, p.pinKind)
		}
	}
}

// TestPickerFailedHostIsNotUnplayable — пара, чий хост щойно не відповів, має
// екстрактор: бейдж каже «хост не відповів», а Enter закріплює її як звичайну.
func TestPickerFailedHostIsNotUnplayable(t *testing.T) {
	ref := testRefs("failed-host", 1)[0]
	eps := []provider.Episode{{Number: 1, Releases: []provider.Release{
		{Studio: "AniUA", Kind: provider.KindDub}, {Studio: "SubUA", Kind: provider.KindSub},
	}}}
	m := newTestModel(t)
	seedPinnedTitle(t, &m, ref, library.Pin{}, eps)
	m.eng.Provider = sourcesStub([]provider.Source{
		{Studio: "AniUA", Kind: provider.KindDub, Episode: 1, Embed: "https://video.invalid/a"},
		{Studio: "SubUA", Kind: provider.KindSub, Episode: 1, Embed: "https://video.invalid/b"},
	})
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	m.pendingEp = 1
	playable := []provider.Source{pairSource("AniUA", provider.KindDub, 1), pairSource("SubUA", provider.KindSub, 1)}
	m.showStudioChoice(playable, playable[:1], &playable[1])

	rows := studioRows(t, m)
	var aniUA item
	for _, it := range rows {
		if it.title == "AniUA" {
			aniUA = it
		}
	}
	if aniUA.badge != i18n.TuiPickFailed || !aniUA.badgeWarn || aniUA.payload.(payloadStudio).unplayable {
		t.Fatalf("рядок AniUA = %+v, want бейдж «хост не відповів» без блокування", aniUA)
	}
	selectTestItem(t, &m, func(it item) bool { return it.title == "AniUA" })
	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("Enter на парі, що впала, має пінувати й ре-резолвити")
	}
	if entry := m.eng.Lib.EntryLookup(ref.Slug); entry == nil || entry.StudioPin != "AniUA" || entry.KindPin != provider.KindDub {
		t.Fatalf("пін = %+v, want AniUA/dub", entry)
	}
}

// TestPinnedPairWildcardRespectsVeto — wildcard-пін студії, у якої в серії лише
// саби, тоді як інша студія має дубляж: ✓ не може стояти на сабах, які Pick
// ніколи не обере, — воно йде на найкраще озвучення студії піна з інших серій.
func TestPinnedPairWildcardRespectsVeto(t *testing.T) {
	ref := testRefs("wildcard-veto", 1)[0]
	eps := []provider.Episode{
		{Number: 1, Releases: []provider.Release{{Studio: "AniUA", Kind: provider.KindVoiceover}}},
		{Number: 2, Releases: []provider.Release{{Studio: "AniUA", Kind: provider.KindSub}, {Studio: "DZUSKA", Kind: provider.KindDub}}},
	}
	m := newTestModel(t)
	seedPinnedTitle(t, &m, ref, library.Pin{Studio: "AniUA"}, eps)
	m.pendingEp = 2
	playable := []provider.Source{pairSource("AniUA", provider.KindSub, 2), pairSource("DZUSKA", provider.KindDub, 2)}
	m.showStudioChoice(playable, nil, &playable[1])

	for _, it := range studioRows(t, m) {
		p := it.payload.(payloadStudio)
		pinned := it.icon == m.ic.Done
		wantPinned := p.src.Studio == "AniUA" && p.src.Kind == provider.KindVoiceover
		if pinned != wantPinned {
			t.Errorf("рядок %s · %s: ✓ = %v, want %v", p.src.Studio, p.src.Kind, pinned, wantPinned)
		}
	}
}
