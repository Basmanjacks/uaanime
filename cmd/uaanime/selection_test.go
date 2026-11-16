package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/provider/anitube"
)

// anyHostDownloadEnv — downloadEnv, але стенд не звіряє Referer: вибір релізу
// може впасти на будь-який хост фікстур (ashdi, moonanime, tortuga), а тут
// перевіряється саме вибір, а не заголовки конкретного хоста.
func anyHostDownloadEnv(t *testing.T) (dataDir, dlDir string) {
	t.Helper()
	dataDir = cliEnv(t)
	hls := downloadtest.NewHLS(t, downloadtest.HLSOptions{Host: "s.moonanime.art"})
	stub := newStreamStub(hls)
	saved := newTransport
	newTransport = func() http.RoundTripper { return stub }
	t.Cleanup(func() { newTransport = saved })
	return dataDir, t.TempDir()
}

func pinTitle(t *testing.T, id, studio string, kind provider.Kind) {
	t.Helper()
	ref, err := anitube.RefFromSlug(id[len("anitube:"):])
	if err != nil {
		t.Fatal(err)
	}
	a, err := newAppWith(newTransport(), false)
	if err != nil {
		t.Fatalf("newAppWith: %v", err)
	}
	defer a.Close()
	if err := a.engineWithoutPlayer().PinStudio(ref, studio, kind); err != nil {
		t.Fatalf("PinStudio: %v", err)
	}
}

func downloadedRelease(t *testing.T, args ...string) provider.Release {
	t.Helper()
	code, out, errOut := runCLI(t, args...)
	mustExit(t, 0, code, out, errOut)
	var got downloadResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("розбір %q: %v", out, err)
	}
	return provider.Release{Studio: got.Studio, Kind: got.Kind}
}

