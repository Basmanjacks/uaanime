package playback

import (
	"context"

	"github.com/basmanjacks/uaanime/internal/errs"
	"github.com/basmanjacks/uaanime/internal/extractor"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// ListedRelease — реліз серії для headless `episodes`. Playable каже, чи є
// серед джерел цього релізу хоч одне, яке вміє хоч один екстрактор (без
// мережі, лише за embed). nil — невідомо: провайдер не віддає джерел усього
// тайтлу або мережа впала, а кеш записано без джерел.
type ListedRelease struct {
	Studio   string        `json:"studio"`
	Kind     provider.Kind `json:"kind"`
	Playable *bool         `json:"playable,omitempty"`
}

// ListedEpisode — серія з релізами; форма provider.Episode плюс Playable.
type ListedEpisode struct {
	Number   int             `json:"number"`
	Releases []ListedRelease `json:"releases"`
}

// EpisodeListing — серії тайтлу з ознакою відтворюваності релізів.
// fresh=false поводиться як EpisodesCached (свіжий кеш без мережі, при
// відмові мережі — кеш будь-якої давності, offline=true); fresh=true — як
// EpisodesFresh. Джерела всього тайтлу провайдер віддає одним запитом
// (provider.TitleSourcer), і вони пишуться в кеш поруч із серіями; кеш без
// джерел (старий формат) перепитується, а не вгадується. async-safe.
func (e *Engine) EpisodeListing(ctx context.Context, ref provider.TitleRef, fresh bool) (eps []ListedEpisode, offline bool, err error) {
	ts, ok := e.Provider.(provider.TitleSourcer)
	if !ok {
		var plain []provider.Episode
		if fresh {
			plain, err = e.EpisodesFresh(ctx, ref)
		} else {
			plain, offline, err = e.EpisodesCached(ctx, ref)
		}
		if err != nil {
			return nil, false, err
		}
		return e.listEpisodes(plain, nil), offline, nil
	}
	if !fresh {
		if cached, sources, isFresh, found := e.Store.LoadEpisodeSources(ref); found && isFresh && sources != nil {
			return e.listEpisodes(cached, sources), false, nil
		}
	}
	sources, err := ts.TitleSources(ctx, ref)
	if err != nil {
		if !fresh && errs.Offline(err) {
			if cached, sources, _, found := e.Store.LoadEpisodeSources(ref); found {
				return e.listEpisodes(cached, sources), true, nil
			}
		}
		return nil, false, err
	}
	plain := provider.EpisodesFromSources(sources)
	_ = e.Store.SaveEpisodeSources(ref, plain, sources)
	return e.listEpisodes(plain, sources), false, nil
}

// listEpisodes додає до серій ознаку Playable; sources == nil — невідомо.
func (e *Engine) listEpisodes(plain []provider.Episode, sources []provider.Source) []ListedEpisode {
	type key struct {
		ep      int
		release provider.Release
	}
	var playable map[key]bool
	if sources != nil {
		playable = make(map[key]bool, len(sources))
		for _, s := range sources {
			k := key{s.Episode, releaseOf(s)}
			if playable[k] {
				continue
			}
			_, ok := extractor.Find(e.Extractors, s.Embed)
			playable[k] = ok
		}
	}
	out := make([]ListedEpisode, 0, len(plain))
	for _, ep := range plain {
		listed := ListedEpisode{Number: ep.Number}
		if ep.Releases != nil { // null у JSON лишається null, як у provider.Episode
			listed.Releases = make([]ListedRelease, 0, len(ep.Releases))
		}
		for _, r := range ep.Releases {
			lr := ListedRelease{Studio: r.Studio, Kind: r.Kind}
			if playable != nil {
				ok := playable[key{ep.Number, r}]
				lr.Playable = &ok
			}
			listed.Releases = append(listed.Releases, lr)
		}
		out = append(out, listed)
	}
	return out
}
