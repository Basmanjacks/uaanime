package ui

import (
	"errors"
	"fmt"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/i18n"
)

func errText(err error) string {
	switch {
	case errors.Is(err, errs.ErrOffline):
		return i18n.MsgOffline
	case errors.Is(err, errs.ErrNoStream):
		return i18n.MsgNoPlayableHost
	default:
		return fmt.Sprintf(i18n.MsgProviderFailed, err)
	}
}
