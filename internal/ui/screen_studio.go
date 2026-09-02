package ui

import (
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func (m *Model) showStudioChoice(candidates []provider.Source) {
	m.setScreen(screenStudio)
	pin := m.studioPin()
	seen := map[string]bool{}
	var items []item
	for _, s := range candidates {
		key := s.Studio + "|" + string(s.Kind)
		if seen[key] {
			continue
		}
		seen[key] = true
		it := item{
			title:   s.Studio,
			meta:    i18n.KindLabel(s.Kind),
			payload: payloadStudio{src: s},
		}
		if s.Studio == pin {
			it.icon = m.ic.Done
			it.iconAccent = true
		}
		items = append(items, it)
	}
	_ = m.setItems(items, 0)
}

func (m Model) studioPin() string {
	title := m.eng.Lib.TitleByRef(m.ref)
	if title == nil {
		return ""
	}
	entry := m.eng.Lib.EntryLookup(title.ID)
	if entry == nil {
		return ""
	}
	return entry.StudioPin
}
