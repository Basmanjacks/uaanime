package ui

import (
	"fmt"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func benchmarkRenderModel(n int) Model {
	lib := &library.Library{}
	for i := range 100 {
		id := fmt.Sprintf("title-%d", i)
		lib.Titles = append(lib.Titles, &library.LocalTitle{ID: id, Name: id, Sources: []provider.TitleRef{{Provider: "test", Slug: id}}})
	}
	for i := range n {
		lib.Progress = append(lib.Progress, &library.Progress{
			TitleID: lib.Titles[i%100].ID, Episode: i/100 + 1,
			PositionSec: 60, WatchedAt: time.Unix(int64((i*7919)%n), 0),
		})
	}
	m := Model{eng: &playback.Engine{Lib: lib, Live: &playback.Live{}}, list: list.New(nil, rowDelegate{}, 80, 24), ic: themeIcons(true)}
	m.ref = lib.Titles[99].Sources[0]
	m.episodesRef = m.ref
	for i := 1; i <= 100; i++ {
		m.episodes = append(m.episodes, provider.Episode{Number: i})
	}
	return m
}

func BenchmarkRender(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("P%d", n), func(b *testing.B) {
			for _, name := range []string{"Episodes", "Playlist", "History"} {
				b.Run(name, func(b *testing.B) {
					m := benchmarkRenderModel(n)
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						switch name {
						case "Episodes":
							_ = m.episodeRows()
						case "Playlist":
							m.publishPlaylist()
						case "History":
							m.showHistory()
						}
					}
				})
			}
		})
	}
}
