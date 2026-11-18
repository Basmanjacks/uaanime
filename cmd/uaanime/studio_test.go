package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/basmanjacks/uaanime/internal/download"
	"github.com/basmanjacks/uaanime/internal/i18n"
	"github.com/basmanjacks/uaanime/internal/playback"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// «Королівство (2 сезон)» — записана фікстура, де сайт не називає студії
// (поле «Переклад: error», плейлист «СУБТИТРИ → ПЛЕЄР ASHDI»).
const noStudioTitleID = "anitube:3733-korolvstvo-2-sezon"

// Реліз без студії видно, його можна обрати й завантажити; файл без частини
// «студія» в імені, sidecar і `downloads` знають його як відомий реліз.
func TestEmptyStudioReleaseEndToEnd(t *testing.T) {
	dataDir, dlDir := anyHostDownloadEnv(t)
	// play читає папку завантажень із налаштувань, а не з --dir.
	cfg := []byte(`{"download_dir":` + strconv.Quote(dlDir) + `}`)
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI(t, "episodes", noStudioTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	var eps []playback.ListedEpisode
	mustJSON(t, out, &eps)
	mustPlayable(t, listedRelease(t, eps, 39, "", provider.KindSub), true)
	if strings.Contains(out, `"error"`) {
		t.Fatalf("службове слово сайту потрапило у студію: %s", out)
	}

	code, out, errOut = runCLI(t, "episodes", noStudioTitleID)
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, "1\t"+i18n.StudioNone+" (sub)\n")

	code, out, errOut = runCLI(t, "resolve", noStudioTitleID, "5", "--kind", "sub", "--json")
	mustExit(t, 0, code, out, errOut)
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	if len(cands) == 0 || cands[0].Studio != "" || cands[0].Kind != provider.KindSub {
		t.Fatalf("resolve: %+v", cands)
	}

	got := downloadedRelease(t, "download", noStudioTitleID, "5", "--dir", dlDir, "--json")
	if got != (provider.Release{Studio: "", Kind: provider.KindSub}) {
		t.Fatalf("download: реліз %+v", got)
	}
	files := download.Saved(dlDir, provider.TitleRef{Provider: "anitube", Slug: "3733-korolvstvo-2-sezon"})[5]
	if len(files) != 1 {
		t.Fatalf("збережено %+v, очікував один файл", files)
	}
	f := files[0]
	if base := filepath.Base(f.Path); !strings.HasPrefix(base, "Королівство (2 сезон) - 05 [Саб, ") {
		t.Fatalf("ім'я файла = %q, очікував без частини «студія»", base)
	}
	if !f.Known || f.Studio != "" || f.Kind != provider.KindSub {
		t.Fatalf("SavedFile = %+v", f)
	}

	// Перегляд бере збережений файл: реліз без студії зіставляється з sidecar-ом.
	code, out, errOut = runCLI(t, "play", noStudioTitleID, "5", "--dry-run")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, f.Path)
	mustContain(t, "stdout", out, i18n.StudioNone)

	// Повтор — той самий реліз уже збережено, а не новий файл поруч.
	code, out, errOut = runCLI(t, "download", noStudioTitleID, "5", "--dir", dlDir, "--json")
	mustExit(t, 1, code, out, errOut)
	if e := jsonErrorOf(t, out, errOut); e.Code != "already_saved" {
		t.Fatalf("повтор: %+v", e)
	}

	code, out, errOut = runCLI(t, "downloads", "--dir", dlDir, "--json")
	mustExit(t, 0, code, out, errOut)
	var listing []downloadsTitle
	mustJSON(t, out, &listing)
	if len(listing) != 1 || len(listing[0].Episodes) != 1 {
		t.Fatalf("downloads: %s", out)
	}
	if file := listing[0].Episodes[0].Files[0]; !file.Known || file.Studio != "" || file.Kind != provider.KindSub {
		t.Fatalf("downloads: файл %+v", file)
	}
}

// Етап 4.2 наскрізно: `--studio` кирилицею знаходить студію, яку сайт пише
// латиницею (FANVOXUA у фікстурі Фрірен), і збережений файл, чий sidecar
// записав кириличну «А», — той самий реліз: повтор не качає його вдруге.
func TestStudioHomoglyphsEndToEnd(t *testing.T) {
	_, dlDir := anyHostDownloadEnv(t)
	const site, cyrillic = "FANVOXUA", "FANVOXU\u0410"

	code, out, errOut := runCLI(t, "resolve", fixtureTitleID, "1", "--studio", "FanVoxU\u0410", "--json")
	mustExit(t, 0, code, out, errOut)
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	if len(cands) == 0 {
		t.Fatal("resolve --studio кирилицею: порожньо")
	}
	for _, c := range cands {
		if c.Studio != site {
			t.Fatalf("resolve --studio кирилицею: %+v (показ має лишитися як на сайті)", c)
		}
	}

	ref := fixtureRef(t)
	folder := filepath.Join(dlDir, "Фрірен")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := download.WriteMarker(folder, download.Marker{Provider: ref.Provider, Slug: ref.Slug}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "Фрірен - 01 - "+cyrillic+" [Озв, 1080p].ts")
	if err := os.WriteFile(path, []byte("медіа"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download.WriteSidecar(path, download.Sidecar{Provider: ref.Provider, Slug: ref.Slug, Episode: 1,
		Studio: cyrillic, Kind: provider.KindVoiceover, Height: 1080}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runCLI(t, "download", fixtureTitleID, "1", "--studio", "FanVoxUA", "--kind", "voiceover",
		"--quality", "1080", "--dir", dlDir, "--json")
	mustExit(t, 1, code, out, errOut)
	if e := jsonErrorOf(t, out, errOut); e.Code != "already_saved" {
		t.Fatalf("повтор з іншим написанням студії: %+v", e)
	}
	if files := download.Saved(dlDir, ref)[1]; len(files) != 1 {
		t.Fatalf("на диску %+v, очікував лише наявний файл", files)
	}
}
