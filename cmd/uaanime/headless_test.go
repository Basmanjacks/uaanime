package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/basmanjacks/uaanime/internal/downloadtest"
	"github.com/basmanjacks/uaanime/internal/i18n"
	"github.com/basmanjacks/uaanime/internal/playback"
	"github.com/basmanjacks/uaanime/internal/provider"
)

// Контракт headless-команд для скриптів: чесний `episodes`, коди помилок під
// --json, версія. Синтетична фікстура anitube (не з сайту): серія 1 — обидва
// релізи на ashdi; серія 2 — озвучка Glass Moon лише на непідтримуваному
// хості, субтитри K0wbassa на ashdi; серія 3 — усе на непідтримуваному хості.
const syntheticTitleID = "anitube:9000001-synthetic-hosts"

// syntheticCachePath — файл кешу серій синтетичного тайтлу.
func syntheticCachePath(dataDir string) string {
	return filepath.Join(dataDir, "cache", "episodes-anitube-9000001-synthetic-hosts.json")
}

func listedRelease(t *testing.T, eps []playback.ListedEpisode, number int, studio string, kind provider.Kind) playback.ListedRelease {
	t.Helper()
	for _, ep := range eps {
		if ep.Number != number {
			continue
		}
		for _, r := range ep.Releases {
			if r.Studio == studio && r.Kind == kind {
				return r
			}
		}
	}
	t.Fatalf("серія %d: немає релізу %s/%s у %+v", number, studio, kind, eps)
	return playback.ListedRelease{}
}

func mustPlayable(t *testing.T, r playback.ListedRelease, want bool) {
	t.Helper()
	if r.Playable == nil || *r.Playable != want {
		t.Fatalf("%s/%s: playable = %v, want %v", r.Studio, r.Kind, r.Playable, want)
	}
}

