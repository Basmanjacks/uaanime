package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Тести зібраного бінарника: справжні коди виходу, розділення stdout/stderr,
// поведінка без TTY і холодний старт — усе, чого in-process run() не покаже.

var (
	binPath  string // порожній лише до TestMain
	repoRoot string
)

func TestMain(m *testing.M) {
	root, err := filepath.Abs("../..")
	if err != nil {
		log.Fatalf("корінь репозиторію: %v", err)
	}
	repoRoot = root
	dir, err := os.MkdirTemp("", "uaanime-bin-")
	if err != nil {
		log.Fatalf("MkdirTemp: %v", err)
	}
	binPath = filepath.Join(dir, "uaanime")
	// Без -race: вимірюємо той бінарник, який отримує користувач.
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		log.Fatalf("go build: %v\n%s", err, out)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// binEnv — ізольоване середовище підпроцесу: фікстури, свій каталог даних,
// без плеєрів у PATH (щоб doctor і play не залежали від машини).
func binEnv(t *testing.T) (dataDir string, env []string) {
	t.Helper()
	dataDir = t.TempDir()
	env = []string{
		"UAANIME_FIXTURES=1",
		"UAANIME_DATA_DIR=" + dataDir,
		"HOME=" + t.TempDir(),
		"PATH=" + t.TempDir(),
	}
	return dataDir, env
}

func runBin(t *testing.T, env []string, args ...string) (code int, out, errOut string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = repoRoot
	cmd.Env = env
	var ob, eb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &ob, &eb
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		code = exitErr.ExitCode()
	default:
		t.Fatalf("%v: %v", args, err)
	}
	return code, ob.String(), eb.String()
}

// Пайплайн через stdout: кожна команда споживає те, що надрукувала попередня.
func TestBinaryPipeline(t *testing.T) {
	t.Parallel()
	_, env := binEnv(t)

	code, out, errOut := runBin(t, env, "search", "фрірен")
	if code != 0 || errOut != "" {
		t.Fatalf("search: код %d, stderr %q", code, errOut)
	}
	id := strings.Split(strings.SplitN(out, "\n", 2)[0], "\t")[0]
	if !strings.HasPrefix(id, "anitube:") {
		t.Fatalf("перша колонка = %q, want title-id", id)
	}

	code, out, errOut = runBin(t, env, "episodes", id, "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("episodes: код %d, stderr %q", code, errOut)
	}
	var eps []provider.Episode
	mustJSON(t, out, &eps)
	if len(eps) == 0 {
		t.Fatal("episodes: порожньо")
	}
	ep := strconv.Itoa(eps[0].Number)

	code, out, errOut = runBin(t, env, "resolve", id, ep, "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("resolve: код %d, stderr %q", code, errOut)
	}
	var cands []playback.Candidate
	mustJSON(t, out, &cands)
	if len(cands) == 0 {
		t.Fatal("resolve: порожньо")
	}
	urls := map[string]bool{}
	for _, c := range cands {
		urls[c.Stream.URL] = true
	}

	code, out, errOut = runBin(t, env, "play", id, ep, "--dry-run")
	if code != 0 {
		t.Fatalf("play --dry-run: код %d, stderr %q", code, errOut)
	}
	argv := lastLine(out)
	found := false
	for u := range urls {
		if strings.Contains(argv, u) {
			found = true
		}
	}
	if !found {
		t.Errorf("argv %q не містить жодного потоку з resolve", argv)
	}
}

