// Package i18n — усі рядки, які бачить користувач. Українська — мова за замовчуванням;
// англійська додається пізніше без рефакторингу викликів.
package i18n

const (
	MsgUsage = `використання:
  uaanime                              # TUI
  uaanime search <запит> [--json]
  uaanime episodes <title-id> [--json]
  uaanime resolve <title-id> <серія> [--json]
  uaanime play <title-id> <серія> [--dry-run]
  uaanime doctor [--json]
  uaanime export > backup.json
  uaanime import backup.json`
	MsgBadEpisode      = "номер серії має бути додатним числом, отримано: %s"
	MsgNothingFound    = "нічого не знайдено"
	MsgResolving       = "Шукаю джерела, серія %d…"
	MsgPickedSource    = "Обрано: %s (%s), хост %s"
	MsgNoPlayableHost  = "для серії є релізи, але жоден відеохост поки не підтримується"
	MsgLaunchingPlayer = "Запускаю mpv…"
	MsgPlayerFailed    = "не вдалося запустити mpv: %v"
	MsgProviderFailed  = "джерело недоступне: %v"
	MsgTryingNext      = "Джерело недоступне, пробую інше…"
	MsgResume          = "Продовжую з %02d:%02d"
	MsgEpisodeDone     = "Серію %d завершено"
	MsgProgressSaved   = "Прогрес збережено: %02d:%02d"
	MsgStudioPinned    = "Студію закріплено за тайтлом: %s"
	MsgNeedTTY         = "TUI потребує термінала; headless-команди: search/episodes/resolve/play/doctor/export/import"
	MsgImported        = "Бібліотеку відновлено з бекапа (попередня — library.json.bak)"
	MsgOfflineCache    = "Немає мережі — показую збережені дані"

	// doctor
	MsgDoctorMPVOK        = "mpv: знайдено"
	MsgDoctorMPVMissing   = "mpv: НЕ знайдено — встанови: brew install mpv"
	MsgDoctorDataDir      = "дані: %s"
	MsgDoctorProviderOK   = "%s: працює"
	MsgDoctorProviderDown = "%s: недоступний (остання успішна відповідь: %s)"
	MsgDoctorNever        = "ще ніколи"

	// TUI
	TuiAppTitle      = "uaanime"
	TuiSearchTitle   = "Пошук"
	TuiStudioTitle   = "Хто озвучує? (закріпиться за тайтлом)"
	TuiContinuePfx   = "Продовжити: %s · серія %d"
	TuiSearchItem    = "Пошук нового"
	TuiSearchPrompt  = "назва українською… "
	TuiEpisode       = "%d серія"
	TuiEpDone        = "переглянуто"
	TuiEpAt          = "зупинився на %02d:%02d"
	TuiStateWatching = "переглядаєш"
	TuiStateDone     = "переглянуто"
	TuiStatePlanned  = "у планах"
	TuiPlaying       = "Грає в mpv — закрий плеєр або натисни Esc, щоб повернутися"
	TuiResolving     = "Шукаю потік…"
	TuiSearching     = "Шукаю…"
	TuiHintHome      = "↑↓ вибір · Enter відкрити · q вихід"
	TuiHintSearch    = "Enter шукати/відкрити · Esc назад"
	TuiHintEpisodes  = "↑↓ вибір · Enter грати · / фільтр · Esc назад"
	TuiHintStudio    = "↑↓ вибір · Enter закріпити й грати · Esc назад"
	TuiEmptyLibrary  = "Бібліотека порожня — почни з пошуку"
	TuiNothingFound  = "Нічого не знайдено"
	TuiMoreStudios   = "+%d"

	TuiHistoryItem     = "Історія"
	TuiUpdatesItem     = "Оновлення"
	TuiHintList        = "↑↓ вибір · Enter грати · Esc назад"
	TuiCheckingUpdates = "Перевіряю нові серії…"
	TuiNoUpdates       = "Нових серій немає"
	TuiNewEpisodes     = "%s — нових серій: %d"
)
