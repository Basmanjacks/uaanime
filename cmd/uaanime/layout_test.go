package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/basmanjacks/uaanime/internal/download"
	"github.com/basmanjacks/uaanime/internal/downloadtest"
	"github.com/basmanjacks/uaanime/internal/i18n"
)

// Наскрізні сценарії `download --layout plex` на фікстурах: два тайтли-сезони
// «Фрірен» (1 і 2 сезон) мають лягти в одну теку серіалу.

const (
	fixtureSeason2ID = "anitube:5663-frren-scho-provodzhaye-v-ostannyu-put-2-sezon"
	// fixtureShowName — назва серіалу без позначки сезону.
	fixtureShowName = "Фрірен, що проводжає в останню путь"
)

// rePlexFile — «<Серіал> - S01E01 - <Студія> [<Тип>, 720p].ts».
func rePlexFile(season string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(fixtureShowName) + ` - S` + season + `E01 - [^/]+ \[[^\]]+, 720p\]\.ts$`)
}

func downloadJSON(t *testing.T, args ...string) downloadResult {
	t.Helper()
	code, out, errOut := runCLI(t, append([]string{"download"}, append(args, "--json")...)...)
	if code != 0 {
		t.Fatalf("download %v = %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	var res downloadResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("розбір %q: %v", out, err)
	}
	return res
}

func TestDownloadLayoutPlex(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	show := filepath.Join(dlDir, fixtureShowName)

	s1 := downloadJSON(t, fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--layout", "plex")
	season1 := filepath.Join(show, "Season 01")
	if filepath.Dir(s1.Path) != season1 || !rePlexFile("01").MatchString(filepath.Base(s1.Path)) {
		t.Fatalf("шлях = %q, очікував %s/%s - S01E01 - …", s1.Path, season1, fixtureShowName)
	}
	// Sidecar поруч із файлом, marker — у теці сезону, тека серіалу без marker-а.
	if _, err := os.Stat(filepath.Join(season1, "."+filepath.Base(s1.Path)+".json")); err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	if m, ok := download.ReadMarker(season1); !ok || m.Name != fixtureTitleName {
		t.Fatalf("marker теки сезону = %+v, %v", m, ok)
	}
	if _, ok := download.ReadMarker(show); ok {
		t.Fatal("тека серіалу має marker")
	}

	// Повтор — «вже збережено», нічого не качається.
	code, _, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--layout=plex", "--json")
	if code != 1 || !strings.Contains(errOut, `"already_saved"`) {
		t.Fatalf("повтор = %d, stderr %s", code, errOut)
	}

	// Другий тайтл — 2 сезон того самого серіалу: та сама тека серіалу.
	s2 := downloadJSON(t, fixtureSeason2ID, "1", "--quality", "720", "--dir", dlDir, "--layout", "plex")
	season2 := filepath.Join(show, "Season 02")
	if filepath.Dir(s2.Path) != season2 || !rePlexFile("02").MatchString(filepath.Base(s2.Path)) {
		t.Fatalf("шлях 2 сезону = %q, очікував у %s", s2.Path, season2)
	}

	// downloads --json бачить обидва тайтли з їхніми теками сезонів.
	code, out, errOut := runCLI(t, "downloads", "--dir", dlDir, "--json")
	if code != 0 {
		t.Fatalf("downloads = %d, stderr %s", code, errOut)
	}
	var listing []downloadsTitle
	if err := json.Unmarshal([]byte(out), &listing); err != nil {
		t.Fatalf("розбір downloads: %v", err)
	}
	want := map[string]struct{ folder, path string }{
		fixtureTitleID:   {season1, s1.Path},
		fixtureSeason2ID: {season2, s2.Path},
	}
	if len(listing) != len(want) {
		t.Fatalf("downloads = %+v", listing)
	}
	for _, title := range listing {
		w, ok := want[title.ID]
		if !ok || len(title.Folders) != 1 || title.Folders[0] != w.folder ||
			len(title.Episodes) != 1 || len(title.Episodes[0].Files) != 1 || title.Episodes[0].Files[0].Path != w.path || !title.Episodes[0].Files[0].Known {
			t.Fatalf("тайтл %s = %+v, очікував теку %s і файл %s", title.ID, title, w.folder, w.path)
		}
	}
	// Нічого зайвого: ані .part, ані теки поза теками сезонів.
	for _, name := range walkDir(t, dlDir) {
		if strings.Contains(name, ".part") {
			t.Fatalf("лишився %s", name)
		}
		if !strings.HasPrefix(name, fixtureShowName+string(filepath.Separator)+"Season 0") && !strings.HasPrefix(name, ".uaanime") {
			t.Fatalf("файл поза теками сезонів: %s", name)
		}
	}
}

// Типова розкладка не змінилася: без прапорця і з --layout default — той
// самий шлях, що й до етапу 5. Plex-файл того самого релізу її не блокує.
func TestDownloadLayoutDefaultUnchanged(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	reDefault := regexp.MustCompile(`^` + regexp.QuoteMeta(fixtureTitleName) + ` - 01 - [^/]+ \[[^\]]+, 720p\]\.ts$`)

	plex := downloadJSON(t, fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--layout", "plex")
	def := downloadJSON(t, fixtureTitleID, "1", "--quality", "720", "--dir", dlDir)
	folder := filepath.Join(dlDir, fixtureTitleName)
	if filepath.Dir(def.Path) != folder || !reDefault.MatchString(filepath.Base(def.Path)) {
		t.Fatalf("типовий шлях = %q, очікував %s/%s - 01 - …", def.Path, folder, fixtureTitleName)
	}
	if def.Studio != plex.Studio || def.Kind != plex.Kind || def.Height != plex.Height {
		t.Fatalf("реліз різниться: %+v проти %+v", def, plex)
	}
	if m, ok := download.ReadMarker(folder); !ok || m.Name != fixtureTitleName {
		t.Fatalf("marker = %+v, %v", m, ok)
	}

	code, _, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--layout", "default")
	if code != 1 {
		t.Fatalf("--layout default повтор = %d, want 1", code)
	}
	mustContain(t, "stderr", errOut, i18n.MsgAlreadySaved)

	if files := download.Saved(dlDir, fixtureRef(t))[1]; len(files) != 2 {
		t.Fatalf("збережено %d файлів, очікував 2", len(files))
	}
}

// Чужа «Season 01» (бібліотека Plex, файли без sidecar-ів) не чіпається:
// uaanime бере «Season 01 [id]». Чужі файли в самій теці серіалу не заважають.
func TestDownloadLayoutPlexForeignSeason(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	show := filepath.Join(dlDir, fixtureShowName)
	foreign := filepath.Join(show, "Season 01")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(foreign, "Frieren S01E01.mkv"), filepath.Join(show, "poster.jpg")} {
		if err := os.WriteFile(p, []byte("чуже"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := downloadJSON(t, fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--layout", "plex")
	if want := filepath.Join(show, "Season 01 [4465]"); filepath.Dir(res.Path) != want {
		t.Fatalf("тека = %q, очікував %q", filepath.Dir(res.Path), want)
	}
	ents, err := os.ReadDir(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != "Frieren S01E01.mkv" {
		t.Fatalf("у чужу теку записано: %v", ents)
	}
	if data, err := os.ReadFile(filepath.Join(show, "poster.jpg")); err != nil || string(data) != "чуже" {
		t.Fatalf("чужий файл теки серіалу змінено: %q, %v", data, err)
	}
	if _, ok := download.ReadMarker(show); ok {
		t.Fatal("тека серіалу отримала marker")
	}
}

// Невідома розкладка — помилка вжитку ще до мережі.
func TestDownloadLayoutUsage(t *testing.T) {
	cliEnv(t)
	for _, args := range [][]string{
		{"download", fixtureTitleID, "1", "--layout", "kodi", "--json"},
		{"download", fixtureTitleID, "1", "--layout", "--json"},
		{"download", fixtureTitleID, "1", "--layout=", "--json"},
	} {
		code, out, errOut := runCLI(t, args...)
		if code != 2 || out != "" || !strings.Contains(errOut, `"usage"`) {
			t.Fatalf("%v = %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
		}
	}
	mustContain(t, "usage", i18n.MsgUsage, "--layout")
}

// Збережене в plex-розкладці грає з диска так само, як і в типовій.
func TestPlayDryRunUsesPlexFile(t *testing.T) {
	dataDir, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	cfg := []byte(`{"download_dir":` + strconv.Quote(dlDir) + `}`)
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatalf("config.json: %v", err)
	}
	res := downloadJSON(t, fixtureTitleID, "1", "--quality", "1080", "--layout", "plex")
	if !strings.Contains(res.Path, string(filepath.Separator)+"Season 01"+string(filepath.Separator)) {
		t.Fatalf("шлях = %q", res.Path)
	}
	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	if code != 0 {
		t.Fatalf("play --dry-run = %d\nstderr: %s", code, errOut)
	}
	argv := lastLine(out)
	mustContain(t, "argv", argv, res.Path)
	if strings.Contains(argv, "http://") || strings.Contains(argv, "https://") {
		t.Fatalf("argv тягне з мережі: %s", argv)
	}
}
