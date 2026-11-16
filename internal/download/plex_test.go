package download

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Тести Plex-розкладки: «<Серіал>/Season NN/<Серіал> - SNNEMM - …».

var refSeason2 = provider.TitleRef{Provider: "anitube", Slug: "5663-frieren-2", Name: "Фрірен (2 сезон)"}

func TestSplitSeason(t *testing.T) {
	tests := []struct {
		in     string
		show   string
		season int
	}{
		// Назви з ТЗ.
		{"Код Ґіасc: Повстання Лелуша 2 (2 сезон)", "Код Ґіасc: Повстання Лелуша 2", 2},
		{"Чорна конюшина (2 сезон)", "Чорна конюшина", 2},
		{"Фрірен, що проводжає в останню путь (1 сезон)", "Фрірен, що проводжає в останню путь", 1},
		{"Королівство (2 сезон)", "Королівство", 2},
		{"Крісло-танк", "Крісло-танк", 1},
		{"86", "86", 1},
		// Усі форми позначки.
		{"Мій сусід Season 3", "Мій сусід", 3},
		{"Мій сусід (Season 3)", "Мій сусід", 3},
		{"Щось S2", "Щось", 2},
		{"Щось S02", "Щось", 2},
		{"Щось s12", "Щось", 12},
		{"Щось 2 сезон", "Щось", 2},
		{"Щось сезон 4", "Щось", 4},
		{"Щось (сезон 4)", "Щось", 4},
		{"Щось (Сезон 4)", "Щось", 4},
		{"Щось ( 3 сезон )", "Щось", 3},
		{"Щось(2 сезон)", "Щось", 2},
		{"Щось 2 сезон", "Щось", 2},
		{"Щось (99 сезон)", "Щось", 99},
		// Розділові знаки перед позначкою зникають разом із нею.
		{"Назва: Сезон 2", "Назва", 2},
		{"Назва - S2", "Назва", 2},
		{"Назва — 3 сезон", "Назва", 3},
		{"  Назва (2 сезон)  ", "Назва", 2},
		// Лише одна позначка, і лише в кінці.
		{"Назва 2 (3 сезон)", "Назва 2", 3},
		{"Назва (2 сезон) OVA", "Назва (2 сезон) OVA", 1},
		{"Сезон полювання", "Сезон полювання", 1},
		// Не сезони.
		{"Щось (0 сезон)", "Щось (0 сезон)", 1},
		{"Щось S0", "Щось S0", 1},
		{"Щось S00", "Щось S00", 1},
		{"Щось (100 сезон)", "Щось (100 сезон)", 1},
		{"Щось 100 сезон", "Щось 100 сезон", 1},
		{"Щось S100", "Щось S100", 1},
		{"ЩосьS2", "ЩосьS2", 1},
		{"Щось 2", "Щось 2", 1},
		{"Steins;Gate 0", "Steins;Gate 0", 1},
		// Назва — сама позначка: лишається цілою.
		{"2 сезон", "2 сезон", 1},
		{"(2 сезон)", "(2 сезон)", 1},
		{"Season 2", "Season 2", 1},
		{"S2", "S2", 1},
		{"", "", 1},
	}
	for _, tc := range tests {
		show, season := SplitSeason(tc.in)
		if show != tc.show || season != tc.season {
			t.Errorf("SplitSeason(%q) = (%q, %d), очікував (%q, %d)", tc.in, show, season, tc.show, tc.season)
		}
	}
}

