package ui

import "github.com/Basmanjacks/uaanime/internal/i18n"

func (m *Model) errorText(err error) string {
	return i18n.ErrorTextWithDebug(err, m.opts.Debug)
}
