package download

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Таблиця санітизації: вхід — те, що реально приходить зі сторінки сайту
// (керуючі послідовності, bidi, роздільники шляху), вихід — те, що має
// лишитися в імені. Ця ж таблиця ганяється через round-trip нижче.
var sanitiseCases = []struct {
	name   string
	in     string
	folder string
}{
	{"роздільник шляху", "Fate/Zero", "Fate-Zero"},
	{"зворотний слеш і двокрапка", `C:\Temp`, "C--Temp"},
	{"дві крапки", "..", untitledName},
	{"крапка", ".", untitledName},
	{"порожнє", "", untitledName},
	{"NUL всередині", "Фрі\x00рен", "Фрірен"},
	{"CSI-послідовність", "\x1b[2JФрірен", "Фрірен"},
	{"OSC-послідовність", "Фрі\x1b]0;pwn\x07рен", "Фрірен"},
	{"RTL override", "\u202eФрірен", "Фрірен"},
	{"нульова ширина", "Фрі\u200bрен", "Фрірен"},
	{"BOM всередині", "Фрі\ufeffрен", "Фрірен"},
	{"заборонені на Windows", `a*b?c"d<e>f|g`, "a-b-c-d-e-f-g"},
	{"пробіли з країв", "  Фрірен  ", "Фрірен"},
	{"крапка в кінці", "Фрірен.", "Фрірен"},
	{"подвійні пробіли", "Фрірен   друга", "Фрірен друга"},
	{"довга кирилиця", strings.Repeat("я", 300), strings.Repeat("я", 100)},
}

func TestFolderName(t *testing.T) {
	for _, tc := range sanitiseCases {
		t.Run(tc.name, func(t *testing.T) {
			got := FolderName(tc.in)
			if got != tc.folder {
				t.Fatalf("FolderName(%q) = %q, очікував %q", tc.in, got, tc.folder)
			}
			if len(got) > maxFolderBytes {
				t.Fatalf("папка %d байт, ліміт %d", len(got), maxFolderBytes)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("папка %q обрізана посеред руни", got)
			}
		})
	}
}

func TestFilenameExamples(t *testing.T) {
	tests := []struct {
		p    Parts
		want string
	}{
		{
			Parts{Title: "Фрірен", Episode: 5, Kind: provider.KindVoiceover, Studio: "FanVoxUA", Height: 1080, Ext: ".ts"},
			"Фрірен - 05 - FanVoxUA [Озв, 1080p].ts",
		},
		{
			Parts{Title: "Фрірен", Episode: 12, Kind: provider.KindVoiceover, Studio: "Glass Moon", Ext: ".webm"},
			"Фрірен - 12 - Glass Moon [Озв].webm",
		},
		{
			Parts{Title: "Фрірен", Episode: 101, Kind: provider.KindSub, Height: 720, Ext: ".ts"},
			"Фрірен - 101 [Саб, 720p].ts",
		},
		{
			// Тип невідомий — у дужках лишається сама якість.
			Parts{Title: "Фрірен", Episode: 3, Studio: "FanVoxUA", Height: 480, Ext: ".mp4"},
			"Фрірен - 03 - FanVoxUA [480p].mp4",
		},
		{
			// Ні типу, ні якості — дужок немає зовсім.
			Parts{Title: "Фрірен", Episode: 3},
			"Фрірен - 03.ts",
		},
		{
			// Порожня назва не має давати ім'я, що починається з пробілу.
			Parts{Title: "   ", Episode: 1, Ext: ".ts"},
			"untitled - 01.ts",
		},
	}
	for _, tc := range tests {
		if got := Filename(tc.p); got != tc.want {
			t.Errorf("Filename(%+v) = %q, очікував %q", tc.p, got, tc.want)
		}
	}
}

