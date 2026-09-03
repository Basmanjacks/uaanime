// Package i18n — усі рядки, які бачить користувач. Українська — мова за замовчуванням;
// англійська додається пізніше без рефакторингу викликів.
package i18n

import "github.com/Basmanjacks/uaanime/internal/provider"

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
	MsgBadTitleID       = "невалідний ідентифікатор тайтлу: %s"
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
	MsgInternalError    = "внутрішня помилка: %v"
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
	TuiEpMarked        = "серія %d — переглянуто"
	TuiEpUnmarked      = "серія %d — позначку знято"
	TuiHintHome        = "↑↓ Вибір · Enter Відкрити · M Закладка · / Пошук · , Налаштування · Q Вихід"
	TuiHintSearch      = "Enter Шукати/відкрити · ↓ Нещодавнє · X Прибрати · M Закладка · Esc"
	// Підказка мусить влазити в 80 колонок цілою: обрізане «Esc Назад» гірше
	// за незгадану клавішу фільтра, яку список підказує сам.
	TuiHintEpisodes = "↑↓ Вибір · Enter Грати · X Переглянуто · S Озвучка · M Закладка · Esc Назад"
	TuiHintStudio   = "↑↓ Вибір · Enter Закріпити й грати · Esc Назад"
	TuiStudioPinned = "озвучка: %s"
	// TuiRemainingFmt — залишок серій і оцінка часу в заголовку екрана серій.
	// «~» перед часом обов'язкове: це середнє, а не точна тривалість.
	TuiRemainingFmt = "%s · ~%s"
	// TuiStudioCoverage — скільки серій тайтлу озвучила студія: «3/12».
	TuiStudioCoverage = "%d/%d"
	TuiStudioAuto     = "авто"
	TuiStudioFallback = "озвучка %s недоступна — грає %s"
	TuiEmptyLibrary   = "Закладок ще немає — почни з пошуку"
	TuiNothingFound   = "Нічого не знайдено"
	TuiMoreStudios    = "+%d"
	TuiToday          = "сьогодні"
	TuiYesterday      = "вчора"
	TuiBlockContinue  = "Продовжити"
	TuiBlockLibrary   = "Закладки"
	TuiBlockMore      = "Ще"
	TuiBlockCatalog   = "Каталог"
	TuiBlockTop       = "Топ сезону"
	TuiBlockFresh     = "Нові релізи"
	TuiShowMore       = "показати ще"
	// TuiBlockRecent — секція нещодавніх запитів під полем пошуку.
	TuiBlockRecent = "Нещодавнє"

	// Мітки озвучення в картці пошуку. Назви студій не перекладаються ніколи,
	// а от «дубляж/субтитри» — саме те, за чим людина сканує список.
	TuiDub    = "Дуб"
	TuiSub    = "Саб"
	TuiDubSub = "Дуб+Саб"

	TuiHistoryItem = "Історія"
	TuiHintList    = "↑↓ Вибір · Enter Грати · Esc Назад"

	// екран налаштувань
	TuiSettingsItem     = "Налаштування"
	TuiSettingsTitle    = "Налаштування"
	TuiHintSettings     = "↑↓ Вибір · Enter Змінити · ←→ Значення · Esc Назад"
	TuiHintSettingsPick = "↑↓ Вибір · Enter Обрати · Esc Назад"
	TuiBlockPlayback    = "Перегляд"
	TuiBlockRemote      = "Пульт на телефоні"
	TuiBlockAbout       = "Про"
	TuiSetPlayer        = "Плеєр"
	TuiSetAutoplay      = "Наступна серія"
	TuiSetKind          = "Що вмикати спершу"
	TuiSetStudio        = "Улюблена студія"
	TuiSetRemote        = "Режим"
	TuiSetStudioNone    = "не вибрано"
	TuiSetStudioEmpty   = "з'явиться після першого вибору озвучки"
	TuiSetKindSubNote   = "якщо українських субтитрів немає — увімкнеться озвучення"
	TuiSetKindDubNote   = "повний дубляж, коли він є"
	TuiSetKindVoiceNote = "багатоголоса озвучка поверх оригіналу"
	TuiValAuto          = "автоматично"
	TuiValManual        = "вручну"
	TuiValAutoNote      = "після серії одразу стартує наступна"
	TuiValManualNote    = "після серії — назад до списку"
	TuiRemoteTokened    = "лише за посиланням"
	TuiRemoteTokenNote  = "адреса містить таємний ключ — працює лише в кого є посилання"
	TuiRemoteOpen       = "будь-хто в мережі"
	TuiRemoteOpenNote   = "коротка адреса; керувати зможе кожен у цій Wi-Fi"
	TuiRemoteOff        = "вимкнено"
	TuiRemoteOffNote    = "пульт не запускається"
	TuiRemoteOpenStatus = "Пульт відкрито для всієї мережі: %s"
	TuiRemoteOffStatus  = "Пульт вимкнено — телефон більше не керує"
	TuiRemoteAltURL     = "або за IP: %s"
	TuiDataDir          = "дані: %s"
	TuiSetSaved         = "Збережено"
	MsgConfigSaveFailed = "Не вдалося зберегти налаштування: %v"

	// веб-пульт (CLI)
	MsgRemoteURL    = "Пульт на телефоні: %s"
	MsgRemoteURLAlt = "або за IP: %s"
	MsgRemoteOpen   = "Пульт без токена: керувати може будь-хто у вашій мережі"
	// MsgRemoteIdentityUnsaved — пульт працює, але remote.json не записався.
	MsgRemoteIdentityUnsaved = "адресу пульта не збережено — після перезапуску вона може змінитися: %v"
	MsgRemotePortBusy        = "Порт %d зайнятий — цього разу пульт на %s; закладка запрацює після перезапуску"
	MsgRemoteFailed          = "Веб-пульт не запустився: %v"
	MsgDoctorRemote          = "пульт: %s"
	MsgDoctorRemoteOpen      = "пульт: %s (без токена — доступний усій мережі)"
	MsgDoctorRemoteOff       = "пульт: вимкнено (config.json, ключ remote: on | open | off)"
	MsgDoctorRemoteNew       = "пульт: ще не запускався — адреса з'явиться після першого перегляду"
	// веб-пульт (TUI)
	TuiRemote       = "Пульт: %s"
	TuiRemoteNarrow = "Пульт: адресу покаже `uaanime doctor`"
	// веб-пульт (сторінка)
	RemotePageTitle  = "uaanime"
	RemoteIdle       = "Зараз нічого не грає"
	RemoteEpisodeFmt = "Серія %d"
	RemotePlay       = "Відтворити"
	RemotePause      = "Пауза"
	RemoteBack       = "−10 с"
	RemoteForward    = "+10 с"
	RemoteNext       = "Наступна серія"
	RemoteStop       = "Зупинити"
	RemoteOffline    = "Немає зв'язку з uaanime"
)

func KindLabel(k provider.Kind) string {
	switch k {
	case provider.KindDub:
		return "дубляж"
	case provider.KindVoiceover:
		return "озвучення"
	case provider.KindSub:
		return "субтитри"
	case provider.KindMulti:
		return "змішано"
	default:
		// Сирий kind із диска чи зі сторінки сайту не має потрапляти в термінал:
		// невідоме значення — це той самий «немає доказів типу», тобто multi.
		return "змішано"
	}
}
