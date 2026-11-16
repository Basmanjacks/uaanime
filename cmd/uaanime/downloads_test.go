package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// putSaved кладе медіафайл (і, якщо sc != nil, його sidecar) з фіксованим
// часом зміни — щоб вивід порівнювався повністю.
func putSaved(t *testing.T, path string, size int, sc *download.Sidecar, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	if sc != nil {
		if err := download.WriteSidecar(path, *sc); err != nil {
			t.Fatal(err)
		}
	}
}

// downloadsFixture: два тайтли з marker-ами (порядок назв навмисно не той, що
// порядок створення), у «Псайрен» серія 6 — два релізи, серія 2 — файл без
// sidecar-а; поруч чужа тека без marker-а з файлом, схожим на наш.
func downloadsFixture(t *testing.T) (dir string, mod time.Time) {
	t.Helper()
	dir = t.TempDir()
	mod = time.Date(2026, 11, 16, 21, 9, 58, 0, time.FixedZone("EET", 2*3600))
	psay := provider.TitleRef{Provider: "anitube", Slug: "6108-psay", Name: "Псайрен"}
	black := provider.TitleRef{Provider: "anitube", Slug: "6000-chorna", Name: "Чорна конюшина"}

	pf := filepath.Join(dir, "Псайрен")
	if err := os.MkdirAll(pf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := download.WriteMarker(pf, download.Marker{Provider: psay.Provider, Slug: psay.Slug, Name: psay.Name}); err != nil {
		t.Fatal(err)
	}
	sc := func(ref provider.TitleRef, ep int, studio string, kind provider.Kind, h int) *download.Sidecar {
		return &download.Sidecar{Provider: ref.Provider, Slug: ref.Slug, Name: ref.Name, Episode: ep, Studio: studio, Kind: kind, Height: h}
	}
	putSaved(t, filepath.Join(pf, "Псайрен - 06 - Рідний Голос [Озв, 1080p].ts"), 30, sc(psay, 6, "Рідний Голос", provider.KindVoiceover, 1080), mod)
	putSaved(t, filepath.Join(pf, "Псайрен - 06 - FanVoxUA [Озв, 720p].ts"), 20, sc(psay, 6, "FanVoxUA", provider.KindVoiceover, 720), mod)
	putSaved(t, filepath.Join(pf, "Псайрен - 02 - Хтось [Саб, 480p].ts"), 10, nil, mod)

	bf := filepath.Join(dir, "Чорна конюшина")
	if err := os.MkdirAll(bf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := download.WriteMarker(bf, download.Marker{Provider: black.Provider, Slug: black.Slug, Name: black.Name}); err != nil {
		t.Fatal(err)
	}
	putSaved(t, filepath.Join(bf, "Чорна конюшина - 03 - FanVoxUА [Озв, 1080p].ts"), 40, sc(black, 3, "FanVoxUА", provider.KindVoiceover, 1080), mod)

	// Чужа тека: файл за нашим шаблоном імені, але ні marker-а, ні sidecar-а.
	putSaved(t, filepath.Join(dir, "Чуже", "Чуже - 01 - X [Озв, 1080p].ts"), 50, nil, mod)
	return dir, mod
}

func TestDownloadsJSON(t *testing.T) {
	cliEnv(t)
	dir, _ := downloadsFixture(t)

	code, out, errOut := runCLI(t, "downloads", "--dir", dir, "--json")
	mustExit(t, 0, code, out, errOut)
	var got []downloadsTitle
	mustJSON(t, out, &got)

	pf, bf := filepath.Join(dir, "Псайрен"), filepath.Join(dir, "Чорна конюшина")
	const modified = "2026-11-16T19:09:58Z"
	want := []downloadsTitle{
		{ID: "anitube:6108-psay", Name: "Псайрен", Folders: []string{pf}, Episodes: []downloadsEpisode{
			{Number: 2, Files: []downloadsFile{
				{Path: filepath.Join(pf, "Псайрен - 02 - Хтось [Саб, 480p].ts"), Height: 480, Bytes: 10, Modified: modified},
			}},
			{Number: 6, Files: []downloadsFile{
				{Path: filepath.Join(pf, "Псайрен - 06 - FanVoxUA [Озв, 720p].ts"), Studio: "FanVoxUA", Kind: provider.KindVoiceover, Height: 720, Bytes: 20, Modified: modified, Known: true},
				{Path: filepath.Join(pf, "Псайрен - 06 - Рідний Голос [Озв, 1080p].ts"), Studio: "Рідний Голос", Kind: provider.KindVoiceover, Height: 1080, Bytes: 30, Modified: modified, Known: true},
			}},
		}},
		{ID: "anitube:6000-chorna", Name: "Чорна конюшина", Folders: []string{bf}, Episodes: []downloadsEpisode{
			{Number: 3, Files: []downloadsFile{
				{Path: filepath.Join(bf, "Чорна конюшина - 03 - FanVoxUА [Озв, 1080p].ts"), Studio: "FanVoxUА", Kind: provider.KindVoiceover, Height: 1080, Bytes: 40, Modified: modified, Known: true},
			}},
		}},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("downloads --json:\n got %s\nwant %s", gotJSON, wantJSON)
	}
	if strings.Contains(out, "Чуже") {
		t.Fatalf("чужа тека потрапила у вивід: %s", out)
	}

	// Ключі файла — рівно ті, що в контракті, навіть для невідомого файла.
	var raw []struct {
		Episodes []struct {
			Files []map[string]json.RawMessage `json:"files"`
		} `json:"episodes"`
	}
	mustJSON(t, out, &raw)
	keys := keysOf(raw[0].Episodes[0].Files[0])
	slices.Sort(keys)
	if strings.Join(keys, ",") != "bytes,height,kind,known,modified,path,studio" {
		t.Fatalf("поля файла = %v", keys)
	}

	// Стабільність: другий виклик дає байт-у-байт той самий вивід.
	_, again, _ := runCLI(t, "downloads", "--dir", dir, "--json")
	if again != out {
		t.Fatalf("вивід нестабільний:\n%s\n---\n%s", out, again)
	}
}

func TestDownloadsText(t *testing.T) {
	cliEnv(t)
	dir, _ := downloadsFixture(t)
	code, out, errOut := runCLI(t, "downloads", "--dir", dir)
	mustExit(t, 0, code, out, errOut)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("рядків %d, want 4 (по одному на файл):\n%s", len(lines), out)
	}
	pf := filepath.Join(dir, "Псайрен")
	if want := "anitube:6108-psay\t2\t" + i18n.MsgDownloadsUnknownRelease + "\t480p\t" + filepath.Join(pf, "Псайрен - 02 - Хтось [Саб, 480p].ts"); lines[0] != want {
		t.Fatalf("рядок 1 = %q, want %q", lines[0], want)
	}
	if want := "anitube:6108-psay\t6\tFanVoxUA (voiceover)\t720p\t" + filepath.Join(pf, "Псайрен - 06 - FanVoxUA [Озв, 720p].ts"); lines[1] != want {
		t.Fatalf("рядок 2 = %q, want %q", lines[1], want)
	}
}

// Порожня й відсутня папка — не помилка; відносний --dir — помилка вжитку.
func TestDownloadsEmptyAndUsage(t *testing.T) {
	cliEnv(t)
	missing := filepath.Join(t.TempDir(), "nope")

	code, out, errOut := runCLI(t, "downloads", "--dir", missing, "--json")
	mustExit(t, 0, code, out, errOut)
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("відсутня папка: %q, want []", out)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("downloads створив папку: %v", err)
	}

	code, out, errOut = runCLI(t, "downloads", "--dir", missing)
	mustExit(t, 0, code, out, errOut)
	if out != i18n.MsgDownloadsEmpty+"\n" {
		t.Fatalf("порожній текст: %q", out)
	}

	code, out, errOut = runCLI(t, "downloads", "--dir", "relative/path", "--json")
	mustExit(t, 2, code, out, errOut)
	if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeUsage {
		t.Fatalf("відносний --dir: %+v", got)
	}

	code, out, errOut = runCLI(t, "downloads", "extra", "--json")
	mustExit(t, 2, code, out, errOut)
	if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeUsage {
		t.Fatalf("зайвий аргумент: %+v", got)
	}
}
