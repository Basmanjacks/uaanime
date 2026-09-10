package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestHistoryStableNewestAndLimit(t *testing.T) {
	m := newTestModel(t)
	stamp := time.Now()
	for i := range 25 {
		id := fmt.Sprintf("title-%d", i)
		m.eng.Lib.Titles = append(m.eng.Lib.Titles, &library.LocalTitle{ID: id, Sources: []provider.TitleRef{{Provider: "test", Slug: id}}})
	}
	// First occurrence of title-0 is old: ordering ties must use the index
	// of each title's newest record, not the title's first occurrence.
	m.eng.Lib.Progress = []*library.Progress{{TitleID: "title-0", Episode: 1, WatchedAt: stamp.Add(-time.Hour)}}
	for i := 1; i < 25; i++ {
		m.eng.Lib.Progress = append(m.eng.Lib.Progress, &library.Progress{TitleID: fmt.Sprintf("title-%d", i), Episode: 3, WatchedAt: stamp})
	}
	m.eng.Lib.Progress = append(m.eng.Lib.Progress,
		&library.Progress{TitleID: "title-0", Episode: 2, WatchedAt: stamp.Add(time.Minute)},
		&library.Progress{TitleID: "title-0", Episode: 9, WatchedAt: stamp.Add(time.Minute)},
	)
	original := append([]*library.Progress(nil), m.eng.Lib.Progress...)
	m.showHistory()
	items := homeItems(t, m)
	if len(items) != 21 {
		t.Fatalf("rows = %d, want 20 plus More", len(items))
	}
	for i, row := range items[:20] {
		if row.payload.(payloadResume).ref.Slug != fmt.Sprintf("title-%d", i) {
			t.Errorf("row %d = %#v", i, row.payload)
		}
	}
	if items[0].payload.(payloadResume).ep != 2 || !strings.Contains(items[0].meta, i18n.Episodes(3)) {
		t.Errorf("newest tie/count = %#v", items[0])
	}
	for i, p := range original {
		if m.eng.Lib.Progress[i] != p {
			t.Fatal("history mutated progress order")
		}
	}
	// Equal newest timestamps put title-0 behind all previously seen maxima.
	m.eng.Lib.Progress[len(original)-2].WatchedAt = stamp
	m.eng.Lib.Progress[len(original)-1].WatchedAt = stamp
	m.showHistory()
	if got := homeItems(t, m)[0].payload.(payloadResume).ref.Slug; got != "title-1" {
		t.Fatalf("stable newest tie = %s, want title-1", got)
	}
}

func TestEpisodeRowsSparseProgress(t *testing.T) {
	m := newTestModel(t)
	m.ref = provider.TitleRef{Provider: "test", Slug: "current"}
	m.episodesRef = m.ref
	m.episodes = []provider.Episode{{Number: 3}, {Number: 1}, {Number: 7}}
	m.eng.Lib.Titles = []*library.LocalTitle{{ID: "current", Sources: []provider.TitleRef{m.ref}}}
	m.eng.Lib.Progress = []*library.Progress{
		{TitleID: "other", Episode: 7, Completed: true},
		{TitleID: "current", Episode: 1, PositionSec: 65},
		{TitleID: "current", Episode: 3, Completed: true},
		{TitleID: "current", Episode: 1, Completed: true},
	}
	rows := m.episodeRows()
	if len(rows) != 3 || rows[0].badge != i18n.TuiEpDone || rows[1].meta != fmt.Sprintf(i18n.TuiEpAt, 1, 5) || rows[2].icon != m.ic.Pending {
		t.Fatalf("sparse/first-match rows = %#v", rows)
	}
}

func TestGroupHistoryNewestTie(t *testing.T) {
	stamp := time.Now()
	progress := []*library.Progress{
		{TitleID: "a", Episode: 1, WatchedAt: stamp.Add(-time.Hour)},
		{TitleID: "b", Episode: 2, WatchedAt: stamp},
		{TitleID: "a", Episode: 3, WatchedAt: stamp},
		{TitleID: "a", Episode: 4, WatchedAt: stamp},
	}
	groups := groupHistory(progress)
	if len(groups) != 2 || groups[0].newest != progress[1] || groups[1].newest != progress[2] || groups[1].count != 3 {
		t.Fatalf("groups = %#v", groups)
	}
}
