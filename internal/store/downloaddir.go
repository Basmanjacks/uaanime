package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// downloadDirName — ім'я підпапки застосунку всередині системної папки з відео.
// Окремо від DataDir: завантажені серії — це медіатека користувача, а не наш
// службовий стан, тому вони лежать там, де людина шукає відео, а не в ~/.config.
const downloadDirName = "uaanime"

// writeProbeName — префікс тимчасового файла-проби. Крапка на початку ховає
// його від Finder, а CreateTemp додає унікальний суфікс, щоб дві одночасні
// проби не боролися за один шлях.
const writeProbeName = ".uaanime-write-test"

// DefaultDownloadDir — типова папка завантажень: там, де відео шукає сама
// система. macOS має канонічну ~/Movies; на решті — XDG_VIDEOS_DIR, якщо
// оточення його дало (user-dirs.dirs), інакше ~/Videos.
func DefaultDownloadDir() string {
	if runtime.GOOS != "darwin" {
		// Відносне значення XDG нічого не визначає (специфікація вимагає
		// абсолютний шлях), тому таке просто ігноруємо і йдемо до ~/Videos.
		if v := os.Getenv("XDG_VIDEOS_DIR"); filepath.IsAbs(v) {
			return filepath.Join(filepath.Clean(v), downloadDirName)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return fallbackDownloadDir()
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Movies", downloadDirName)
	}
	return filepath.Join(home, "Videos", downloadDirName)
}

// fallbackDownloadDir — коли домашнього каталогу немає (порожній HOME у
// сервісному оточенні чи в контейнері). Повертати відносний шлях не можна:
// гігабайти сипалися б у випадковий CWD, а те саме налаштування вело б у різні
// місця. Каталог даних застосунку вже абсолютний і вже наш, тому downloads
// живуть у ньому; якщо й він недоступний — TempDir, який абсолютний завжди.
func fallbackDownloadDir() string {
	if dir, err := DataDir(); err == nil && filepath.IsAbs(dir) {
		return filepath.Join(dir, "downloads")
	}
	return filepath.Join(os.TempDir(), downloadDirName, "downloads")
}

// ExpandHome розкриває початкову тильду: у полі шляху люди пишуть ~/Movies.
// `~user` не розкривається — для цього треба читати базу користувачів, а
// такий шлях у налаштуваннях означає радше помилку, ніж намір. Тильда не на
// початку — звичайний символ імені файла, і файл із нею має лишитися собою.
func ExpandHome(p string) string {
	if p != "~" && !hasHomePrefix(p) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

func hasHomePrefix(p string) bool {
	return len(p) >= 2 && p[0] == '~' && os.IsPathSeparator(p[1])
}

// EnsureDownloadDir створює папку і доводить, що в неї справді можна писати.
// Самого MkdirAll мало: на наявному каталозі він мовчить, і відмова вилізла б
// аж посеред завантаження, коли частина гігабайта вже прочитана з мережі.
// Режим 0755, а не 0700 як у каталозі даних: це медіатека, яку відкривають у
// Finder і віддають плеєру чи файловому менеджеру, таємниць у ній немає.
// Кличуть лише job на старті та екран шляху при збереженні — не LoadConfig.
func EnsureDownloadDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return downloadDirError(dir, err)
	}
	f, err := os.CreateTemp(dir, writeProbeName+".*")
	if err != nil {
		return downloadDirError(dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// ProbeDownloadDir — read-only погляд на папку для doctor: нічого не створює
// (відсутня папка не помилка, її зробить перше завантаження) і пробує запис
// лише тоді, коли папка вже є. free == 0 при ok==false у freeBytes означає
// «невідомо», і doctor так це й показує.
func ProbeDownloadDir(dir string) (exists, writable bool, free int64) {
	if dir == "" {
		return false, false, 0
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false, false, 0
	}
	if f, err := os.CreateTemp(dir, writeProbeName+".*"); err == nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		writable = true
	}
	n, _ := freeBytes(dir)
	return true, writable, n
}

// FreeBytes — вільне місце на ФС каталогу; false означає «невідомо» (ОС без
// підтримки або неіснуючий каталог), і це не те саме, що «нуль»: викликач,
// який не знає розміру, просто не робить перевірку, а не відмовляє.
func FreeBytes(dir string) (int64, bool) { return freeBytes(dir) }

// downloadDirError переводить помилку ФС у сентинел, який i18n покаже людині
// різним текстом: «немає місця» і «немає доступу» вимагають різних дій.
// Шлях лишається в повідомленні — без нього незрозуміло, яку саме папку чинити.
func downloadDirError(dir string, err error) error {
	noWrite, full := diskErrno(err)
	switch {
	case full:
		return fmt.Errorf("%s: %w", dir, errs.ErrDiskFull)
	case noWrite || errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s: %w", dir, errs.ErrNoWriteAccess)
	}
	return fmt.Errorf("%s: %w", dir, err)
}
