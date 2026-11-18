package playback

import (
	"github.com/basmanjacks/uaanime/internal/library"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// BuildEpisodeInfo snapshots progress on the library owner's goroutine; the
// resulting playlist contains only values and is safe to publish to Live.
func BuildEpisodeInfo(episodes []provider.Episode, titleID string, progress []*library.Progress, current int) []EpisodeInfo {
	index := library.IndexProgress(titleID, progress)
	rows := make([]EpisodeInfo, 0, len(episodes))
	for _, ep := range episodes {
		row := EpisodeInfo{Number: ep.Number, Current: ep.Number == current}
		if p := index[ep.Number]; p != nil {
			row.Watched, row.PositionSec = p.Completed, p.PositionSec
		}
		rows = append(rows, row)
	}
	return rows
}