// Довга назва ріжеться, суфікс — ніколи: без номера серії файл не знайдеться
// ні очима, ні через parseSavedName.
func TestFilenameTruncatesTitleNotSuffix(t *testing.T) {
	p := Parts{
		Title:   strings.Repeat("я", 300),
		Episode: 5,
		Kind:    provider.KindVoiceover,
		Studio:  "FanVoxUA",
		Height:  1080,
		Ext:     ".ts",
	}
	got := Filename(p)
	if len(got) > maxFileBytes {
		t.Fatalf("ім'я %d байт, ліміт %d", len(got), maxFileBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("ім'я обрізане посеред руни: %q", got)
	}
	if !strings.HasSuffix(got, " - 05 - FanVoxUA [Озв, 1080p].ts") {
		t.Fatalf("суфікс постраждав: %q", got)
	}
}

// Коли студія сама з'їдає бюджет, ріжеться і вона — але шматок назви
// лишається, інакше тайтл не впізнати.
func TestFilenameTruncatesStudio(t *testing.T) {
	p := Parts{
		Title:   strings.Repeat("я", 300),
		Episode: 5,
		Kind:    provider.KindVoiceover,
		Studio:  strings.Repeat("ю", 300),
		Height:  1080,
		Ext:     ".ts",
	}
	got := Filename(p)
	if len(got) > maxFileBytes {
		t.Fatalf("ім'я %d байт, ліміт %d", len(got), maxFileBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("ім'я обрізане посеред руни: %q", got)
	}
	if !strings.HasSuffix(got, " [Озв, 1080p].ts") {
		t.Fatalf("суфікс постраждав: %q", got)
	}
	title := strings.SplitN(got, " - ", 2)[0]
	if len(title) < minTitleBytes {
		t.Fatalf("від назви лишилось %d байт (%q), очікував ≥%d", len(title), title, minTitleBytes)
	}
	ep, h, ok := parseSavedName(got)
	if !ok || ep != 5 || h != 1080 {
		t.Fatalf("parseSavedName(%q) = %d, %d, %v", got, ep, h, ok)
	}
}

// Round-trip: що записали в ім'я, те parseSavedName і дістає. Це єдиний шлях
// впізнати файл без sidecar-а, тож він перевіряється на всій таблиці.
func TestFilenameRoundTrip(t *testing.T) {
	variants := []Parts{
		{Episode: 5, Kind: provider.KindVoiceover, Studio: "FanVoxUA", Height: 1080, Ext: ".ts"},
		{Episode: 12, Kind: provider.KindDub, Studio: "Glass Moon", Ext: ".webm"},
		{Episode: 101, Kind: provider.KindSub, Height: 720, Ext: ".mp4"},
		{Episode: 7, Height: 480, Ext: ".ts"},
		{Episode: 1, Ext: ".ts"},
		{Episode: 9, Kind: provider.KindMulti, Studio: "Fate/Zero Team", Height: 360, Ext: ".ts"},
	}
	for _, tc := range sanitiseCases {
		for _, v := range variants {
			p := v
			p.Title = tc.in
			name := Filename(p)
			ep, h, ok := parseSavedName(name)
			if !ok {
				t.Fatalf("parseSavedName(%q) не впізнав ім'я", name)
			}
			if ep != p.Episode || h != p.Height {
				t.Fatalf("parseSavedName(%q) = %d, %d; очікував %d, %d", name, ep, h, p.Episode, p.Height)
			}
		}
	}
}

func TestParseSavedName(t *testing.T) {
	tests := []struct {
		name   string
		ep     int
		height int
		ok     bool
	}{
		{"Фрірен - 05 - FanVoxUA [Озв, 1080p].ts", 5, 1080, true},
		{"Фрірен - 12 - Glass Moon [Озв].webm", 12, 0, true},
		{"Фрірен - 101 [Саб, 720p].ts", 101, 720, true},
		{"Фрірен - 07.mp4", 7, 0, true},
		// Жадібний `.+` віддає останню групу « - NN - »: суфікс ми дописуємо
		// в кінець, тож усе лівіше — назва тайтлу.
		{"Steins - 12 - Gate - 05 - Студія [Озв, 1080p].ts", 5, 1080, true},
		// Відхилення.
		{"Фрірен - 05 - FanVoxUA [Озв, 1080p].ts.part", 0, 0, false},
		{".Фрірен - 05 - FanVoxUA [Озв, 1080p].ts.json", 0, 0, false},
		{".uaanime-title.json", 0, 0, false},
		{"foo.ts", 0, 0, false},
		{"Фрірен.ts", 0, 0, false},
		{"Фрірен - 05 - FanVoxUA [Озв, 1080p].mkv", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range tests {
		ep, h, ok := parseSavedName(tc.name)
		if ok != tc.ok || ep != tc.ep || h != tc.height {
			t.Errorf("parseSavedName(%q) = %d, %d, %v; очікував %d, %d, %v", tc.name, ep, h, ok, tc.ep, tc.height, tc.ok)
		}
	}
}

func TestFolderID(t *testing.T) {
	hex6 := regexp.MustCompile(`^[0-9a-f]{6}$`)
	// Числовий префікс DLE-слага читабельний і вже унікальний у провайдера.
	if got := folderID(provider.TitleRef{Provider: "anitube", Slug: "4465-frren-besmertna-chariwnicya"}); got != "4465" {
		t.Fatalf("folderID = %q, очікував 4465", got)
	}
	nonNum := folderID(provider.TitleRef{Provider: "anitube", Slug: "frieren"})
	if !hex6.MatchString(nonNum) {
		t.Fatalf("folderID = %q, очікував 6 hex", nonNum)
	}
	// Слаг без числового префікса перед дефісом — теж хеш.
	if got := folderID(provider.TitleRef{Provider: "anitube", Slug: "-abc"}); !hex6.MatchString(got) {
		t.Fatalf("folderID = %q, очікував 6 hex", got)
	}
	// Провайдер входить у хеш: однаковий слаг на різних сайтах — різні папки.
	if other := folderID(provider.TitleRef{Provider: "other", Slug: "frieren"}); other == nonNum {
		t.Fatalf("folderID не враховує провайдера: %q", other)
	}
	// Стабільність: id потрапляє в ім'я папки на диску назавжди.
	if again := folderID(provider.TitleRef{Provider: "anitube", Slug: "frieren"}); again != nonNum {
		t.Fatalf("folderID нестабільний: %q != %q", again, nonNum)
	}
}

func TestPath(t *testing.T) {
	p := Parts{Title: "Фрірен", Episode: 5, Kind: provider.KindSub, Height: 720, Ext: ".ts"}
	if got, want := Path("/d", "Фрірен", p), "/d/Фрірен/Фрірен - 05 [Саб, 720p].ts"; got != want {
		t.Fatalf("Path = %q, очікував %q", got, want)
	}
	// FolderFor віддає папку шляхом — він має перекривати dir, а не клеїтись.
	if got, want := Path("/d", "/d/Фрірен", p), "/d/Фрірен/Фрірен - 05 [Саб, 720p].ts"; got != want {
		t.Fatalf("Path(абсолютна папка) = %q, очікував %q", got, want)
	}
}