// Приймання етапу 1: реліз, який лежить лише на непідтримуваному хості,
// `episodes --json` позначає playable:false, а `resolve` його не повертає.
func TestEpisodesMarksReleasesWithoutExtractor(t *testing.T) {
	cliEnv(t)

	code, out, errOut := runCLI(t, "episodes", syntheticTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	var eps []playback.ListedEpisode
	mustJSON(t, out, &eps)
	mustPlayable(t, listedRelease(t, eps, 1, "Glass Moon", provider.KindVoiceover), true)
	mustPlayable(t, listedRelease(t, eps, 1, "K0wbassa", provider.KindSub), true)
	mustPlayable(t, listedRelease(t, eps, 2, "Glass Moon", provider.KindVoiceover), false)
	mustPlayable(t, listedRelease(t, eps, 2, "K0wbassa", provider.KindSub), true)
	mustPlayable(t, listedRelease(t, eps, 3, "Glass Moon", provider.KindVoiceover), false)

	// Наявні поля не змінились: кожен реліз — рівно studio, kind і нове playable.
	var raw []map[string]json.RawMessage
	mustJSON(t, out, &raw)
	for _, ep := range raw {
		var releases []map[string]json.RawMessage
		if err := json.Unmarshal(ep["releases"], &releases); err != nil {
			t.Fatal(err)
		}
		for _, r := range releases {
			keys := make([]string, 0, len(r))
			for k := range r {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if strings.Join(keys, ",") != "kind,playable,studio" {
				t.Fatalf("поля релізу = %v", keys)
			}
		}
	}

	code, out, errOut = runCLI(t, "resolve", syntheticTitleID, "2", "--json")
	mustExit(t, 0, code, out, errOut)
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	if len(cands) == 0 {
		t.Fatal("resolve 2: порожньо")
	}
	for _, c := range cands {
		if c.Studio != "K0wbassa" {
			t.Fatalf("resolve повернув реліз без екстрактора: %+v", c)
		}
	}

	// Текстовий вивід позначає непідтримуваний реліз.
	code, out, errOut = runCLI(t, "episodes", syntheticTitleID)
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, "2\t"+fmt.Sprintf(i18n.MsgEpisodeReleaseUnplayable, "Glass Moon", provider.KindVoiceover)+", K0wbassa (sub)")
}

// Справжня фікстура сайту: у Фрірен субтитри InariDuB лежать лише на
// непідтримуваному хості.
func TestEpisodesPlayableOnRecordedFixture(t *testing.T) {
	cliEnv(t)
	code, out, errOut := runCLI(t, "episodes", fixtureTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	var eps []playback.ListedEpisode
	mustJSON(t, out, &eps)
	mustPlayable(t, listedRelease(t, eps, 1, "InariDuB", provider.KindSub), false)
	mustPlayable(t, listedRelease(t, eps, 1, "InariDuB", provider.KindVoiceover), true)

	code, out, errOut = runCLI(t, "resolve", fixtureTitleID, "1", "--json")
	mustExit(t, 0, code, out, errOut)
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	for _, c := range cands {
		if c.Studio == "InariDuB" && c.Kind == provider.KindSub {
			t.Fatalf("resolve повернув реліз без екстрактора: %+v", c)
		}
	}
}

func writeEpisodesCache(t *testing.T, path string, at time.Time, body map[string]any) {
	t.Helper()
	body["fetched_at"] = at
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func episodeNumbers(t *testing.T, out string) []int {
	t.Helper()
	var eps []provider.Episode
	mustJSON(t, out, &eps)
	nums := make([]int, 0, len(eps))
	for _, ep := range eps {
		nums = append(nums, ep.Number)
	}
	return nums
}

// Кеш: свіжий запис із джерелами віддається без мережі; --fresh його
// обходить; старий формат без джерел перепитується, а не вгадується.
func TestEpisodesCacheAndFresh(t *testing.T) {
	dir := cliEnv(t)
	path := syntheticCachePath(dir)
	sources := []provider.Source{{Studio: "X", Kind: provider.KindDub, Embed: "https://ashdi.vip/vod/42", Episode: 42}}
	writeEpisodesCache(t, path, time.Now(), map[string]any{
		"episodes": []provider.Episode{{Number: 42, Releases: []provider.Release{{Studio: "X", Kind: provider.KindDub}}}},
		"sources":  sources,
	})

	code, out, errOut := runCLI(t, "episodes", syntheticTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	if got := episodeNumbers(t, out); !slices.Equal(got, []int{42}) {
		t.Fatalf("свіжий кеш з джерелами: серії %v, want [42]", got)
	}
	var listed []playback.ListedEpisode
	mustJSON(t, out, &listed)
	mustPlayable(t, listed[0].Releases[0], true)

	code, out, errOut = runCLI(t, "episodes", syntheticTitleID, "--json", "--fresh")
	mustExit(t, 0, code, out, errOut)
	if got := episodeNumbers(t, out); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("--fresh: серії %v, want [1 2 3]", got)
	}

	// Старий формат: свіжий, але без джерел — перепитати провайдера.
	writeEpisodesCache(t, path, time.Now(), map[string]any{
		"episodes": []provider.Episode{{Number: 42, Releases: []provider.Release{{Studio: "X", Kind: provider.KindDub}}}},
	})
	code, out, errOut = runCLI(t, "episodes", syntheticTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	if got := episodeNumbers(t, out); !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("кеш без джерел: серії %v, want [1 2 3] з провайдера", got)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"sources"`) {
		t.Fatalf("кеш після episodes без джерел: %s", b)
	}
}

// Офлайн зі старим кешем без джерел: серії з кешу, playable невідомий —
// поля немає, а не вигадане значення.
func TestEpisodesOfflineOldCacheOmitsPlayable(t *testing.T) {
	dir, ft, _ := journeyEnv(t)
	writeEpisodesCache(t, syntheticCachePath(dir), time.Now().Add(-7*time.Hour), map[string]any{
		"episodes": []provider.Episode{{Number: 42, Releases: []provider.Release{{Studio: "X", Kind: provider.KindDub}}}},
	})
	ft.failHost("anitube.in.ua", faultOffline)

	code, out, errOut := runCLI(t, "episodes", syntheticTitleID, "--json")
	mustExit(t, 0, code, out, errOut)
	if got := episodeNumbers(t, out); !slices.Equal(got, []int{42}) {
		t.Fatalf("офлайн: серії %v, want [42] з кешу", got)
	}
	if strings.Contains(out, "playable") {
		t.Fatalf("playable без джерел: %s", out)
	}
	// --fresh не має офлайн-fallback: обхід кешу означає «лише свіжі дані».
	code, out, errOut = runCLI(t, "episodes", syntheticTitleID, "--json", "--fresh")
	mustExit(t, 1, code, out, errOut)
	if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeOffline {
		t.Fatalf("--fresh офлайн: %+v", got)
	}
}

// jsonErrorOf перевіряє форму помилки під --json: stdout порожній, на stderr
// рівно один рядок-об'єкт {"error":{...}}.
func jsonErrorOf(t *testing.T, out, errOut string) jsonErrorBody {
	t.Helper()
	if out != "" {
		t.Fatalf("stdout під час помилки не порожній: %q", out)
	}
	if strings.Count(errOut, "\n") != 1 || !strings.HasSuffix(errOut, "\n") {
		t.Fatalf("stderr має бути одним рядком: %q", errOut)
	}
	var e jsonError
	dec := json.NewDecoder(strings.NewReader(errOut))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("stderr не JSON-помилка: %v\n%s", err, errOut)
	}
	if e.Error.Code == "" || e.Error.Message == "" {
		t.Fatalf("порожні поля: %+v", e)
	}
	return e.Error
}

// Кожен код помилки під --json: той самий код виходу і той самий текст, що
// без --json.
func TestJSONErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		args []string
		exit int
		code string
	}{
		{"usage: невідома команда", []string{"frobnicate"}, 2, i18n.CodeUsage},
		{"usage: арність", []string{"episodes", "a", "b"}, 2, i18n.CodeUsage},
		{"usage: title-id", []string{"resolve", "bad/../slug", "1"}, 2, i18n.CodeUsage},
		{"usage: серія", []string{"resolve", fixtureTitleID, "0"}, 2, i18n.CodeUsage},
		{"usage: прапорець перед --json", []string{"download", fixtureTitleID, "1", "--quality", "abc"}, 2, i18n.CodeUsage},
		{"provider", []string{"episodes", "999999-nemaye-takogo-tajtlu"}, 1, i18n.CodeProvider},
		{"no_stream", []string{"resolve", syntheticTitleID, "9"}, 1, i18n.CodeNoStream},
		{"host_unsupported", []string{"resolve", syntheticTitleID, "3"}, 1, i18n.CodeHostUnsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliEnv(t)
			code, out, humanErr := runCLI(t, tt.args...)
			mustExit(t, tt.exit, code, out, humanErr)
			code, out, errOut := runCLI(t, append(slices.Clone(tt.args), "--json")...)
			mustExit(t, tt.exit, code, out, errOut)
			got := jsonErrorOf(t, out, errOut)
			if got.Code != tt.code {
				t.Fatalf("code = %q, want %q (%s)", got.Code, tt.code, errOut)
			}
			if got.Message != strings.TrimSuffix(humanErr, "\n") {
				t.Fatalf("message = %q, а без --json: %q", got.Message, humanErr)
			}
		})
	}
}

func TestJSONErrorOffline(t *testing.T) {
	_, ft, _ := journeyEnv(t)
	for _, h := range []string{"anitube.in.ua", "ashdi.vip", "tortuga.tw", "moonanime.art"} {
		ft.failHost(h, faultOffline)
	}
	for _, args := range [][]string{
		{"episodes", fixtureTitleID, "--json"},
		{"resolve", fixtureTitleID, "1", "--json"},
	} {
		code, out, errOut := runCLI(t, args...)
		mustExit(t, 1, code, out, errOut)
		got := jsonErrorOf(t, out, errOut)
		if got.Code != i18n.CodeOffline || got.Message != i18n.MsgOffline {
			t.Fatalf("%v: %+v", args, got)
		}
	}
}

func TestJSONErrorQualityMissing(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "4320", "--dir", dlDir, "--json")
	mustExit(t, 1, code, out, errOut)
	got := jsonErrorOf(t, out, errOut)
	if got.Code != i18n.CodeQualityMissing || !strings.Contains(got.Message, "1080p") {
		t.Fatalf("помилка = %+v", got)
	}
	if files := walkDir(t, dlDir); len(files) != 0 {
		t.Fatalf("папка завантажень не порожня: %v", files)
	}
}

func TestJSONErrorInternal(t *testing.T) {
	cliEnv(t)
	commands["boom"] = command{minArgs: 1, maxArgs: 1, run: func(*app, context.Context, []string, options) int {
		panic("тестовий вибух")
	}}
	t.Cleanup(func() { delete(commands, "boom") })

	code, out, errOut := runCLI(t, "boom", "--json")
	mustExit(t, 1, code, out, errOut)
	got := jsonErrorOf(t, out, errOut)
	if got.Code != i18n.CodeInternal || got.Message != i18n.MsgInternalFailure {
		t.Fatalf("помилка = %+v", got)
	}
}

func TestJSONErrorCancelled(t *testing.T) {
	_, dlDir, hls := downloadEnv(t, downloadtest.HLSOptions{Segments: 8})
	release := make(chan struct{})
	defer close(release)
	hls.BlockSegment(6, release)

	type result struct {
		code        int
		out, errOut string
	}
	done := make(chan result, 1)
	go func() {
		code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir, "--json")
		done <- result{code, out, errOut}
	}()
	waitFor(t, 10*time.Second, func() bool {
		return strings.Contains(strings.Join(hls.Paths(), " "), "segment6.ts")
	})
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
	select {
	case r := <-done:
		mustExit(t, 1, r.code, r.out, r.errOut)
		if got := jsonErrorOf(t, r.out, r.errOut); got.Code != i18n.CodeCancelled {
			t.Fatalf("помилка = %+v", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("download не завершився після SIGINT")
	}
}

// Без --json помилка лишається людським текстом, а не JSON.
func TestErrorsWithoutJSONStayText(t *testing.T) {
	cliEnv(t)
	code, _, errOut := runCLI(t, "resolve", syntheticTitleID, "3")
	if code != 1 || strings.TrimSpace(errOut) != i18n.MsgNoPlayableHost {
		t.Fatalf("код %d, stderr %q", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	cliEnv(t)
	for _, args := range [][]string{{"version"}, {"--version"}} {
		code, out, errOut := runCLI(t, args...)
		mustExit(t, 0, code, out, errOut)
		if want := fmt.Sprintf(i18n.MsgVersion, buildVersion()) + "\n"; out != want {
			t.Fatalf("%v: stdout = %q, want %q", args, out, want)
		}
	}
	for _, args := range [][]string{{"version", "--json"}, {"--version", "--json"}} {
		code, out, errOut := runCLI(t, args...)
		mustExit(t, 0, code, out, errOut)
		var v struct {
			Version string `json:"version"`
		}
		mustJSON(t, out, &v)
		if v.Version == "" || v.Version != buildVersion() {
			t.Fatalf("%v: version = %q", args, v.Version)
		}
	}
	code, out, errOut := runCLI(t, "version", "extra", "--json")
	mustExit(t, 2, code, out, errOut)
	if got := jsonErrorOf(t, out, errOut); got.Code != i18n.CodeUsage {
		t.Fatalf("version extra: %+v", got)
	}
}

// Версію, підставлену через -ldflags, видно як є.
func TestBuildVersionPrefersInjected(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v9.9.9"
	if got := buildVersion(); got != "v9.9.9" {
		t.Fatalf("buildVersion = %q", got)
	}
}
