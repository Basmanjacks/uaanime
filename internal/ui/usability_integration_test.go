package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestFullBookmarksSeparatesLateStudioReleasesFromNewEpisodes(t *testing.T) {
	m := newTestModel(t)
	ref := provider.TitleRef{Provider: "test", Slug: "1-late", Name: "Late releases"}
	title := m.eng.Lib.EnsureTitle(ref, func() string { return "late" })
	entry := m.eng.Lib.EntryFor(title.ID)
	entry.StudioPin = "A"
	entry.KnownEpisodes = 3
	old := []provider.Episode{{Number: 1, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}, {Number: 2, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}, {Number: 3, Releases: []provider.Release{{Studio: "B", Kind: provider.KindDub}}}}
	m.eng.Lib.SeedReleaseBaseline(title.ID, old)
	current := []provider.Episode{{Number: 1, Releases: []provider.Release{{Studio: "A", Kind: provider.KindDub}}}, {Number: 2, Releases: []provider.Release{{Studio: "A", Kind: provider.KindDub}}}, {Number: 3, Releases: old[2].Releases}, {Number: 4, Releases: old[2].Releases}}
	if err := m.eng.Store.SaveEpisodes(ref, current); err != nil {
		t.Fatal(err)
	}
	m.showBookmarks()
	rows := m.bookmarkRows()
	if len(rows) != 1 {
		t.Fatalf("rows=%d", len(rows))
	}
	if want := fmt.Sprintf(i18n.TuiStudioNews, 2, "A"); rows[0].badge != want {
		t.Fatalf("badge=%q want%q", rows[0].badge, want)
	}
	if !strings.Contains(rows[0].meta, i18n.NewEpisodes(1)) {
		t.Fatalf("missing independent generic count: %s", rows[0].meta)
	}
}

func TestHelpReturnPreservesPendingStatus(t *testing.T) {
	m := newTestModel(t)
	m.status = "loading sentinel"
	m.statusKind = statusLoading
	m.statusGen = 42
	pending := m.snapshot()
	m.pending = &pending
	m.pendingReq = 7
	m.reqID = 7
	m.openOverlay(overlayHelp)
	_ = m.closeOverlay()
	if m.status != "loading sentinel" || m.statusKind != statusLoading || m.statusGen != 42 {
		t.Fatalf("help altered pending status: %q %v %d", m.status, m.statusKind, m.statusGen)
	}
	if m.pending == nil || m.reqID != 7 {
		t.Fatal("help altered pending request")
	}
}

func BenchmarkFullBookmarksWithStudioBaselines(b *testing.B) {
	lib := &library.Library{}
	m := Model{eng: &playback.Engine{Lib: lib}, screen: screenBookmarks, epsScratch: map[string][]provider.Episode{}}
	for i := range 50 {
		id := fmt.Sprint(i)
		ref := provider.TitleRef{Provider: "test", Slug: "1-" + id, Name: "Title " + id}
		lib.EnsureTitle(ref, func() string { return id })
		entry := lib.EntryFor(id)
		entry.StudioPin = "Studio0"
		entry.KnownEpisodes = 1500
		var eps []provider.Episode
		for n := range 1500 {
			eps = append(eps, provider.Episode{Number: n, Releases: []provider.Release{{Studio: fmt.Sprintf("Studio%d", n%6), Kind: provider.KindDub}}})
		}
		lib.SeedReleaseBaseline(id, eps[:1000])
		m.epsScratch[id] = eps
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = m.bookmarkRows()
	}
}
