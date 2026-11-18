package playback

import (
	"fmt"

	"github.com/basmanjacks/uaanime/internal/download"
	"github.com/basmanjacks/uaanime/internal/extractor"
	"github.com/basmanjacks/uaanime/internal/library"
	"github.com/basmanjacks/uaanime/internal/provider"
)

const localHostID = "local"

func releaseOf(s provider.Source) provider.Release {
	return provider.Release{Studio: s.Studio, Kind: s.Kind}
}

// matchingRelease — джерела цього релізу. Студія — за provider.StudioKey:
// реліз міг прийти з sidecar-а збереженого файла чи з піна, записаних іншим
// написанням, ніж зараз на сайті.
func matchingRelease(sources []provider.Source, release provider.Release) []provider.Source {
	var out []provider.Source
	for _, s := range sources {
		if provider.SameRelease(releaseOf(s), release) {
			out = append(out, s)
		}
	}
	return out
}

func savedEpisode(ref provider.TitleRef, h Hints) download.SavedTitle {
	if h.NoLocal || h.DownloadDir == "" {
		return download.SavedTitle{}
	}
	return download.Index(h.DownloadDir)[download.RefKey(ref)]
}

// preferredLocal — короткий офлайн-шлях лише для бажаного релізу. Інший
// локальний реліз не може випередити бажаний онлайн-реліз. Метадані кешу
// доповнюють вибір, але не підтверджують працездатність віддаленого хоста.
func (e *Engine) preferredLocal(ref provider.TitleRef, ep int, h Hints, title download.SavedTitle) *Resolved {
	files := title.Files[ep]
	sources := localSources(files, ep)
	if len(sources) == 0 {
		return nil
	}
	if h.Release != (provider.Release{}) {
		f, ok := fileForRelease(files, h.Release)
		if !ok {
			return nil
		}
		res := resolvedFile(ref, ep, h, title.Ref.Name, f)
		res.Pin = library.Pin{Studio: h.Release.Studio, Kind: h.Release.Kind}
		return res
	}
	known := append([]provider.Source(nil), sources...)
	memo, hasFreshEvidence := e.cachedSources(ref, ep)
	known = append(known, memo...)
	if e.Store != nil {
		episodes, fresh, found := e.Store.LoadEpisodes(ref)
		if found {
			for _, episode := range episodes {
				if episode.Number == ep {
					releases := library.SourcesFromReleases(episode)
					hasFreshEvidence = hasFreshEvidence || (fresh && len(releases) > 0)
					known = append(known, releases...)
					break
				}
			}
		}
	}
	pin := library.Pin{Studio: h.StudioPin, Kind: h.KindPin}
	chosen, candidates := library.Pick(known, pin, h.Prefs)
	if chosen == nil {
		return nil
	}
	wantKind := h.Prefs.PreferKind
	if wantKind == "" {
		wantKind = provider.KindDub
	}
	switch {
	case pin.Studio != "":
		if !provider.SameStudio(chosen.Studio, pin.Studio) || (pin.Kind != "" && chosen.Kind != pin.Kind) {
			return nil
		}
		// Wildcard без метаданих потрібного типу не доводить, що його немає онлайн.
		if pin.Kind == "" && chosen.Kind != wantKind && !hasFreshEvidence {
			return nil
		}
	case h.Prefs.FavoriteStudio != "":
		if !provider.SameStudio(chosen.Studio, h.Prefs.FavoriteStudio) || (chosen.Kind != wantKind && !hasFreshEvidence) {
			return nil
		}
	default:
		if chosen.Kind != wantKind && !hasFreshEvidence {
			return nil
		}
	}
	f, ok := fileForRelease(files, releaseOf(*chosen))
	if !ok {
		return nil
	}
	// Для першого вибору відомі онлайн-альтернативи теж мають бути в пікері.
	for _, s := range candidates {
		if _, ok := fileForRelease(files, releaseOf(s)); !ok {
			return nil
		}
	}
	res := resolvedFile(ref, ep, h, title.Ref.Name, f)
	res.Deviation = library.DeviationOf(pin, *chosen)
	res.Candidates = library.ReleaseChoices(candidates)
	res.Playable = library.ReleaseChoices(sources)
	return res
}

func resolvedFile(ref provider.TitleRef, ep int, h Hints, savedName string, f download.SavedFile) *Resolved {
	name := h.Name
	if name == "" {
		name = savedName
	}
	if name == "" {
		name = ref.Slug
	}
	stream, streams := localStream(f)
	source := provider.Source{Episode: ep, Studio: f.Studio, Kind: f.Kind}
	return &Resolved{Ref: ref, Episode: ep, Local: true, Pin: library.Pin{Studio: h.StudioPin, Kind: h.KindPin}, Prefs: h.Prefs,
		HostID: localHostID, StartSec: h.StartSec, Name: name, MediaTitle: fmt.Sprintf("%s · %d", name, ep), Source: source,
		Stream: stream, Streams: streams, Playable: []provider.Source{source}}
}

// localSources не вгадує реліз з імені: лише повні відомі пари з sidecar.
func localSources(files []download.SavedFile, ep int) []provider.Source {
	out := make([]provider.Source, 0, len(files))
	for _, f := range files {
		// Порожня студія з sidecar-а — реліз без студії на сайті, і він
		// відомий; невідомий лише файл без sidecar-а.
		if !f.Known || !provider.ValidKind(f.Kind) {
			continue
		}
		out = append(out, provider.Source{Episode: ep, Studio: f.Studio, Kind: f.Kind})
	}
	return out
}

// fileForRelease — найвища якість збереженого файла цього релізу. Sidecar
// міг записати студію іншим написанням, ніж зараз на сайті («FanVoxUА» з
// кириличною «А» проти «FanVoxUA») — звірка за provider.StudioKey.
func fileForRelease(files []download.SavedFile, release provider.Release) (download.SavedFile, bool) {
	var best download.SavedFile
	found := false
	for _, f := range files {
		if !f.Known || !provider.SameRelease(provider.Release{Studio: f.Studio, Kind: f.Kind}, release) {
			continue
		}
		if !found || f.Height > best.Height {
			best, found = f, true
		}
	}
	return best, found
}

func (e *Engine) playableWithLocal(sources []provider.Source, files []download.SavedFile) []provider.Source {
	var out []provider.Source
	for _, source := range sources {
		_, local := fileForRelease(files, releaseOf(source))
		_, remote := extractor.Find(e.Extractors, source.Embed)
		if local || remote {
			out = append(out, source)
		}
	}
	return out
}

func localStream(f download.SavedFile) (extractor.Stream, []extractor.Stream) {
	s := extractor.Stream{URL: f.Path, Quality: f.Height}
	return s, []extractor.Stream{s}
}
