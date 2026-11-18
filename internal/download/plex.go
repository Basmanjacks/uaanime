package download

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/basmanjacks/uaanime/internal/errs"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// Layout — як серії розкладаються по папках. Типова — одна папка на тайтл
// (FolderFor), plex — «<Серіал>/Season NN/<Серіал> - SNNEMM - …», яку
// розуміє сканер серіалів Plex. Обирається лише прапорцем download: у
// config.json нового налаштування немає.
type Layout string

const (
	LayoutDefault Layout = "default"
	LayoutPlex    Layout = "plex"
)

// ParseLayout приймає рівно ті значення, які друкує довідка.
func ParseLayout(s string) (Layout, bool) {
	switch Layout(s) {
	case LayoutDefault, LayoutPlex:
		return Layout(s), true
	}
	return "", false
}

// norm: порожній Layout (TUI, старі виклики) — типовий.
func (l Layout) norm() Layout {
	if l == LayoutPlex {
		return LayoutPlex
	}
	return LayoutDefault
}

const (
	minSeason = 1
	maxSeason = 99
	// seasonSep — пробіл, зокрема нерозривний: назви з сайту його містять.
	seasonSep = `[\s\x{00a0}]`
)

// reSeasonTail — явна позначка сезону в САМОМУ КІНЦІ назви: «(2 сезон)»,
// «2 сезон», «сезон 2», «(сезон 2)», «Season 2», «(Season 2)», «S2»/«S02»
// окремим словом. Голе число в кінці («86», «Лелуша 2») — не сезон: воно
// частина назви. Без дужок позначка мусить стояти після пробілу (або бути
// всією назвою — тоді SplitSeason її не відокремлює). Три й більше цифр
// регулярка не бере, бо перед групою стоїть «(» або пробіл.
var reSeasonTail = regexp.MustCompile(`(?i)^(.*?)(?:` +
	`\(` + seasonSep + `*(?:(\d{1,2})` + seasonSep + `*сезон|сезон` + seasonSep + `*(\d{1,2})|season` + seasonSep + `*(\d{1,2}))` + seasonSep + `*\)` +
	`|(?:^|` + seasonSep + `)(?:(\d{1,2})` + seasonSep + `*сезон|сезон` + seasonSep + `*(\d{1,2})|season` + seasonSep + `*(\d{1,2})|s(\d{1,2}))` +
	`)` + seasonSep + `*$`)

// SplitSeason ділить назву тайтлу на назву серіалу й номер сезону. Без
// позначки (або з номером поза 1–99) — назва як є і сезон 1. Розділові знаки
// перед позначкою («Назва: Сезон 2», «Назва - S2») прибираються разом із нею.
// Назва, що складається лише з позначки, лишається цілою: інакше серіал
// звався б «untitled».
func SplitSeason(title string) (show string, season int) {
	title = strings.TrimSpace(title)
	m := reSeasonTail.FindStringSubmatch(title)
	if m == nil {
		return title, minSeason
	}
	for _, g := range m[2:] {
		if g == "" {
			continue
		}
		n, err := strconv.Atoi(g)
		if err != nil || n < minSeason || n > maxSeason {
			return title, minSeason
		}
		season = n
		break
	}
	show = strings.TrimRightFunc(m[1], func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(":-–—,;.", r)
	})
	if show == "" || season == 0 {
		return title, minSeason
	}
	return show, season
}

// SeasonFolderName — «Season 02». Латиною, бо саме цю форму шукає Plex.
func SeasonFolderName(season int) string {
	return fmt.Sprintf("Season %02d", season)
}

// reSeasonFolder — тека сезону, зокрема з суфіксом розв'язання колізії
// («Season 02 [5663]»). Лише такі підпапки Index читає на другому рівні.
var reSeasonFolder = regexp.MustCompile(`^Season \d{2,}(?: \[[^\]]+\])?$`)

func isSeasonFolder(name string) bool { return reSeasonFolder.MatchString(name) }

