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
	_, text := classify(err)
	return provider.CleanText(text)
}

// ErrorCode — машинний код того самого класу помилки для `--json`. Коди —
// контракт для скриптів: їх не перекладають і не перейменовують.
func ErrorCode(err error) string {
	code, _ := classify(err)
	return code
}

// Коди помилок headless-команд під --json. Частина з них не має сентинела:
// CodeUsage і CodeInternal ставить сам CLI.
const (
	CodeUsage              = "usage"
	CodeInternal           = "internal"
	CodeOffline            = "offline"
	CodeProvider           = "provider"
	CodeNoStream           = "no_stream"
	CodeHostUnsupported    = "host_unsupported"
	CodeReleaseUnavailable = "release_unavailable"
	CodeQualityMissing     = "quality_missing"
	CodeCancelled          = "cancelled"
)

// errorClasses — єдина впорядкована таблиця «сентинел → код → текст».
// Порядок важливий: перший збіг перемагає, тому підкласи (ErrHostUnsupported
// всередині ErrNoStream) стоять перед своїм загальним класом.
var errorClasses = []struct {
	sentinel error
	code     string
	text     string
}{
	{errs.ErrStoreBusy, "store_busy", MsgStoreBusy},
	{errs.ErrOffline, CodeOffline, MsgOffline},
	{errs.ErrHostUnsupported, CodeHostUnsupported, MsgNoPlayableHost},
	{errs.ErrNoStream, CodeNoStream, MsgNoStream},
	{errs.ErrNoPlayer, "no_player", MsgNoPlayer},
	{errs.ErrPlayer, "player", MsgPlayerUnavailable},
	{errs.ErrDiskFull, "disk_full", MsgDiskFull},
	{errs.ErrNoWriteAccess, "no_write_access", MsgNoWriteAccess},
	{errs.ErrCancelled, CodeCancelled, MsgDownloadCancelled},
	{errs.ErrEncryptedStream, "stream_encrypted", MsgStreamEncrypted},
	{errs.ErrUnsupportedStream, "stream_unsupported", MsgStreamUnsupported},
	{errs.ErrStreamExpired, "stream_expired", MsgStreamExpired},
	{errs.ErrAlreadySaved, "already_saved", MsgAlreadySaved},
	{errs.ErrSavedFileMissing, "saved_file_missing", MsgSavedFileMissing},
	{errs.ErrDownloadBusy, "download_busy", MsgDownloadBusy},
	{errs.ErrReleaseUnavailable, CodeReleaseUnavailable, MsgReleaseUnavailable},
	{errs.ErrQualityMissing, CodeQualityMissing, MsgQualityMissing},
	{errs.ErrProvider, CodeProvider, MsgSourceUnavailable},
}

func classify(err error) (code, text string) {
	for _, c := range errorClasses {
		if errors.Is(err, c.sentinel) {
			return c.code, c.text
		}
	}
	return CodeInternal, MsgOperationFailed
}

// ErrorTextWithDebug keeps detailed causes opt-in and terminal-safe.
func ErrorTextWithDebug(err error, debug bool) string {
	text := ErrorText(err)
	if debug && err != nil {
		text += fmt.Sprintf("\n%s", provider.CleanText(err.Error()))
	}
	return text
}
