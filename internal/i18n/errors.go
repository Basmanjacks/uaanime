package i18n

import (
	"errors"
	"fmt"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// ErrorText — єдине відображення класу помилки в текст для людини.
// І CLI, і TUI показують помилки лише через нього: інакше кожен шар мав би
// власну (розбіжну) таблицю класів, а правило «offline / провайдер / немає
// потоку — три різні повідомлення» трималося б на дисципліні.
//
// Результат проходить через provider.CleanText, бо помилки цитують недовірені
// рядки: embed-URL, адресу потоку, тіло відповіді сайту. Це остання точка
// перед терміналом, тому саме тут ESC-послідовність із чужої сторінки
// перестає бути здатною перемалювати чужий екран.
func ErrorText(err error) string {
	var text string
	switch {
	case errors.Is(err, errs.ErrStoreBusy):
		text = MsgStoreBusy
	case errors.Is(err, errs.ErrOffline):
		text = MsgOffline
	case errors.Is(err, errs.ErrNoStream):
		text = MsgNoPlayableHost
	case errors.Is(err, errs.ErrNoPlayer):
		text = MsgNoPlayer
	case errors.Is(err, errs.ErrPlayer):
		text = MsgPlayerUnavailable
	case errors.Is(err, errs.ErrDiskFull):
		text = MsgDiskFull
	case errors.Is(err, errs.ErrNoWriteAccess):
		text = MsgNoWriteAccess
	case errors.Is(err, errs.ErrCancelled):
		text = MsgDownloadCancelled
	case errors.Is(err, errs.ErrEncryptedStream):
		text = MsgStreamEncrypted
	case errors.Is(err, errs.ErrUnsupportedStream):
		text = MsgStreamUnsupported
	case errors.Is(err, errs.ErrStreamExpired):
		text = MsgStreamExpired
	case errors.Is(err, errs.ErrAlreadySaved):
		text = MsgAlreadySaved
	case errors.Is(err, errs.ErrDownloadBusy):
		text = MsgDownloadBusy
	case errors.Is(err, errs.ErrProvider):
		text = MsgSourceUnavailable
	default:
		text = MsgOperationFailed
	}
	return provider.CleanText(text)
}

// ErrorTextWithDebug keeps detailed causes opt-in and terminal-safe.
func ErrorTextWithDebug(err error, debug bool) string {
	text := ErrorText(err)
	if debug && err != nil {
		text += fmt.Sprintf("\n%s", provider.CleanText(err.Error()))
	}
	return text
}
