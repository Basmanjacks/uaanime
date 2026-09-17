package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestDefaultDownloadDirPerOS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Run("system video folder", func(t *testing.T) {
		t.Setenv("XDG_VIDEOS_DIR", "")
		want := filepath.Join(home, "Videos", "uaanime")
		if runtime.GOOS == "darwin" {
			want = filepath.Join(home, "Movies", "uaanime")
		}
		if got := DefaultDownloadDir(); got != want {
			t.Fatalf("DefaultDownloadDir() = %q, очікував %q", got, want)
		}
	})

	t.Run("absolute XDG_VIDEOS_DIR", func(t *testing.T) {
		xdg := t.TempDir()
		t.Setenv("XDG_VIDEOS_DIR", xdg)
		want := filepath.Join(xdg, "uaanime")
		if runtime.GOOS == "darwin" {
			// macOS має канонічну ~/Movies; XDG там не діє.
			want = filepath.Join(home, "Movies", "uaanime")
		}
		if got := DefaultDownloadDir(); got != want {
			t.Fatalf("DefaultDownloadDir() = %q, очікував %q", got, want)
		}
	})

	t.Run("relative XDG_VIDEOS_DIR ignored", func(t *testing.T) {
		t.Setenv("XDG_VIDEOS_DIR", "Відео")
		want := filepath.Join(home, "Videos", "uaanime")
		if runtime.GOOS == "darwin" {
			want = filepath.Join(home, "Movies", "uaanime")
		}
		if got := DefaultDownloadDir(); got != want {
			t.Fatalf("DefaultDownloadDir() = %q, очікував %q", got, want)
		}
	})
}

// Без домашнього каталогу дефолт усе одно мусить бути абсолютним: відносний
// шлях означав би завантаження у випадковий CWD.
func TestDefaultDownloadDirWithoutHomeIsAbsolute(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_VIDEOS_DIR", "")
	got := DefaultDownloadDir()
	if !filepath.IsAbs(got) {
		t.Fatalf("DefaultDownloadDir() = %q, очікував абсолютний шлях", got)
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "порожній", in: "", want: ""},
		{name: "сама тильда", in: "~", want: home},
		{name: "тильда зі слешем", in: "~/Movies/uaanime", want: filepath.Join(home, "Movies", "uaanime")},
		{name: "абсолютний без змін", in: "/tmp/a", want: "/tmp/a"},
		{name: "тильда всередині", in: "/tmp/~/a", want: "/tmp/~/a"},
		{name: "тильда в імені файла", in: "/tmp/a~b", want: "/tmp/a~b"},
		{name: "~user не розкривається", in: "~root/Movies", want: "~root/Movies"},
		{name: "~~ не розкривається", in: "~~/a", want: "~~/a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandHome(tt.in); got != tt.want {
				t.Fatalf("ExpandHome(%q) = %q, очікував %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeConfigDownloadDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_VIDEOS_DIR", "")
	def := DefaultDownloadDir()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "порожній — дефолт", in: "", want: def},
		{name: "відносний — дефолт", in: "x/y", want: def},
		{name: "вихід угору — дефолт", in: "../../etc", want: def},
		{name: "крапка — дефолт", in: ".", want: def},
		{name: "керуючі послідовності зчищено", in: "\x1b[31m/tmp/a", want: "/tmp/a"},
		{name: "тильда розкрита", in: "~/x", want: filepath.Join(home, "x")},
		{name: "пробіли обрізано", in: "  /tmp/a  ", want: "/tmp/a"},
		{name: "хвостовий слеш прибрано", in: "/tmp/a/", want: "/tmp/a"},
		{name: "подвійні слеші стиснуто", in: "/tmp//a/./b", want: "/tmp/a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{DownloadDir: tt.in}
			normalizeConfig(cfg)
			if cfg.DownloadDir != tt.want {
				t.Fatalf("DownloadDir = %q, очікував %q", cfg.DownloadDir, tt.want)
			}
		})
	}

	// NUL у шляху вбив би будь-який syscall; після нормалізації його немає,
	// а шлях лишається абсолютним.
	cfg := &Config{DownloadDir: "/tmp/a\x00b"}
	normalizeConfig(cfg)
	if strings.ContainsRune(cfg.DownloadDir, 0) || !filepath.IsAbs(cfg.DownloadDir) {
		t.Fatalf("DownloadDir = %q", cfg.DownloadDir)
	}

	// normalizeConfig не має права торкатися ФС: холодний старт.
	if _, err := os.Stat(def); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("normalizeConfig створив %s: %v", def, err)
	}
}