func TestParseLayout(t *testing.T) {
	for in, want := range map[string]Layout{"default": LayoutDefault, "plex": LayoutPlex} {
		if got, ok := ParseLayout(in); !ok || got != want {
			t.Errorf("ParseLayout(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "Plex", "kodi", "plex "} {
		if _, ok := ParseLayout(in); ok {
			t.Errorf("ParseLayout(%q) прийнято", in)
		}
	}
}

func TestFilenamePlex(t *testing.T) {
	tests := []struct {
		p    Parts
		want string
	}{
		{Parts{Title: "Королівство", Season: 2, Episode: 5, Kind: provider.KindDub, Studio: "FanVoxUA", Height: 1080, Ext: ".ts"},
			"Королівство - S02E05 - FanVoxUA [Дуб, 1080p].ts"},
		{Parts{Title: "Королівство", Season: 2, Episode: 5, Kind: provider.KindVoiceover, Height: 1080, Ext: ".ts"},
			"Королівство - S02E05 [Озв, 1080p].ts"},
		{Parts{Title: "Ван Піс", Season: 1, Episode: 1105, Kind: provider.KindSub, Studio: "X", Height: 720, Ext: ".mp4"},
			"Ван Піс - S01E1105 - X [Саб, 720p].mp4"},
	}
	for _, tc := range tests {
		got := Filename(tc.p)
		if got != tc.want {
			t.Errorf("Filename(%+v) = %q, очікував %q", tc.p, got, tc.want)
		}
		ep, h, ok := parseSavedName(got)
		if !ok || ep != tc.p.Episode || h != tc.p.Height {
			t.Errorf("parseSavedName(%q) = %d, %d, %v", got, ep, h, ok)
		}
	}
	for name, wantEp := range map[string]int{
		"Щось - S02E07.ts":                   7,
		"Щось - S02E07 [Озв].webm":           7,
		"Steins - 12 - Gate - S03E04 - X.ts": 4,
	} {
		if ep, h, ok := parseSavedName(name); !ok || ep != wantEp || h != 0 {
			t.Errorf("parseSavedName(%q) = %d, %d, %v; очікував серію %d", name, ep, h, ok, wantEp)
		}
	}
}

// Два тайтли-сезони однієї назви: одна тека серіалу без marker-а, у ній
// різні «Season NN» зі своїми marker-ами.
func TestPlexFolderForSeasonsShareShow(t *testing.T) {
	dir := t.TempDir()
	s1, err := PlexFolderFor(dir, refFrieren, "Фрірен", 1)
	if err != nil {
		t.Fatalf("PlexFolderFor 1: %v", err)
	}
	s2, err := PlexFolderFor(dir, refSeason2, "Фрірен", 2)
	if err != nil {
		t.Fatalf("PlexFolderFor 2: %v", err)
	}
	show := filepath.Join(dir, "Фрірен")
	if s1 != filepath.Join(show, "Season 01") || s2 != filepath.Join(show, "Season 02") {
		t.Fatalf("теки = %q, %q", s1, s2)
	}
	if m, ok := ReadMarker(show); ok {
		t.Fatalf("тека серіалу має marker %+v", m)
	}
	if m, ok := ReadMarker(s1); !ok || m.Slug != refFrieren.Slug {
		t.Fatalf("marker Season 01 = %+v, %v", m, ok)
	}
	if m, ok := ReadMarker(s2); !ok || m.Slug != refSeason2.Slug {
		t.Fatalf("marker Season 02 = %+v, %v", m, ok)
	}
	// Повторно — та сама тека за marker-ом, навіть якщо назва змінилася.
	again, err := PlexFolderFor(dir, refFrieren, "Frieren", 1)
	if err != nil || again != s1 {
		t.Fatalf("повторний PlexFolderFor = %q, %v; очікував %q", again, err, s1)
	}
	// Типова розкладка теки сезонів не бачить і не чіпає.
	def, err := FolderFor(dir, refFrieren, "Фрірен (1 сезон)")
	if err != nil || def != filepath.Join(dir, "Фрірен (1 сезон)") {
		t.Fatalf("FolderFor = %q, %v", def, err)
	}
}

// Чужа тека сезону — з marker-ом іншого тайтлу або з чужими файлами без
// sidecar-ів — не усиновлюється: беремо «Season NN [id]».
func TestPlexFolderForSkipsForeignSeason(t *testing.T) {
	cases := map[string]func(t *testing.T, season string){
		"чужий marker": func(t *testing.T, season string) { mustMarker(t, season, refOther) },
		"чужий файл без sidecar-а": func(t *testing.T, season string) {
			writeMedia(t, season, "Frieren S01E01.mkv", 10, nil)
		},
		"чужий sidecar": func(t *testing.T, season string) {
			writeMedia(t, season, "Фрірен - S01E01 [Озв, 1080p].ts", 10, sidecarFor(refOther, 1, "", "", 1080))
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			taken := filepath.Join(dir, "Фрірен", "Season 01")
			prepare(t, taken)
			before := entries(t, taken)

			got, err := PlexFolderFor(dir, refFrieren, "Фрірен", 1)
			if err != nil {
				t.Fatalf("PlexFolderFor: %v", err)
			}
			if want := filepath.Join(dir, "Фрірен", "Season 01 ["+folderID(refFrieren)+"]"); got != want {
				t.Fatalf("тека = %q, очікував %q", got, want)
			}
			if after := entries(t, taken); len(after) != len(before) {
				t.Fatalf("у чужу теку записано: було %v, стало %v", before, after)
			}
		})
	}
}

// Тека серіалу з чужими файлами без marker-а придатна: пишемо лише в теку
// сезону. Своя тека сезону без marker-а (sidecar-и цього ref) усиновлюється.
func TestPlexFolderForUsesForeignShowFolder(t *testing.T) {
	dir := t.TempDir()
	show := filepath.Join(dir, "Фрірен")
	writeMedia(t, show, "poster.jpg", 10, nil)
	own := filepath.Join(show, "Season 02")
	writeMedia(t, own, "Фрірен - S02E01 [Озв, 1080p].ts", 10, sidecarFor(refSeason2, 1, "", provider.KindVoiceover, 1080))

	s1, err := PlexFolderFor(dir, refFrieren, "Фрірен", 1)
	if err != nil || s1 != filepath.Join(show, "Season 01") {
		t.Fatalf("PlexFolderFor 1 = %q, %v", s1, err)
	}
	s2, err := PlexFolderFor(dir, refSeason2, "Фрірен", 2)
	if err != nil || s2 != own {
		t.Fatalf("PlexFolderFor 2 = %q, %v; очікував %q", s2, err, own)
	}
	if m, ok := ReadMarker(own); !ok || m.Slug != refSeason2.Slug {
		t.Fatalf("marker усиновленої теки = %+v, %v", m, ok)
	}
	if _, ok := ReadMarker(show); ok {
		t.Fatal("тека серіалу отримала marker")
	}
}

// Тека з іменем серіалу — тека тайтлу типової розкладки: своя придатна,
// чужа — ні (тоді «<Серіал> [id]»).
func TestPlexFolderForShowFolderWithMarker(t *testing.T) {
	dir := t.TempDir()
	mustMarker(t, filepath.Join(dir, "Крісло-танк"), refFrieren)
	got, err := PlexFolderFor(dir, refFrieren, "Крісло-танк", 1)
	if err != nil || got != filepath.Join(dir, "Крісло-танк", "Season 01") {
		t.Fatalf("своя тека: %q, %v", got, err)
	}
	got, err = PlexFolderFor(dir, refOther, "Крісло-танк", 1)
	if want := filepath.Join(dir, "Крісло-танк ["+folderID(refOther)+"]", "Season 01"); err != nil || got != want {
		t.Fatalf("чужа тека: %q, %v; очікував %q", got, err, want)
	}
}

// Типова розкладка не усиновлює теку серіалу, де лежить сезон іншого тайтлу.
func TestFolderForSkipsShowWithForeignSeason(t *testing.T) {
	dir := t.TempDir()
	if _, err := PlexFolderFor(dir, refOther, "Фрірен", 2); err != nil {
		t.Fatalf("PlexFolderFor: %v", err)
	}
	got, err := FolderFor(dir, refFrieren, "Фрірен")
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	if want := filepath.Join(dir, "Фрірен ["+folderID(refFrieren)+"]"); got != want {
		t.Fatalf("тека = %q, очікував %q", got, want)
	}
}

// Index бачить обидві розкладки, а з підпапок тек тайтлів — лише «Season NN».
func TestIndexFindsPlexLayout(t *testing.T) {
	dir := t.TempDir()
	def := filepath.Join(dir, "Фрірен (1 сезон)")
	mustMarker(t, def, refFrieren)
	writeMedia(t, def, "Фрірен (1 сезон) - 01 [Озв, 1080p].ts", 10, sidecarFor(refFrieren, 1, "", provider.KindVoiceover, 1080))

	s1 := filepath.Join(dir, "Фрірен", "Season 01")
	mustMarker(t, s1, refFrieren)
	writeMedia(t, s1, "Фрірен - S01E01 [Озв, 1080p].ts", 10, sidecarFor(refFrieren, 1, "", provider.KindVoiceover, 1080))
	// Без sidecar-а: серію дає ім'я, ref — marker теки сезону.
	writeMedia(t, s1, "Фрірен - S01E02 [Озв, 720p].ts", 10, nil)
	s2 := filepath.Join(dir, "Фрірен", "Season 02 [5663]")
	mustMarker(t, s2, refSeason2)
	writeMedia(t, s2, "Фрірен - S02E03.ts", 10, nil)
	// Не тека сезону — не читається, хай навіть із sidecar-ом.
	writeMedia(t, filepath.Join(dir, "Фрірен", "Extras"), "x.ts", 10, sidecarFor(refFrieren, 9, "", "", 0))
	// Третій рівень — теж ні.
	writeMedia(t, filepath.Join(s1, "Season 01"), "y.ts", 10, sidecarFor(refFrieren, 8, "", "", 0))

	idx := Index(dir)
	t1 := idx[RefKey(refFrieren)]
	if len(t1.Files) != 2 || len(t1.Files[1]) != 2 || len(t1.Files[2]) != 1 {
		t.Fatalf("файли Фрірен = %+v", t1.Files)
	}
	if t1.Files[2][0].Height != 720 || t1.Files[2][0].Known || t1.Files[2][0].Layout != LayoutPlex {
		t.Fatalf("файл без sidecar-а = %+v", t1.Files[2][0])
	}
	layouts := map[Layout]bool{}
	for _, f := range t1.Files[1] {
		layouts[f.Layout] = true
	}
	if !layouts[LayoutDefault] || !layouts[LayoutPlex] {
		t.Fatalf("розкладки серії 1 = %v", layouts)
	}
	// Порядок — обхід за алфавітом: «Фрірен/Season 01» раніше за «Фрірен (1 сезон)».
	if len(t1.Folders) != 2 || t1.Folders[0] != s1 || t1.Folders[1] != def {
		t.Fatalf("Folders = %v", t1.Folders)
	}
	t2 := idx[RefKey(refSeason2)]
	if len(t2.Files[3]) != 1 || len(t2.Folders) != 1 || t2.Folders[0] != s2 {
		t.Fatalf("сезон 2 = %+v", t2)
	}
	if len(idx) != 2 {
		t.Fatalf("Index = %d тайтлів", len(idx))
	}
}

// Завдання з Layout=plex: шлях, sidecar і marker теки сезону; повтор —
// «вже збережено»; файл типової розкладки plex-завантаження не блокує.
func TestRunPlexLayout(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))
	ref := provider.TitleRef{Provider: "anitube", Slug: "3733-korolevstvo-2", Name: "Королівство (2 сезон)"}

	def, err := run(t.Context(), f, jobFor(dir, ref, 5, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("типова розкладка: %v", err)
	}
	if want := filepath.Join(dir, "Королівство (2 сезон)", "Королівство (2 сезон) - 05 - FanVoxUA [Дуб, 720p].ts"); def.Path != want {
		t.Fatalf("типовий шлях = %q, очікував %q", def.Path, want)
	}

	job := jobFor(dir, ref, 5, 720, s.URL())
	job.Layout = LayoutPlex
	p, err := run(t.Context(), f, job, nil)
	if err != nil {
		t.Fatalf("plex: %v", err)
	}
	want := filepath.Join(dir, "Королівство", "Season 02", "Королівство - S02E05 - FanVoxUA [Дуб, 720p].ts")
	if p.Path != want {
		t.Fatalf("plex-шлях = %q, очікував %q", p.Path, want)
	}
	if sc, ok := readSidecar(p.Path); !ok || sc.Name != ref.Name || sc.Episode != 5 || sc.Height != 720 {
		t.Fatalf("sidecar = %+v, %v", sc, ok)
	}
	if m, ok := ReadMarker(filepath.Dir(p.Path)); !ok || m.Slug != ref.Slug {
		t.Fatalf("marker = %+v, %v", m, ok)
	}
	for _, name := range entries(t, filepath.Dir(p.Path)) {
		if filepath.Ext(name) == partSuffix {
			t.Fatalf(".part лишився: %s", name)
		}
	}
	if _, err := os.Stat(def.Path); err != nil {
		t.Fatalf("файл типової розкладки зник: %v", err)
	}

	_, err = run(t.Context(), f, job, nil)
	if !errors.Is(err, errs.ErrAlreadySaved) {
		t.Fatalf("повтор plex = %v, очікував ErrAlreadySaved", err)
	}
	if files := Saved(dir, ref)[5]; len(files) != 2 {
		t.Fatalf("Saved = %+v, очікував два файли", files)
	}
}
