package i18n

import "fmt"

// Рядки завантаження серій на диск. CLI — Msg*, TUI — TuiDl*/TuiSetDl*.
const (
	MsgDiskFull               = "недостатньо місця на диску для цієї серії"
	MsgNoWriteAccess          = "немає доступу на запис у папку завантажень"
	MsgDownloadCancelled      = "Завантаження скасовано"
	MsgStreamEncrypted        = "цей потік зашифровано — зберегти його не можна, але можна дивитись онлайн"
	MsgStreamUnsupported      = "формат цього потоку не підтримується для збереження"
	MsgStreamExpired          = "посилання на відео застаріло — спробуй ще раз"
	MsgAlreadySaved           = "ця серія вже збережена в цій якості"
	MsgDownloadBusy           = "інше завантаження цього тайтлу триває"
	MsgDownloadProgress       = "Серія %d · %s · %d%% · %s / %s · %s/с · залишилось %s"
	MsgDownloadDone           = "Збережено: %s"
	MsgDownloadDirLine        = "завантаження: %s (вільно %s)"
	MsgDownloadDirNew         = "завантаження: %s (папку буде створено при першому завантаженні)"
	MsgDownloadDirNoWrite     = "завантаження: %s (немає доступу на запис)"
	MsgDownloadQualityMissing = "якості %dp немає; є: %s"
	MsgDownloadQualityAuto    = "авто"

	TuiDlItem           = "Завантаження"
	TuiDlTitle          = "Завантаження"
	TuiDlQualityTitle   = "Завантажити серію %d"
	TuiDlSizeApprox     = "~%s"
	TuiDlSizeUnknown    = "розмір невідомий"
	TuiDlSizeNote       = "Розмір приблизний: оцінка за бітрейтом і тривалістю"
	TuiDlFolder         = "Папка: %s"
	TuiDlBlockNow       = "Зараз"
	TuiDlBadgeQueued    = "у черзі"
	TuiDlBadgePreparing = "готую"
	TuiDlBadgeActive    = "завантажую %d%%"
	TuiDlBadgeSaved     = "на диску"
	TuiDlBadgeFailed    = "не завантажилось"
	TuiDlBadgeCancelled = "скасовано"
	TuiDlMetaRate       = "%s/с · ~%s"
	TuiDlHomeBadge      = "%d з %d · %d%%"
	TuiDlEpisodeRow     = "%s · серія %d"
	TuiDlPreparing      = "Готую завантаження…"
	TuiDlQueued         = "Серія %d у черзі · %s"
	TuiDlSaved          = "Серія %d збережена · %s · %s"
	TuiDlFailed         = "Серія %d не завантажилась: %s"
	TuiDlAlready        = "Серія %d уже на диску · Enter — грати"
	TuiDlPlayingLocal   = "Граю з диска"
	TuiDlCancelled      = "Завантаження серії %d скасовано"
	TuiDlQuitWarn       = "Завантаження триває · ще раз — перервати й вийти"
	TuiDlUnavailable    = "Завантаження недоступні"
	TuiDlPathBusy       = "Спершу дочекайся або скасуй завантаження"
	TuiDlPathNoWrite    = "Немає доступу на запис: %s"
	TuiDlPathNoteHome   = "~ розгортається в домашню теку"
	TuiDlPathNoteCreate = "Папку буде створено автоматично"
	TuiDlPathNoteSub    = "Для кожного тайтлу — своя підпапка"
	TuiDlFolderNote     = "Для кожного тайтлу створюється своя підпапка"
	TuiSetDlFolder      = "Папка"
	TuiSetDlFolderTitle = "Папка завантажень"
	TuiBlockDownloads   = "Завантаження"
	TuiActionDownload   = "зберегти"
	TuiActionRemove     = "прибрати"
	TuiActionRetry      = "повторити"
	TuiActionSave       = "зберегти"
	TuiActionCancel     = "скасувати"
	TuiActionDownloads  = "завантаження"
)

// Downloads — «3 завантаження» за українським правилом множини.
func Downloads(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "завантаження", "завантаження", "завантажень"))
}

// ActiveDownloads — «1 активне», «2 активних».
func ActiveDownloads(n int) string {
	return fmt.Sprintf("%d %s", n, plural(n, "активне", "активних", "активних"))
}

// QueuedDownloads — «2 у черзі».
func QueuedDownloads(n int) string {
	return fmt.Sprintf("%d у черзі", n)
}

// QualityLabel — «1080p» або «авто» для невідомої якості.
func QualityLabel(height int) string {
	if height <= 0 {
		return MsgDownloadQualityAuto
	}
	return fmt.Sprintf("%dp", height)
}

// Bytes — розмір словами: «1,2 ГБ», «610 МБ», «0 Б». Кома як десятковий
// роздільник, бо це український текст; один знак після коми лише до 10,
// далі цілі — точніше людина все одно не читає.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d Б", n)
	}
	units := []string{"КБ", "МБ", "ГБ", "ТБ"}
	value := float64(n)
	idx := -1
	for value >= unit && idx < len(units)-1 {
		value /= unit
		idx++
	}
	if value < 10 {
		s := fmt.Sprintf("%.1f", value)
		if len(s) > 2 && s[len(s)-2:] == ".0" {
			s = s[:len(s)-2]
		}
		for i := range s {
			if s[i] == '.' {
				s = s[:i] + "," + s[i+1:]
				break
			}
		}
		return s + " " + units[idx]
	}
	return fmt.Sprintf("%.0f %s", value, units[idx])
}
