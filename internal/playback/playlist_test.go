package playback

import (
	"reflect"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestBuildEpisodeInfoSparseOrderedSnapshot(t *testing.T) {
	episodes := []provider.Episode{{Number: 7}, {Number: 3}, {Number: 1}}
	progress := []*library.Progress{{TitleID: "other", Episode: 7, Completed: true}, {TitleID: "title", Episode: 3, Completed: true, PositionSec: 120}, {TitleID: "title", Episode: 1, PositionSec: 60}}
	got := BuildEpisodeInfo(episodes, "title", progress, 1)
	want := []EpisodeInfo{{Number: 7}, {Number: 3, Watched: true, PositionSec: 120}, {Number: 1, PositionSec: 60, Current: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v, want %#v", got, want)
	}
	progress[1].PositionSec = 999
	if !reflect.DeepEqual(got, want) {
		t.Fatal("playlist aliases progress")
	}
	got = BuildEpisodeInfo(episodes, "", progress, 0)
	if !reflect.DeepEqual(got, []EpisodeInfo{{Number: 7}, {Number: 3}, {Number: 1}}) {
		t.Fatalf("missing title = %#v", got)
	}
}