func TestDefaultConfigHasAbsoluteDownloadDir(t *testing.T) {
	if got := DefaultConfig().DownloadDir; !filepath.IsAbs(got) {
		t.Fatalf("DefaultConfig().DownloadDir = %q, очікував абсолютний шлях", got)
	}
}

func TestSaveConfigKeepsDownloadDir(t *testing.T) {
	s := openTemp(t)
	dir := filepath.Join(t.TempDir(), "Кіно")
	if err := s.SaveConfig(&Config{DownloadDir: dir + "/"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := s.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadDir != dir {
		t.Fatalf("DownloadDir = %q, очікував %q", cfg.DownloadDir, dir)
	}
	// SaveConfig не створює папку — це робота EnsureDownloadDir.
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SaveConfig створив папку: %v", err)
	}
}

func TestBackupCarriesDownloadDir(t *testing.T) {
	src := openTemp(t)
	dir := filepath.Join(t.TempDir(), "Downloads")
	if err := src.SaveConfig(&Config{DownloadDir: dir}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := src.Export(&b); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"download_dir"`) {
		t.Fatalf("бекап без download_dir: %s", b.String())
	}
	dst := openTemp(t)
	if err := dst.Import(bytes.NewReader(b.Bytes())); err != nil {
		t.Fatal(err)
	}
	cfg, err := dst.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DownloadDir != dir {
		t.Fatalf("після імпорту DownloadDir = %q, очікував %q", cfg.DownloadDir, dir)
	}
}

func TestEnsureDownloadDirCreatesAndProbes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "uaanime", "nested")
	if err := EnsureDownloadDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("папку не створено: %v", err)
	}
	// Проба запису не лишає сміття в медіатеці.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("після проби лишилися файли: %v", entries)
	}
	if err := EnsureDownloadDir(dir); err != nil {
		t.Fatalf("повторний виклик: %v", err)
	}
}

func TestEnsureDownloadDirReadOnlyParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ігнорує права доступу")
	}
	parent := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	if err := EnsureDownloadDir(filepath.Join(parent, "uaanime")); !errors.Is(err, errs.ErrNoWriteAccess) {
		t.Fatalf("EnsureDownloadDir = %v, очікував ErrNoWriteAccess", err)
	}
	// Наявна, але недоступна на запис папка теж має відмовити — MkdirAll на
	// ній мовчить, ловить саме проба.
	err := EnsureDownloadDir(parent)
	if !errors.Is(err, errs.ErrNoWriteAccess) {
		t.Fatalf("EnsureDownloadDir на 0500 = %v, очікував ErrNoWriteAccess", err)
	}
	// Без шляху в тексті незрозуміло, яку саме папку чинити.
	if !strings.Contains(err.Error(), parent) {
		t.Fatalf("у помилці немає шляху: %v", err)
	}
}

func TestProbeDownloadDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "немає")
	exists, writable, free := ProbeDownloadDir(dir)
	if exists || writable || free != 0 {
		t.Fatalf("ProbeDownloadDir = %v %v %d", exists, writable, free)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor створив папку: %v", err)
	}
	if exists, _, _ := ProbeDownloadDir(""); exists {
		t.Fatal("порожній шлях не існує")
	}
	// Файл замість папки — теж «немає папки», а не паніка.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exists, _, _ := ProbeDownloadDir(file); exists {
		t.Fatal("файл не папка")
	}
}

func TestProbeDownloadDirExisting(t *testing.T) {
	dir := t.TempDir()
	exists, writable, free := ProbeDownloadDir(dir)
	if !exists || !writable {
		t.Fatalf("ProbeDownloadDir = %v %v", exists, writable)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if free <= 0 {
			t.Fatalf("free = %d, очікував відоме додатне значення", free)
		}
		if n, ok := FreeBytes(dir); !ok || n <= 0 {
			t.Fatalf("FreeBytes = %d %v", n, ok)
		}
	}
	if _, ok := FreeBytes(filepath.Join(dir, "немає")); ok {
		t.Fatal("FreeBytes на неіснуючому каталозі має казати «невідомо»")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("проба лишила сміття: %v", entries)
	}
}

func TestProbeDownloadDirReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ігнорує права доступу")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	exists, writable, _ := ProbeDownloadDir(dir)
	if !exists || writable {
		t.Fatalf("ProbeDownloadDir = %v %v, очікував існує/не записуємо", exists, writable)
	}
}
