package download

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Ліміти імені. 255 байт — стеля компонента шляху на ext4, APFS, exFAT і NTFS;
// беремо запас, бо поруч із медіафайлом лежить sidecar «.<ім'я>.json» (+6 байт)
// і тимчасовий «<ім'я>.NNNNN.part» від CreateTemp. Папка коротша за файл: її
// ім'я входить у шлях кожного файла всередині.
const (
	maxFileBytes   = 250
	maxFolderBytes = 200
	// minTitleBytes — скільки байтів назви обов'язково лишається, коли студія
	// така довга, що суфікс сам з'їдає бюджет. Без цього файл звався б
	// « - 05 - <дуже довга студія> [Озв].ts», і людина не впізнала б тайтл.
	minTitleBytes = 16
	// untitledName — назва не пережила санітизації (порожня, самі крапки,
	// самі керуючі символи). Латиною: це технічний фолбек, а не текст для UI.
	untitledName = "untitled"
)

// Parts — усе, з чого складається ім'я збереженої серії. Studio й Kind можуть
// бути порожні (прямий файл без відомого релізу), Height == 0 означає «якість
// невідома» — тоді в імені немає токена якості, а не «0p».
type Parts struct {
	Title   string
	Episode int
	Kind    provider.Kind
	Studio  string
	Height  int
	Ext     string
}

// FolderName — ім'я папки тайтлу всередині папки завантажень. Людське:
// користувач відкриває цю теку у Finder і має впізнати тайтл без нашої
// допомоги, тому ніяких слагів і хешів у звичайному випадку.
func FolderName(title string) string {
	if s := sanitise(title, maxFolderBytes); s != "" {
		return s
	}
	return untitledName
}

// Filename — «<Назва> - 05 - <Студія> [<Тип>, 1080p].ts». Порядок частин
// обрано так, щоб сортування за іменем у файловому менеджері давало серії по
// порядку, а варіанти однієї серії стояли поруч.
//
// Назва ріжеться під ліміт, суфікс — ні: без номера серії й якості файл
// перестає бути впізнаваним, а обрізана назва лишається впізнаваною.
func Filename(p Parts) string {
	title := sanitise(p.Title, maxFileBytes)
	if title == "" {
		title = untitledName
	}
	studio := sanitise(p.Studio, maxFileBytes)
	ext := normExt(p.Ext)
	ep := episodeToken(p.Episode)
	tag := bracketTag(p.Kind, p.Height)

	// Бюджет назви = ліміт мінус усе, що не ріжеться.
	budget := maxFileBytes - len(" - ") - len(ep) - len(tag) - len(ext)
	if studio != "" {
		// Студію ріжемо лише тоді, коли інакше назві не лишається місця.
		if room := budget - len(" - ") - minTitleBytes; len(studio) > room {
			studio = strings.Trim(truncBytes(studio, room), " .")
		}
	}
	mid := ""
	if studio != "" {
		mid = " - " + studio
	}
	title = strings.Trim(truncBytes(title, budget-len(mid)), " .")
	if title == "" {
		title = untitledName
	}
	return title + " - " + ep + mid + tag + ext
}

// Path — повний шлях медіафайла. folder — ім'я папки тайтлу; FolderFor віддає
// його вже як шлях, тому абсолютний folder перекриває dir, а не приклеюється
// до нього (інакше вийшло б «<dir>/<dir>/<folder>»).
func Path(dir, folder string, p Parts) string {
	if filepath.IsAbs(folder) {
		return filepath.Join(folder, Filename(p))
	}
	return filepath.Join(dir, folder, Filename(p))
}

// Розбір імені потрібен для файлів БЕЗ sidecar-а: людина перенесла їх руками
// або sidecar загубився. Порівнювати ім'я з Filename() не можна: ми не робимо
// NFC-нормалізації (x/text не в стеку, а своя таблиця composition — це не те,
// що варто тримати в застосунку), тож «й» із диска буває двома рунами там, де
// в нас одна, і байтове порівняння дало б хибне «не знайдено». Тому якорі —
// лише ASCII-частини імені: номер серії, «1080p» і розширення.
//
// `.+` жадібний, тому у назві на кшталт «Steins - 12 - Gate - 05 - Студія
// [Озв].ts» виграє ОСТАННЯ група « - NN - », тобто 05. Це і є бажане: суфікс
// ми дописуємо в кінець, а все, що лівіше, — назва тайтлу.
var (
	reSavedQuality = regexp.MustCompile(`^.+ - (\d{1,4})(?: - .*)? \[(?:[^\]]*?, )?(\d{2,4})p\]\.(ts|webm|mp4)$`)
	reSavedPlain   = regexp.MustCompile(`^.+ - (\d{1,4})(?: - .*)?(?: \[[^\]]*\])?\.(ts|webm|mp4)$`)
)

