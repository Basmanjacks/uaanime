package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestReviewSearchPaste(t *testing.T) {
	m := openTestSearch(t, newTestModel(t))
	m, _ = updateTestModel(t, m, tea.PasteMsg{Content: "Фрірен"})
	if got := m.input.Value(); got != "Фрірен" {
		t.Fatalf("paste lost: %q", got)
	}
}

func TestReviewSyncNavigationInvalidatesPending(t *testing.T) {
	for _, destination := range []string{"search", "history"} {
		t.Run(destination, func(t *testing.T) {
			m := newTestModel(t)
			ref := testRefs("pending", 1)[0]
			updated, _ := m.openTitle(ref)
			m = updated.(Model)
			old := m.reqID
			if destination == "search" {
				updated, _ = m.openSearch()
			} else {
				m.setItems([]item{{title: "history", payload: payloadHistory{}}}, 0)
				updated, _ = m.openSelected()
			}
			m = updated.(Model)
			want := m.screen
			m, _ = updateTestModel(t, m, episodesDoneMsg{req: old, ref: ref, eps: testEpisodes(2), navigate: true})
			if m.screen != want || m.pending != nil {
				t.Fatalf("old navigation hijacked screen: %v, pending=%v", m.screen, m.pending)
			}
		})
	}
}

func TestReviewEpisodeRowsRejectOtherTitle(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("title", 2)
	m.ref, m.episodesRef, m.episodes = refs[1], refs[0], testEpisodes(3)
	if rows := m.episodeRows(); len(rows) != 0 {
		t.Fatalf("displayed %d rows from another title", len(rows))
	}
	if err := m.eng.Store.SaveEpisodes(refs[1], testEpisodes(1)); err != nil {
		t.Fatal(err)
	}
	if rows := m.episodeRows(); len(rows) != 1 {
		t.Fatalf("cached title rows=%d", len(rows))
	}
}

func TestReviewAutoplayRejectsOtherTitle(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("title", 2)
	m.ref, m.episodesRef, m.episodes = refs[1], refs[0], testEpisodes(3)
	m.pendingEp, m.screen, m.eng.Autoplay = 1, screenPlaying, true
	m, _ = updateTestModel(t, m, playDoneMsg{reason: player.EndEOF})
	if m.pendingEp != 1 {
		t.Fatalf("guessed next episode from old title: %d", m.pendingEp)
	}
	if m.screen == screenEpisodes && len(m.list.Items()) > 0 {
		t.Fatal("returned to another title's episodes")
	}
}

func TestReviewLateEpisodesRefreshWithoutNavigation(t *testing.T) {
	for _, screen := range []screen{screenEpisodes, screenHome} {
		t.Run(string(rune('0'+screen)), func(t *testing.T) {
			m := newTestModel(t)
			m.ref = testRefs("title", 1)[0]
			m.episodesRef, m.episodes = m.ref, testEpisodes(1)
			m.showEpisodes()
			if screen == screenHome {
				m.showHome()
			}
			count := len(m.list.Items())
			m, _ = updateTestModel(t, m, episodesDoneMsg{req: m.reqID, ref: m.ref, eps: []provider.Episode{{Number: 1}, {Number: 2}}})
			if m.screen != screen {
				t.Fatalf("late metadata navigated to %v", m.screen)
			}
			if screen == screenEpisodes && len(m.list.Items()) != 2 {
				t.Fatal("late episodes did not refresh list")
			}
			if screen == screenHome && len(m.list.Items()) != count {
				t.Fatal("late episodes changed home rows")
			}
		})
	}
}

func TestReviewSelectedVoiceoverSurvivesResolve(t *testing.T) {
	m := newTestModel(t)
	m.ref = testRefs("voiceover", 1)[0]
	sources := []provider.Source{
		{Studio: "A", Kind: provider.KindDub, Embed: "https://test.invalid/dub"},
		{Studio: "A", Kind: provider.KindVoiceover, Embed: "https://test.invalid/voice"},
		{Studio: "B", Kind: provider.KindVoiceover, Embed: "https://test.invalid/b"},
	}
	m.eng.Provider = sourcesStub(sources)
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	m.pendingEp = 1
	m.showStudioChoice(sources[1:])
	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	if cmd == nil {
		t.Fatal("selection did not resolve")
	}
	res := cmd().(resolvedMsg)
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.res.Source.Studio != "A" || res.res.Source.Kind != provider.KindVoiceover {
		t.Fatalf("selection changed: %+v", res.res.Source)
	}
}

func TestReviewBookmarkUsesOwnedEpisodes(t *testing.T) {
	m := newTestModel(t)
	refs := testRefs("bookmark", 2)
	m.ref, m.episodesRef, m.episodes = refs[1], refs[0], testEpisodes(12)
	m.screen = screenEpisodes
	updated, _ := m.bookmarkSelected()
	m = updated.(Model)
	title := m.eng.Lib.TitleByRef(refs[1])
	if got := m.eng.Lib.EntryLookup(title.ID).KnownEpisodes; got != 0 {
		t.Fatalf("saved other title's baseline: %d", got)
	}
}
