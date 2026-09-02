package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// fixtureTitleID — канонічний тайтл фікстур (Фрірен, 7 студій).
const fixtureTitleID = "anitube:4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon"

func TestShellQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "плоскі аргументи не змінюються",
			args: []string{"vlc", "--play-and-exit", "https://cdn.invalid/a/b.m3u8"},
			want: "vlc --play-and-exit https://cdn.invalid/a/b.m3u8",
		},
		{
			name: "пробіл і лапка",
			args: []string{"--meta-title=Тайтл 'Один' · 1"},
			want: `'--meta-title=Тайтл '\''Один'\'' · 1'`,
		},
		{
			// `?` і `&` в оболонці значущі, тому URL із запитом береться в лапки
			name: "URL із запитом",
			args: []string{"https://cdn.invalid/a.m3u8?expires=1&sig=ab"},
			want: "'https://cdn.invalid/a.m3u8?expires=1&sig=ab'",
		},
		{
			name: "порожній аргумент",
			args: []string{"mpv", ""},
			want: "mpv ''",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shellQuote(tt.args); got != tt.want {
				t.Fatalf("shellQuote(%q) = %s, want %s", tt.args, got, tt.want)
			}
		})
	}
}

// cliEnv ізолює один запуск run(): фікстури замість мережі, порожній каталог
// даних і корінь репозиторію як cwd — шляхи фікстур у newApp відносні.
func cliEnv(t *testing.T) string {
	t.Helper()
	t.Chdir("../..")
	t.Setenv("UAANIME_FIXTURES", "1")
	dir := t.TempDir()
	t.Setenv("UAANIME_DATA_DIR", dir)
	return dir
}

// runCLI ганяє справжній run() і збирає його вивід. Тести не можуть іти
// паралельно: stdout/stderr — пакетні змінні.
func runCLI(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var ob, eb bytes.Buffer
	savedOut, savedErr := stdout, stderr
	stdout, stderr = &ob, &eb
	defer func() { stdout, stderr = savedOut, savedErr }()
	code = run(args)
	return code, ob.String(), eb.String()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

func mustContain(t *testing.T, where, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Fatalf("%s = %q, очікував підрядок %q", where, s, want)
	}
}

func TestRunCommands(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		want  int
		check func(t *testing.T, out, errOut string)
	}{
		{
			name: "невідома команда",
			args: []string{"frobnicate"},
			want: 2,
			check: func(t *testing.T, _, errOut string) {
				mustContain(t, "stderr", errOut, i18n.MsgUsage)
			},
		},
		{
			// нижня межа арності: resolve потребує тайтл І серію
			name: "замало аргументів",
			args: []string{"resolve", "slug"},
			want: 2,
			check: func(t *testing.T, _, errOut string) {
				mustContain(t, "stderr", errOut, i18n.MsgUsage)
			},
		},
		{
			// верхня межа арності: episodes бере рівно один тайтл
			name: "забагато аргументів",
			args: []string{"episodes", "a", "b"},
			want: 2,
			check: func(t *testing.T, _, errOut string) {
				mustContain(t, "stderr", errOut, i18n.MsgUsage)
			},
		},
		{
			name: "невалідний title-id",
			args: []string{"resolve", "bad/../slug", "1"},
			want: 2,
			check: func(t *testing.T, _, errOut string) {
				mustContain(t, "stderr", errOut, fmt.Sprintf(i18n.MsgBadTitleID, "bad/../slug"))
			},
		},
		{
			name: "search --json",
			args: []string{"search", "фрірен", "--json"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				var cards []provider.TitleCard
				mustJSON(t, out, &cards)
				if len(cards) == 0 {
					t.Fatal("search --json: порожній масив")
				}
				for _, c := range cards {
					if !provider.ValidSlug(c.Slug) {
						t.Errorf("невалідний слаг %q", c.Slug)
					}
				}
			},
		},
		{
			name: "episodes --json",
			args: []string{"episodes", fixtureTitleID, "--json"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				var eps []provider.Episode
				mustJSON(t, out, &eps)
				if len(eps) == 0 {
					t.Fatal("episodes --json: порожній масив")
				}
				if eps[0].Number != 1 {
					t.Errorf("перша серія = %d, want 1", eps[0].Number)
				}
			},
		},
		{
			name: "resolve --json",
			args: []string{"resolve", fixtureTitleID, "1", "--json"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				var cands []playback.Candidate
				mustJSON(t, out, &cands)
				if len(cands) == 0 {
					t.Fatal("resolve --json: порожній масив")
				}
				for _, c := range cands {
					if !strings.HasPrefix(c.Stream.URL, "https://") {
						t.Errorf("потік %q не https", c.Stream.URL)
					}
				}
			},
		},
		{
			name: "play --dry-run",
			args: []string{"play", fixtureTitleID, "1", "--dry-run"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				argv := lastLine(out)
				if !strings.HasPrefix(argv, "vlc ") {
					t.Fatalf("argv = %q, очікував початок %q", argv, "vlc ")
				}
				mustContain(t, "argv", argv, "--play-and-exit")
			},
		},
		{
			name: "doctor --json",
			args: []string{"doctor", "--json"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				var rep doctorReport
				mustJSON(t, out, &rep)
				if len(rep.Players) == 0 {
					t.Error("doctor --json: немає players")
				}
				if len(rep.Providers) == 0 {
					t.Fatal("doctor --json: немає providers")
				}
				// фікстури відповідають на канонічний запит, отже джерело живе
				if !rep.Providers[0].Alive {
					t.Errorf("провайдер не живий: %+v", rep.Providers[0])
				}
			},
		},
		{
			// Табличний вивід — теж контракт: колонки розділені табуляціями,
			// перша з них — готовий title-id для наступної команди.
			name: "search без --json",
			args: []string{"search", "фрірен"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				cols := strings.Split(lastLine(out), "\t")
				if len(cols) != 4 {
					t.Fatalf("рядок %q: %d колонок, want 4", lastLine(out), len(cols))
				}
				if !strings.HasPrefix(cols[0], "anitube:") {
					t.Errorf("title-id = %q", cols[0])
				}
			},
		},
		{
			name: "episodes без --json",
			args: []string{"episodes", fixtureTitleID},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				if first := strings.SplitN(out, "\n", 2)[0]; !strings.HasPrefix(first, "1\t") {
					t.Errorf("перший рядок = %q, очікував початок %q", first, "1\t")
				}
			},
		},
		{
			name: "resolve без --json",
			args: []string{"resolve", fixtureTitleID, "1"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				mustContain(t, "stdout", lastLine(out), "\thttps://")
			},
		},
		{
			name: "doctor без --json",
			args: []string{"doctor"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				mustContain(t, "stdout", out, strings.SplitN(i18n.MsgDoctorDataDir, "%", 2)[0])
			},
		},
		{
			// слаг валідний, але такого тайтлу немає — це збій джерела (код 1),
			// а не помилка вжитку (код 2)
			name: "невідомий тайтл",
			args: []string{"episodes", "999999-nemaye-takogo-tajtlu"},
			want: 1,
			check: func(t *testing.T, _, errOut string) {
				if strings.TrimSpace(errOut) == "" {
					t.Error("порожній stderr на збій джерела")
				}
			},
		},
		{
			name: "export",
			args: []string{"export"},
			want: 0,
			check: func(t *testing.T, out, _ string) {
				var backup map[string]json.RawMessage
				mustJSON(t, out, &backup)
				if _, ok := backup["library"]; !ok {
					t.Errorf("бекап без ключа library: %v", keysOf(backup))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cliEnv(t)
			code, out, errOut := runCLI(t, tt.args...)
			if code != tt.want {
				t.Fatalf("run(%q) = %d, want %d\nstdout: %s\nstderr: %s", tt.args, code, tt.want, out, errOut)
			}
			if tt.check != nil {
				tt.check(t, out, errOut)
			}
		})
	}
}

