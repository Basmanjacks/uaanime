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
	MsgBadEpisode       = "номер серії має бути додатним числом, отримано: %s"
	MsgNothingFound     = "нічого не знайдено"
	MsgResolving        = "Шукаю джерела, серія %d…"
	MsgPickedSource     = "Обрано: %s (%s), хост %s"
	MsgNoPlayableHost   = "для серії є релізи, але жоден відеохост поки не підтримується"
	MsgLaunchingPlayer  = "Запускаю плеєр…"
	MsgPlayerFailed     = "не вдалося запустити плеєр: %v"
	MsgPlayerFallback   = "Бажаний плеєр не знайдено — граю через %s"
	MsgNoPlayer         = "не знайдено підтримуваного відеоплеєра"
	MsgInstallHintMac   = "встанови: brew install --cask vlc або brew install mpv"
	MsgInstallHintLinux = "встанови vlc або mpv з пакетного менеджера дистрибутива"
	MsgProviderFailed   = "джерело недоступне: %v"
	MsgTryingNext       = "Джерело недоступне, пробую інше…"
	MsgResume           = "Продовжую з %02d:%02d"
	MsgEpisodeDone      = "Серію %d завершено"
	MsgProgressSaved    = "Прогрес збережено: %02d:%02d"
	MsgStudioPinned     = "Студію закріплено за тайтлом: %s"
	MsgNeedTTY          = "TUI потребує термінала; headless-команди: search/episodes/resolve/play/doctor/export/import"
	MsgImported         = "Бібліотеку відновлено з бекапа (попередня — library.json.bak)"
	MsgOffline          = "немає з'єднання — перевір інтернет"
	MsgOfflineCache     = "Немає мережі — показую збережені дані"

	// doctor
	MsgDoctorPlayerOK      = "%s: знайдено"
	MsgDoctorPlayerMissing = "%s: НЕ знайдено"
	MsgDoctorDataDir       = "дані: %s"
	MsgDoctorProviderOK    = "%s: працює"
	MsgDoctorProviderDown  = "%s: недоступний (остання успішна відповідь: %s)"
	MsgDoctorNever         = "ще ніколи"

	// TUI
	TuiAppTitle        = "uaanime"
	TuiTagline         = "Аніме українською · дубляж і субтитри"
	TuiTaglineShort    = "Аніме українською"
	TuiSearchTitle     = "Пошук"
	TuiStudioTitle     = "Хто озвучує? (закріпиться за тайтлом)"
	TuiContinuePfx     = "%s · серія %d"
	TuiSearchItem      = "Пошук нового"
	TuiSearchPrompt    = "назва українською… "
	TuiEpisodeNo       = "Серія %d"
	TuiEpDone          = "переглянуто"
	TuiEpAt            = "зупинився на %02d:%02d"
	TuiStateWatching   = "переглядаєш"
	TuiStateDone       = "переглянуто"
	TuiStatePlanned    = "у планах"
	TuiPlaying         = "Грає у плеєрі — закрий його або натисни Esc, щоб повернутися"
	TuiResolving       = "Шукаю потік…"
	TuiSearching       = "Шукаю…"
	TuiBookmarkAdded   = "додано в закладки"
	TuiBookmarkRemoved = "прибрано із закладок"
	TuiHintHome        = "↑↓ Вибір · Enter Відкрити · M Закладка · / Пошук · Q Вихід"
	TuiHintSearch      = "Enter Шукати/відкрити · M Закладка · Esc Назад"
	TuiHintEpisodes    = "↑↓ Вибір · Enter Грати · M Закладка · / Фільтр · Esc Назад"
	TuiHintStudio      = "↑↓ Вибір · Enter Закріпити й грати · Esc Назад"
	TuiEmptyLibrary    = "Закладок ще немає — почни з пошуку"
	TuiNothingFound    = "Нічого не знайдено"
	TuiMoreStudios     = "+%d"
	TuiToday           = "сьогодні"
	TuiYesterday       = "вчора"
	TuiBlockContinue   = "Продовжити"
	TuiBlockLibrary    = "Закладки"
	TuiBlockMore       = "Ще"
	TuiBlockTop        = "Топ сезону"
	TuiBlockFresh      = "Нові релізи"
	TuiShowMore        = "показати ще"

	// Мітки озвучення в картці пошуку. Назви студій не перекладаються ніколи,
	// а от «дубляж/субтитри» — саме те, за чим людина сканує список.
	TuiDub    = "Дуб"
	TuiSub    = "Саб"
	TuiDubSub = "Дуб+Саб"

	TuiHistoryItem = "Історія"
	TuiHintList    = "↑↓ Вибір · Enter Грати · Esc Назад"
)