// Регресія: без --studio/--kind download обирає рівно те, що обирав до появи
// прапорців (значення зафіксовано на коді до етапу 2). Пін, улюблена студія,
// перевага типу і реліз лише на непідтримуваному хості.
func TestDownloadSelectionWithoutFlagsUnchanged(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		ep     string
		config string
		pin    *provider.Release
		want   provider.Release
	}{
		{"типово", fixtureTitleID, "1", `{}`, nil, provider.Release{Studio: "4UA", Kind: provider.KindVoiceover}},
		{"улюблена студія", fixtureTitleID, "1", `{"favorite_studio":"Glass Moon"}`, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindVoiceover}},
		{"перевага sub", fixtureTitleID, "1", `{"prefer_kind":"sub"}`, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}},
		{"улюблена sub без екстрактора", fixtureTitleID, "1", `{"favorite_studio":"InariDuB","prefer_kind":"sub"}`, nil, provider.Release{Studio: "InariDuB", Kind: provider.KindVoiceover}},
		{"пін студії", fixtureTitleID, "1", `{}`, &provider.Release{Studio: "FANVOXUA"}, provider.Release{Studio: "FANVOXUA", Kind: provider.KindVoiceover}},
		{"пін субтитрів", fixtureTitleID, "1", `{}`, &provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}, provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}},
		{"синтетичний: обидва релізи", syntheticTitleID, "1", `{}`, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindVoiceover}},
		{"синтетичний: озвучка без екстрактора", syntheticTitleID, "2", `{}`, nil, provider.Release{Studio: "K0wbassa", Kind: provider.KindSub}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir, dlDir := anyHostDownloadEnv(t)
			if err := os.WriteFile(filepath.Join(dataDir, "config.json"), []byte(tt.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if tt.pin != nil {
				pinTitle(t, tt.id, tt.pin.Studio, tt.pin.Kind)
			}
			got := downloadedRelease(t, "download", tt.id, tt.ep, "--dir", dlDir, "--json")
			if got != tt.want {
				t.Fatalf("обрано %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Приймання етапу 2: озвучка серії лише на непідтримуваному хості —
// `--kind voiced` завершується host_unsupported, субтитри не підставляються,
// на диску нічого (ані .part, ані теки тайтлу).
func TestDownloadExplicitReleaseUnavailableWritesNothing(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code string
	}{
		{"озвучка лише на непідтримуваному хості", []string{syntheticTitleID, "2", "--kind", "voiced"}, i18n.CodeHostUnsupported},
		{"студія лише на непідтримуваному хості", []string{syntheticTitleID, "2", "--studio", "Glass Moon"}, i18n.CodeHostUnsupported},
		{"такого типу немає", []string{syntheticTitleID, "2", "--kind", "dub"}, i18n.CodeReleaseUnavailable},
		{"такої студії немає", []string{fixtureTitleID, "1", "--studio", "Нема Такої"}, i18n.CodeReleaseUnavailable},
		{"пари немає", []string{fixtureTitleID, "1", "--studio", "4UA", "--kind", "sub"}, i18n.CodeReleaseUnavailable},
		// субтитри студії не підставляються, поки є озвучення, яке можна грати
		{"студія без типу лише в субтитрах", []string{syntheticTitleID, "1", "--studio", "K0wbassa"}, i18n.CodeReleaseUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir, dlDir := anyHostDownloadEnv(t)
			before := dataSnapshot(t, dataDir)
			args := append([]string{"download"}, tt.args...)
			args = append(args, "--dir", dlDir, "--json")
			code, out, errOut := runCLI(t, args...)
			mustExit(t, 1, code, out, errOut)
			if got := jsonErrorOf(t, out, errOut); got.Code != tt.code {
				t.Fatalf("помилка = %+v, want %s", got, tt.code)
			}
			if entries, err := os.ReadDir(dlDir); err != nil || len(entries) != 0 {
				t.Fatalf("папка завантажень не порожня: %v %v", entries, err)
			}
			if after := dataSnapshot(t, dataDir); after != before {
				t.Fatalf("каталог даних змінився:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// dataSnapshot — бібліотека й налаштування як текст: прапорці вибору релізу
// не мають їх змінювати.
func dataSnapshot(t *testing.T, dataDir string) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"library.json", "config.json"} {
		data, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		b.WriteString(name + ":" + string(data) + "\n")
	}
	return b.String()
}

// Явний вибір бере рівно запитане, без зміни піна, бібліотеки й налаштувань.
func TestDownloadExplicitReleaseSelection(t *testing.T) {
	tests := []struct {
		name string
		args []string
		pin  *provider.Release
		want provider.Release
	}{
		// дві озвучки: без прапорців узяли б 4UA (регресія вище)
		{"студія з двох озвучок", []string{fixtureTitleID, "1", "--studio", "Glass Moon"}, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindVoiceover}},
		{"студія без урахування регістру", []string{fixtureTitleID, "1", "--studio", "FanVoxUA"}, nil, provider.Release{Studio: "FANVOXUA", Kind: provider.KindVoiceover}},
		{"студія і тип", []string{fixtureTitleID, "1", "--studio", "Glass Moon", "--kind", "sub"}, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}},
		{"лише субтитри", []string{fixtureTitleID, "1", "--kind", "sub"}, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}},
		// пін на субтитри Glass Moon: voiced бере озвучку тієї ж студії за Pick
		{"voiced при піні субтитрів", []string{fixtureTitleID, "1", "--kind", "voiced"}, &provider.Release{Studio: "Glass Moon", Kind: provider.KindSub}, provider.Release{Studio: "Glass Moon", Kind: provider.KindVoiceover}},
		{"студія поверх піна", []string{fixtureTitleID, "1", "--studio", "Amanogawa"}, &provider.Release{Studio: "FANVOXUA"}, provider.Release{Studio: "Amanogawa", Kind: provider.KindVoiceover}},
		// озвучка недоступна (лише непідтримуваний хост) — субтитри студії дозволені
		{"студія лише в субтитрах, озвучки не зіграти", []string{syntheticTitleID, "2", "--studio", "k0wbassa"}, nil, provider.Release{Studio: "K0wbassa", Kind: provider.KindSub}},
		{"voiced на синтетичному", []string{syntheticTitleID, "1", "--kind", "voiced"}, nil, provider.Release{Studio: "Glass Moon", Kind: provider.KindVoiceover}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir, dlDir := anyHostDownloadEnv(t)
			if tt.pin != nil {
				pinTitle(t, tt.args[0], tt.pin.Studio, tt.pin.Kind)
			}
			before := dataSnapshot(t, dataDir)
			args := append([]string{"download"}, tt.args...)
			got := downloadedRelease(t, append(args, "--dir", dlDir, "--json")...)
			if got != tt.want {
				t.Fatalf("обрано %+v, want %+v", got, tt.want)
			}
			if after := dataSnapshot(t, dataDir); after != before {
				t.Fatalf("каталог даних змінився:\n%s\n---\n%s", before, after)
			}
		})
	}
}

// resolve з тими самими прапорцями фільтрує кандидатів так само.
func TestResolveExplicitRelease(t *testing.T) {
	cliEnv(t)
	code, out, errOut := runCLI(t, "resolve", fixtureTitleID, "1", "--studio", "fanvoxua", "--json")
	mustExit(t, 0, code, out, errOut)
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	if len(cands) == 0 {
		t.Fatal("resolve --studio: порожньо")
	}
	for _, c := range cands {
		if c.Studio != "FANVOXUA" {
			t.Fatalf("чужий кандидат: %+v", c)
		}
	}

	code, out, errOut = runCLI(t, "resolve", fixtureTitleID, "1", "--kind", "voiced", "--json")
	mustExit(t, 0, code, out, errOut)
	cands = nil
	mustJSON(t, out, &cands)
	for _, c := range cands {
		if c.Kind != provider.KindDub && c.Kind != provider.KindVoiceover {
			t.Fatalf("--kind voiced повернув %+v", c)
		}
	}

	for _, tt := range []struct {
		args []string
		code string
	}{
		{[]string{syntheticTitleID, "2", "--kind", "voiced"}, i18n.CodeHostUnsupported},
		{[]string{syntheticTitleID, "2", "--kind", "dub"}, i18n.CodeReleaseUnavailable},
	} {
		code, out, errOut := runCLI(t, append(append([]string{"resolve"}, tt.args...), "--json")...)
		mustExit(t, 1, code, out, errOut)
		if got := jsonErrorOf(t, out, errOut); got.Code != tt.code {
			t.Fatalf("%v: %+v, want %s", tt.args, got, tt.code)
		}
	}
}

// Усі потоки запитаного релізу мертві — release_unavailable, а не інший реліз.
func TestExplicitReleaseDeadStreams(t *testing.T) {
	_, ft, _ := journeyEnv(t)
	ft.failHost("ashdi.vip", faultEmptyBody)
	dlDir := t.TempDir()
	for _, args := range [][]string{
		{"resolve", syntheticTitleID, "1", "--studio", "Glass Moon", "--json"},
		{"download", syntheticTitleID, "1", "--studio", "Glass Moon", "--dir", dlDir, "--json"},
	} {
		code, out, errOut := runCLI(t, args...)
		mustExit(t, 1, code, out, errOut)
		if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeReleaseUnavailable {
			t.Fatalf("%v: %+v", args, got)
		}
	}
	if entries, _ := os.ReadDir(dlDir); len(entries) != 0 {
		t.Fatalf("папка завантажень не порожня: %v", entries)
	}
}

func TestExplicitReleaseFlagsUsage(t *testing.T) {
	cliEnv(t)
	for _, args := range [][]string{
		{"resolve", fixtureTitleID, "1", "--kind", "multi", "--json"},
		{"resolve", fixtureTitleID, "1", "--kind", "--json"},
		{"download", fixtureTitleID, "1", "--studio=", "--json"},
		{"download", fixtureTitleID, "1", "--studio", " ", "--json"},
	} {
		code, out, errOut := runCLI(t, args...)
		mustExit(t, 2, code, out, errOut)
		if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeUsage {
			t.Fatalf("%v: %+v", args, got)
		}
	}
}