// Без аргументів і без термінала бінарник має відмовити, а не зависнути.
func TestBinaryWithoutTTY(t *testing.T) {
	t.Parallel()
	_, env := binEnv(t)
	code, out, errOut := runBin(t, env)
	if code != 2 {
		t.Fatalf("код = %d, want 2\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	mustContain(t, "stderr", errOut, i18n.MsgNeedTTY)
	if out != "" {
		t.Errorf("stdout має бути порожнім: %q", out)
	}
}

func TestBinaryExitCodes(t *testing.T) {
	t.Parallel()
	_, env := binEnv(t)
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"невідома команда", []string{"frobnicate"}, 2},
		{"невалідна серія", []string{"resolve", fixtureTitleID, "нуль"}, 2},
		{"невідомий тайтл", []string{"episodes", "999999-nemaye-takogo-tajtlu"}, 1},
		{"doctor без плеєрів", []string{"doctor", "--json"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runBin(t, env, tt.args...)
			if code != tt.want {
				t.Fatalf("код = %d, want %d\nstdout: %s\nstderr: %s", code, tt.want, out, errOut)
			}
			if tt.want != 0 {
				if strings.TrimSpace(errOut) == "" {
					t.Error("помилка без пояснення на stderr")
				}
				if out != "" {
					t.Errorf("помилка не має писати у stdout: %q", out)
				}
			} else if !json.Valid([]byte(out)) {
				t.Errorf("--json дав невалідний JSON: %s", out)
			}
		})
	}
}

// doctor: без жодного плеєра — точна команда встановлення; з плеєром —
// жодних підказок. PATH порожній, але VLC.app на macOS шукається за
// абсолютним шляхом, тому очікування виводиться з --json того ж бінарника.
func TestBinaryDoctorInstallHint(t *testing.T) {
	t.Parallel()
	_, env := binEnv(t)
	code, out, _ := runBin(t, env, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json: код %d", code)
	}
	var rep doctorReport
	mustJSON(t, out, &rep)
	anyFound := false
	for _, p := range rep.Players {
		anyFound = anyFound || p.Found
	}

	code, out, _ = runBin(t, env, "doctor")
	if code != 0 {
		t.Fatalf("doctor: код %d", code)
	}
	hint := strings.Contains(out, i18n.MsgInstallHintMac) || strings.Contains(out, i18n.MsgInstallHintLinux)
	if hint == anyFound {
		t.Errorf("плеєр знайдено=%v, підказка встановлення=%v:\n%s", anyFound, hint, out)
	}
}

// Зіпсований бекап не має чіпати наявну бібліотеку — ні байта.
func TestBinaryCorruptImportKeepsLibrary(t *testing.T) {
	t.Parallel()
	dataDir, env := binEnv(t)
	libPath := filepath.Join(dataDir, "library.json")
	before := []byte(`{"titles":[{"id":"t1","name":"Тест","sources":[{"provider":"anitube","slug":"1-test","name":"Тест","url":"https://anitube.in.ua/1-test.html"}]}],"entries":[{"title_id":"t1","state":"watching","studio_pin":"FANVOXUA"}],"progress":[]}`)
	if err := os.WriteFile(libPath, before, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	for name, content := range map[string]string{
		"сміття":      "не json",
		"без library": `{"config":{}}`,
		"ESC у назві": "{\"library\":{\"titles\":[{\"id\":\"x\",\"name\":\"\x1b[31mesc\",\"sources\":[]}]}}",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(bad, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			code, out, errOut := runBin(t, env, "import", bad)
			if code != 1 || strings.TrimSpace(errOut) == "" {
				t.Fatalf("import: код %d, stderr %q, stdout %q", code, errOut, out)
			}
			after, err := os.ReadFile(libPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Errorf("library.json змінився після невдалого імпорту: %s", after)
			}
			if _, err := os.Stat(libPath + ".bak"); err == nil {
				t.Error("невдалий імпорт не має лишати .bak")
			}
		})
	}
}

// Холодний старт: бриф вимагає < 100 мс; тут — грубий регрес (CI шумний),
// точне значення лягає у -v для ручної перевірки.
func TestBinaryColdStart(t *testing.T) {
	_, env := binEnv(t)
	var samples []time.Duration
	for range 5 {
		started := time.Now()
		if code, _, errOut := runBin(t, env, "export"); code != 0 {
			t.Fatalf("export: код %d, %s", code, errOut)
		}
		samples = append(samples, time.Since(started))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	median := samples[len(samples)/2]
	t.Logf("холодний старт (export), медіана з %d: %v (min %v, max %v)", len(samples), median, samples[0], samples[len(samples)-1])
	if median > 500*time.Millisecond {
		t.Errorf("холодний старт %v — грубий регрес (бриф: < 100 мс)", median)
	}
}
