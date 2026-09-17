package errs

import "errors"

// Сентинели завантаження на диск. Кожен — окремий клас для i18n.ErrorText:
// «немає місця», «нема доступу», «зашифровано» і «протухло» вимагають різних
// дій від людини, тож не можуть ховатися за спільним «джерело зламалось».
var (
	ErrDiskFull      = errors.New("недостатньо місця на диску")
	ErrNoWriteAccess = errors.New("немає доступу на запис")
	ErrCancelled     = errors.New("скасовано")
	// ErrEncryptedStream — потік має #EXT-X-KEY. Ми нічого не обходимо
	// (правило 8): це відмова зберігати, а не поломка джерела.
	ErrEncryptedStream = errors.New("потік зашифровано")
	// ErrUnsupportedStream — формат, який не склеїти в один .ts: fMP4
	// (#EXT-X-MAP), #EXT-X-BYTERANGE або live-плейлист без #EXT-X-ENDLIST.
	ErrUnsupportedStream = errors.New("формат потоку не підтримується")
	// ErrStreamExpired — підписане посилання протухло посеред завантаження
	// (403/404 на сегменті); ретрай не допоможе, потрібна нова резолюція.
	ErrStreamExpired = errors.New("посилання на відео застаріло")
	// ErrAlreadySaved — файл того самого релізу й якості вже лежить на диску.
	ErrAlreadySaved = errors.New("серія вже збережена")
	// ErrDownloadBusy — інший процес тримає lock цього тайтлу.
	ErrDownloadBusy = errors.New("інше завантаження цього тайтлу триває")
)
