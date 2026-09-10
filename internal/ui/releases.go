package ui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

type bootstrapMsg struct{ seeds []playback.ReleaseSeed }
type episodeRequest struct {
	ref      provider.TitleRef
	req      int
	navigate bool
}
type bookmarkRequest struct {
	titleID     string
	ref         provider.TitleRef
	provisional int
}

func refKey(ref provider.TitleRef) string { return ref.Provider + "/" + ref.Slug }
func (m *Model) visibleRefs() []provider.TitleRef {
	var refs []provider.TitleRef
	seen := map[string]bool{}
	if m.eng.Lib == nil {
		return nil
	}
	for _, entry := range m.eng.Lib.Entries {
		if entry == nil || entry.Hidden {
			continue
		}
		title := m.titleByID(entry.TitleID)
		if title == nil || len(title.Sources) == 0 {
			continue
		}
		ref := title.Sources[0]
		if ref.Provider == "" || ref.Slug == "" || seen[refKey(ref)] {
			continue
		}
		seen[refKey(ref)] = true
		refs = append(refs, ref)
	}
	return refs
}
func (m *Model) prepareBootstrap() {
	m.newsDisabled = map[string]bool{}
	if m.eng.Provider == nil || m.eng.Store == nil {
		return
	}
	for _, ref := range m.visibleRefs() {
		title := m.eng.Lib.TitleByRef(ref)
		if e := m.eng.Lib.EntryLookup(title.ID); e != nil && e.ReleaseBaseline == nil {
			m.bootstrapRefs = append(m.bootstrapRefs, ref)
		}
	}
	m.bootstrapPending = len(m.bootstrapRefs) > 0
}

// The first metadata command only reads old snapshots. No cache writer starts
// until Update has committed them together against the current library.
func (m *Model) bootstrapCmd() tea.Cmd {
	if !m.bootstrapPending {
		return nil
	}
	refs := append([]provider.TitleRef{}, m.bootstrapRefs...)
	st := m.eng.Store
	return func() tea.Msg {
		var seeds []playback.ReleaseSeed
		for _, ref := range refs {
			eps, _, found := st.LoadEpisodes(ref)
			if found {
				seeds = append(seeds, playback.ReleaseSeed{Ref: ref, Episodes: eps})
			}
		}
		return bootstrapMsg{seeds}
	}
}
func (m *Model) deferEpisodeRequest(ref provider.TitleRef, req int, navigate bool) bool {
	if !m.bootstrapPending {
		return false
	}
	for _, old := range m.bootstrapRefs {
		if old.Same(ref) {
			m.deferredEpisodes = &episodeRequest{ref, req, navigate}
			return true
		}
	}
	return false
}
func (m *Model) seedReleases(seeds []playback.ReleaseSeed) {
	var eligible []playback.ReleaseSeed
	for _, seed := range seeds {
		title := m.eng.Lib.TitleByRef(seed.Ref)
		if title == nil {
			continue
		}
		entry := m.eng.Lib.EntryLookup(title.ID)
		if entry != nil && entry.ReleaseBaseline == nil && !m.newsDisabled[title.ID] {
			eligible = append(eligible, seed)
		}
	}
	if len(eligible) == 0 {
		return
	}
	if err := m.eng.InitializeReleaseBaselines(eligible); err != nil {
		m.baselineWarning = true
		for _, seed := range eligible {
			if title := m.eng.Lib.TitleByRef(seed.Ref); title != nil {
				m.newsDisabled[title.ID] = true
			}
		}
	}
}
func (m *Model) refreshLibraryLists() {
	if m.overlay != overlayNone {
		return
	}
	switch m.screen {
	case screenHome:
		m.refreshHome()
	case screenBookmarks:
		f := m.snapshot()
		m.pendingUI = m.restoreRows(f)
	}
}