// PlexFolderFor вибирає (і за потреби створює) теку сезону
// «<dir>/<Серіал>/Season NN» під тим самим folders.lock, що й FolderFor.
//
// Marker лежить у теці сезону: один marker — один тайтл провайдера, як і в
// типовій розкладці. Тека серіалу marker-а не має і може тримати сезони
// кількох тайтлів. Правила по порядку: (1) тека сезону з marker-ом цього
// ref — де б вона не була; (2) тека серіалу FolderName(show), якщо вона не є
// чужою текою тайтлу типової розкладки, а в ній «Season NN», якщо вільна
// або нічия й без чужих файлів; (3) те саме для «Season NN [id]»; (4) тека
// серіалу «<Серіал> [id]» за тими самими правилами. Чужі файли в самій теці
// серіалу не заважають — uaanime пише лише в теку сезону.
func PlexFolderFor(dir string, ref provider.TitleRef, show string, season int) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("папка завантажень не задана: %w", errs.ErrNoWriteAccess)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", writeErr(dir, err)
	}
	lock, err := lockFolders(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Close() }()

	if folder, ok := seasonFolderByMarker(dir, ref); ok {
		return folder, nil
	}
	if show == "" {
		show, _ = SplitSeason(ref.Name)
	}
	base := FolderName(show)
	id := " [" + folderID(ref) + "]"
	seasonName := SeasonFolderName(season)
	for _, showName := range []string{base, base + id} {
		showDir := filepath.Join(dir, showName)
		if !showFolderUsable(showDir, ref) {
			continue
		}
		for _, name := range []string{seasonName, seasonName + id} {
			folder, ok, err := claim(filepath.Join(showDir, name), ref, seasonForeign)
			if err != nil || ok {
				return folder, err
			}
		}
	}
	return "", fmt.Errorf("%s/%s: не вдалося виділити папку: %w", base, seasonName, errs.ErrProvider)
}

// seasonFolderByMarker — як folderByMarker, але на другому рівні: лише теки
// сезонів усередині тек верхнього рівня. Тека тайтлу типової розкладки сюди
// не потрапляє: збережене раніше в типовій розкладці не переноситься.
func seasonFolderByMarker(dir string, ref provider.TitleRef) (string, bool) {
	tops, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, top := range tops {
		if !top.IsDir() || strings.HasPrefix(top.Name(), ".") {
			continue
		}
		showDir := filepath.Join(dir, top.Name())
		ents, err := os.ReadDir(showDir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() || !isSeasonFolder(e.Name()) {
				continue
			}
			folder := filepath.Join(showDir, e.Name())
			if m, ok := ReadMarker(folder); ok && m.Provider == ref.Provider && m.Slug == ref.Slug {
				return folder, true
			}
		}
	}
	return "", false
}

// showFolderUsable — чи можна класти теку сезону в цю теку серіалу. Нова або
// нічия (без marker-а, хай навіть із чужими файлами) — так; тека тайтлу
// типової розкладки — лише якщо це тека цього ж ref; файл на місці теки — ні.
func showFolderUsable(showDir string, ref provider.TitleRef) bool {
	info, err := os.Stat(showDir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return true
	case err != nil || !info.IsDir():
		return false
	}
	if m, ok := ReadMarker(showDir); ok {
		return m.Provider == ref.Provider && m.Slug == ref.Slug
	}
	return true
}

// seasonForeign — строгіше за ownedByOther: теку сезону без marker-а
// усиновлюємо лише тоді, коли в ній немає ані чужих sidecar-ів, ані медіа
// без нашого sidecar-а. Тека «Season 01» у бібліотеці Plex — майже завжди
// чужа серія, і дописувати в неї свої файли не можна.
func seasonForeign(folder string, ref provider.TitleRef) bool {
	if ownedByOther(folder, ref) {
		return true
	}
	ents, err := os.ReadDir(folder)
	if err != nil {
		return true
	}
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		sc, ok := readSidecar(filepath.Join(folder, e.Name()))
		if !ok || sc.Provider != ref.Provider || sc.Slug != ref.Slug {
			return true
		}
	}
	return false
}