// parseSavedName дістає з імені номер серії й висоту (0 — невідома).
// Приховані імена відкидаються одразу: «.<ім'я>.json» — це наш sidecar, а не
// медіафайл, і вгадувати серію з нього не можна.
func parseSavedName(name string) (ep, height int, ok bool) {
	if name == "" || strings.HasPrefix(name, ".") {
		return 0, 0, false
	}
	if m := reSavedQuality.FindStringSubmatch(name); m != nil {
		ep, _ = strconv.Atoi(m[1])
		height, _ = strconv.Atoi(m[2])
		return ep, height, true
	}
	if m := reSavedPlain.FindStringSubmatch(name); m != nil {
		ep, _ = strconv.Atoi(m[1])
		return ep, 0, true
	}
	return 0, 0, false
}

// folderID — короткий стабільний суфікс для випадку, коли два різні тайтли
// дають однакову FolderName. Числовий префікс слага (news_id у DLE) читабельний
// і вже унікальний у межах провайдера, тож він у пріоритеті; інакше 6 hex
// від fnv64a — достатньо, щоб не зіткнутися на сотні папок, і коротко.
func folderID(ref provider.TitleRef) string {
	if i := strings.IndexByte(ref.Slug, '-'); i > 0 && isDigits(ref.Slug[:i]) {
		return ref.Slug[:i]
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(ref.Provider + ":" + ref.Slug))
	return fmt.Sprintf("%06x", h.Sum64()&0xffffff)
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// episodeToken — «05», «101». Два розряди тримають серії в правильному порядку
// при сортуванні за іменем; трицифрові серії ростуть самі.
func episodeToken(ep int) string {
	if ep < 0 {
		ep = 0
	}
	return fmt.Sprintf("%02d", ep)
}

// bracketTag — « [Озв, 1080p]». Невідомий тип або невідома якість просто
// зникають з дужок; коли невідомі обидва — дужок немає зовсім.
func bracketTag(kind provider.Kind, height int) string {
	k := ""
	if provider.ValidKind(kind) {
		k = i18n.KindShort(kind)
	}
	q := ""
	if height > 0 {
		q = strconv.Itoa(height) + "p"
	}
	switch {
	case k != "" && q != "":
		return " [" + k + ", " + q + "]"
	case k != "":
		return " [" + k + "]"
	case q != "":
		return " [" + q + "]"
	}
	return ""
}

// normExt зводить розширення до трьох, які вміє прочитати parseSavedName:
// інакше збережений файл не знайшовся б після перезапуску. .ts — типове,
// бо HLS-сегменти склеюються саме в MPEG-TS.
func normExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".webm", "webm":
		return ".webm"
	case ".mp4", "mp4":
		return ".mp4"
	}
	return ".ts"
}

// sanitise робить із недовіреного рядка (назва з сайту, студія з плеєра)
// безпечний компонент імені файла.
func sanitise(s string, limitBytes int) string {
	// CleanText уже прибрав C0/C1, ESC-послідовності й bidi-керування —
	// ті самі символи, що ламають термінал, ламають і вивід `ls`.
	s = provider.CleanText(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '/' || r == '\\':
			// Роздільники шляху: інакше назва «Fate/Zero» створила б підпапку.
			b.WriteRune('-')
		case r == ':':
			// Двокрапка легальна на APFS, але ламає MRL для VLC («file:…»)
			// і показується у Finder як «/» — тобто виглядає не тим, чим є.
			b.WriteRune('-')
		case r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			// Заборонені на Windows і exFAT: зовнішній диск із завантаженнями
			// має лишатися придатним до перенесення.
			b.WriteRune('-')
		case isInvisible(r):
			// Нульової ширини й bidi-керування: в імені файла вони невидимі,
			// але роблять два різні імені візуально однаковими.
		default:
			b.WriteRune(r)
		}
	}
	out := collapseSpaces(b.String())
	// Крапки з країв: провідна ховає файл у Unix, кінцева зникає у Windows.
	out = strings.Trim(out, " .")
	out = strings.Trim(truncBytes(out, limitBytes), " .")
	if out == "" || out == "." || out == ".." {
		return ""
	}
	return out
}

// isInvisible — нульової ширини (U+200B–U+200D, U+FEFF) і керування напрямом
// письма (U+200E–U+200F, U+202A–U+202E, U+2066–U+2069). Частину з них знімає
// вже CleanText; дублюємо свідомо — санітизація імені має бути самодостатньою.
func isInvisible(r rune) bool {
	return (r >= 0x200b && r <= 0x200f) ||
		(r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069) ||
		r == 0xfeff
}

// collapseSpaces стискає серії пробілів до одного: заміна «/» на «-» лишає
// «A - - B», а видалення невидимих — подвійні пробіли.
func collapseSpaces(s string) string {
	if !strings.Contains(s, "  ") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' {
			if space {
				continue
			}
			space = true
		} else {
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// truncBytes ріже по межі руни: половинка руни в імені файла — це «?» у
// файловому менеджері й зламаний round-trip розбору.
func truncBytes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	for i := limit; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i]
		}
	}
	return ""
}
