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
		if it.meta != i18n.KindLabel(src.Kind) {
			t.Errorf("studio %q meta = %q, want %q", src.Studio, it.meta, i18n.KindLabel(src.Kind))
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
	dub := i18n.KindLabel(provider.KindDub)

	t.Run("from the model", func(t *testing.T) {
		m := newTestModel(t)
		m.ref, m.episodes, m.episodesRef = ref, episodes, ref
		m.showStudioChoice(candidates)

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
		m.showStudioChoice(candidates)

		if got, want := studioMeta(t, m, "Alpha"), dub+metaSep+"3/3"; got != want {
			t.Errorf("Alpha meta = %q, want %q", got, want)
		}
	})

	t.Run("without any episode list", func(t *testing.T) {
		m := newTestModel(t)
		m.ref = ref
		m.showStudioChoice(candidates)

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
	seedTestLibrary(&m, refs, library.StateWatching)

	m.ref = previous
	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      m.reqID,
		ref:      previous,
		eps:      coverageEpisodes(3, map[string]int{"Alpha": 3}),
		navigate: true,
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
	}})
	if m.screen != screenStudio {
		t.Fatalf("screen = %d, want studio %d", m.screen, screenStudio)
	}
	if got := studioMeta(t, m, "Alpha"); got != i18n.KindLabel(provider.KindDub) {
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
		Ref:         ref,
		Episode:     1,
		Source:      provider.Source{Studio: "Fallback", Kind: provider.KindDub},
		PinFallback: true,
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
		req:      req,
		ref:      ref,
		eps:      testEpisodes(2),
		navigate: true,
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