// Без аргументів запускається TUI, а він потребує термінала.
func TestRunWithoutCommandNeedsTTY(t *testing.T) {
	if fi, _ := os.Stdout.Stat(); fi != nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdout тестового раннера — термінал, TUI справді запуститься")
	}
	cliEnv(t)
	code, _, errOut := runCLI(t)
	if code != 2 {
		t.Fatalf("run() = %d, want 2", code)
	}
	mustContain(t, "stderr", errOut, i18n.MsgNeedTTY)
}

// config.json обирає плеєр і для --dry-run: команда будується без пошуку
// встановлених програм, тож тест не залежить від наявності mpv у системі.
func TestRunPlayDryRunHonoursConfiguredPlayer(t *testing.T) {
	dir := cliEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"player":"mpv"}`), 0o600); err != nil {
		t.Fatalf("config.json: %v", err)
	}
	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	if code != 0 {
		t.Fatalf("run(play --dry-run) = %d, want 0\nstderr: %s", code, errOut)
	}
	if argv := lastLine(out); !strings.HasPrefix(argv, "mpv ") {
		t.Fatalf("argv = %q, очікував початок %q", argv, "mpv ")
	}
}

// export → import того самого файлу: бекап, який застосунок щойно написав,
// мусить прийматися ним же.
func TestRunExportImportRoundTrip(t *testing.T) {
	cliEnv(t)
	code, out, errOut := runCLI(t, "export")
	if code != 0 {
		t.Fatalf("run(export) = %d, want 0\nstderr: %s", code, errOut)
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	code, out, errOut = runCLI(t, "import", path)
	if code != 0 {
		t.Fatalf("run(import) = %d, want 0\nstderr: %s", code, errOut)
	}
	mustContain(t, "stdout", out, i18n.MsgImported)
}

// Жорстке правило 3: жоден panic не долітає до користувача. Команда-бомба
// живе лише в цьому тесті — у таблиці її немає.
func TestRunRecoversPanic(t *testing.T) {
	cliEnv(t)
	commands["boom"] = command{minArgs: 1, maxArgs: 1, run: func(*app, context.Context, []string, options) int {
		panic("тестовий вибух")
	}}
	t.Cleanup(func() { delete(commands, "boom") })

	code, _, errOut := runCLI(t, "boom")
	if code != 1 {
		t.Fatalf("run(boom) = %d, want 1", code)
	}
	mustContain(t, "stderr", errOut, fmt.Sprintf(i18n.MsgInternalError, "тестовий вибух"))
}

func mustJSON(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("невалідний JSON: %v\n%s", err, out)
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
