package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/i18n"
)

// Наскрізні сценарії команди download: справжні провайдер, екстрактори, план,
// конвеєр і CLI-вивід; підставні — лише відеохост (стенд downloadtest) і
// каталог даних. Мережі немає жодної.

// streamHosts — хости, на які показують фікстурні embed-и екстракторів.
// Запит за потоком іде на стенд, усе інше (сторінка тайтлу, embed) — на
// фікстури: так один транспорт обслуговує весь шлях від пошуку до сегмента.
var streamHosts = map[string]bool{
	"ashdi.vip":          true,
	"s.moonanime.art":    true,
	"calypso.tortuga.tw": true,
}

// streamStub відводить запити за потоком на HLS-стенд. Шлях переписується в
// розкладку стенда: фікстурний URL потоку (…/hls/…/index.m3u8) стає
// /master.m3u8, а все, що стенд згенерував сам (/q/720/index.m3u8,
// /q/720/segment0.ts), лишається як є — відносні URL у плейлистах стенда
// розв'язуються вже від /master.m3u8.
type streamStub struct {
	harness http.RoundTripper
	next    http.RoundTripper
}

func newStreamStub(hls *downloadtest.HLSServer) streamStub {
	rewrites := map[string]string{}
	for host := range streamHosts {
		for _, base := range hls.Rewrites() {
			rewrites[host] = base
		}
	}
	return streamStub{harness: downloadtest.Transport(rewrites), next: fixtureTransport()}
}

func (s streamStub) RoundTrip(req *http.Request) (*http.Response, error) {
	if !isStreamRequest(req.URL) {
		return s.next.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.URL = harnessURL(req.URL)
	// Стенд вимагає повний набір заголовків moonanime (Referer, User-Agent,
	// Accept, Accept-Language). Обраний фікстурами реліз їх і шле, але інші
	// екстрактори (ashdi) дають лише Referer і User-Agent, тож відсутні
	// дописуємо: строгість чужого хоста перевіряють тести download, а тут
	// важливо, що заголовки потоку дійшли до CDN живими.
	for _, h := range downloadtest.RequiredHeaders {
		if clone.Header.Get(h) == "" {
			clone.Header.Set(h, "*/*")
		}
	}
	return s.harness.RoundTrip(clone)
}

func isStreamRequest(u *url.URL) bool {
	if !streamHosts[u.Hostname()] {
		return false
	}
	return strings.HasSuffix(u.Path, ".m3u8") || strings.Contains(u.Path, "/segment")
}

func harnessURL(u *url.URL) *url.URL {
	out := *u
	if !strings.Contains(u.Path, "/q/") {
		out.Path, out.RawPath, out.RawQuery = "/master.m3u8", "", ""
	}
	return &out
}

// downloadEnv — cliEnv плюс стенд відеохоста на шві newTransport.
func downloadEnv(t *testing.T, opts downloadtest.HLSOptions) (dataDir, dlDir string, hls *downloadtest.HLSServer) {
	t.Helper()
	dataDir = cliEnv(t)
	if opts.Host == "" {
		// Хост потоку, який дають фікстури для обраного релізу (moonanime);
		// саме він стоїть в абсолютних URL варіантів у master-плейлисті стенда.
		opts.Host = "s.moonanime.art"
	}
	if opts.Referer == "" {
		// Фікстури детерміновані, тож стенд вимагає рівно те значення, яке
		// шле екстрактор: так тест ловить втрату заголовків потоку по дорозі
		// до CDN — найчастішу поломку завантаження.
		opts.Referer = "https://moonanime.art/"
	}
	hls = downloadtest.NewHLS(t, opts)
	stub := newStreamStub(hls)
	saved := newTransport
	newTransport = func() http.RoundTripper { return stub }
	t.Cleanup(func() { newTransport = saved })
	return dataDir, t.TempDir(), hls
}

// savedFile знаходить збережену серію за ref, а не за ім'ям файла: назву
// збирає сам завантажувач, і тест не має повторювати її правила.
func savedFile(t *testing.T, dir string, ep int) download.SavedFile {
	t.Helper()
	files := download.Saved(dir, fixtureRef(t))[ep]
	if len(files) != 1 {
		t.Fatalf("Saved(%s)[%d] = %+v, очікував рівно один файл", dir, ep, files)
	}
	return files[0]
}

// walkDir — усі файли дерева відносно кореня (для перевірок «нічого не лишилось»).
func walkDir(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", root, err)
	}
	return out
}

// Головний сценарій: обрана якість, цілий файл, sidecar і marker на місці.
func TestDownloadSavesEpisode(t *testing.T) {
	_, dlDir, hls := downloadEnv(t, downloadtest.HLSOptions{})

	code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir)
	if code != 0 {
		t.Fatalf("download = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	f := savedFile(t, dlDir, 1)
	if f.Height != 720 {
		t.Fatalf("збережено %dp, очікував 720p", f.Height)
	}
	body, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", f.Path, err)
	}
	if want := hls.Expected(720); string(body) != string(want) {
		t.Fatalf("байти файла = %d, очікував %d", len(body), len(want))
	}
	if f.Bytes != int64(len(body)) {
		t.Fatalf("SavedFile.Bytes = %d, очікував %d", f.Bytes, len(body))
	}
	// Якість справді просили в стенда, а не взяли першу-ліпшу.
	if !strings.Contains(strings.Join(hls.Paths(), " "), "/q/720/") {
		t.Fatalf("стенд не бачив /q/720/: %v", hls.Paths())
	}
	// Ідентичність збереженого: marker папки і sidecar файла.
	folder := filepath.Dir(f.Path)
	if _, err := os.Stat(filepath.Join(folder, ".uaanime-title.json")); err != nil {
		t.Fatalf("marker папки: %v", err)
	}
	sidecar := filepath.Join(folder, "."+filepath.Base(f.Path)+".json")
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	mustContain(t, "stdout", out, f.Path)
	// Нічого зайвого: ані .part, ані плейсхолдера.
	for _, name := range walkDir(t, dlDir) {
		if strings.Contains(name, ".part") {
			t.Fatalf("лишився %s", name)
		}
	}

	// Повторний запуск того самого завдання не качає вдруге і чесно каже, чому.
	code, _, errOut = runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir)
	if code != 1 {
		t.Fatalf("повторний download = %d, want 1", code)
	}
	mustContain(t, "stderr", errOut, i18n.MsgAlreadySaved)
}

// --quality, якої в релізі немає: відмова зі списком наявних, а не мовчазна
// підміна на найближчу.
func TestDownloadMissingQuality(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})

	code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "4320", "--dir", dlDir)
	if code != 1 {
		t.Fatalf("download --quality 4320 = %d, want 1\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{"1080p", "720p", "480p"} {
		mustContain(t, "stderr", errOut, want)
	}
	if files := walkDir(t, dlDir); len(files) != 0 {
		t.Fatalf("папка завантажень не порожня: %v", files)
	}
}

// Ctrl+C посеред завантаження: код 1, повідомлення про скасування і жодного
// сліду на диску — ані .part, ані порожнього плейсхолдера.
func TestDownloadCancelledBySignal(t *testing.T) {
	_, dlDir, hls := downloadEnv(t, downloadtest.HLSOptions{Segments: 8})
	release := make(chan struct{})
	defer close(release)
	hls.BlockSegment(6, release)

	type result struct {
		code   int
		errOut string
	}
	done := make(chan result, 1)
	go func() {
		code, _, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir)
		done <- result{code, errOut}
	}()

	// Сигнал шлемо лише тоді, коли завантаження справді пішло: NotifyContext
	// уже встановлено, тож SIGINT дістанеться команді, а не вб'є тест.
	waitFor(t, 10*time.Second, func() bool {
		return strings.Contains(strings.Join(hls.Paths(), " "), "segment6.ts")
	})
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}

	select {
	case r := <-done:
		if r.code != 1 {
			t.Fatalf("download після SIGINT = %d, want 1", r.code)
		}
		mustContain(t, "stderr", r.errOut, i18n.MsgDownloadCancelled)
	case <-time.After(20 * time.Second):
		t.Fatal("download не завершився після SIGINT")
	}
	for _, name := range walkDir(t, dlDir) {
		if strings.Contains(name, ".part") || strings.HasSuffix(name, ".ts") {
			t.Fatalf("після скасування лишився %s", name)
		}
	}
}

// --json: рівно один об'єкт на stdout і жодного рядка прогресу.
func TestDownloadJSON(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})

	code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--json", "--dir", dlDir)
	if code != 0 {
		t.Fatalf("download --json = %d, want 0\nstderr: %s", code, errOut)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var got downloadResult
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("розбір %q: %v", out, err)
	}
	if dec.More() {
		t.Fatalf("на stdout більше одного об'єкта: %q", out)
	}
	f := savedFile(t, dlDir, 1)
	if got.Path != f.Path || got.Bytes != f.Bytes || got.Height != f.Height {
		t.Fatalf("JSON = %+v, очікував path=%s bytes=%d height=%d", got, f.Path, f.Bytes, f.Height)
	}
	if got.Seconds < 0 {
		t.Fatalf("seconds = %v", got.Seconds)
	}
}

// Збережена серія грає з диска: у команді плеєра — шлях до файла і жодного
// http-URL. Доказ того, що локальне відтворення працює і в headless.
func TestPlayDryRunUsesSavedFile(t *testing.T) {
	dataDir, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})
	cfg := []byte(`{"download_dir":` + strconv.Quote(dlDir) + `}`)
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatalf("config.json: %v", err)
	}

	code, _, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "1080")
	if code != 0 {
		t.Fatalf("download = %d, want 0\nstderr: %s", code, errOut)
	}
	f := savedFile(t, dlDir, 1)

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	if code != 0 {
		t.Fatalf("play --dry-run = %d, want 0\nstderr: %s", code, errOut)
	}
	argv := lastLine(out)
	mustContain(t, "argv", argv, f.Path)
	if strings.Contains(argv, "http://") || strings.Contains(argv, "https://") {
		t.Fatalf("argv тягне з мережі: %s", argv)
	}
}

// doctor знає про папку завантажень і не створює її.
func TestDoctorDownloadBlock(t *testing.T) {
	dataDir := cliEnv(t)
	dlDir := filepath.Join(t.TempDir(), "downloads")
	cfg := []byte(`{"download_dir":` + strconv.Quote(dlDir) + `}`)
	if err := os.WriteFile(filepath.Join(dataDir, "config.json"), cfg, 0o600); err != nil {
		t.Fatalf("config.json: %v", err)
	}

	code, out, _ := runCLI(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json = %d, want 0", code)
	}
	var rep doctorReport
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("розбір doctor --json: %v", err)
	}
	if rep.Download.Dir != dlDir || rep.Download.Exists || rep.Download.Writable {
		t.Fatalf("download-блок = %+v, очікував неіснуючу %s", rep.Download, dlDir)
	}
	if _, err := os.Stat(dlDir); !os.IsNotExist(err) {
		t.Fatalf("doctor створив папку завантажень: %v", err)
	}

	if err := os.MkdirAll(dlDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	code, out, _ = runCLI(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor = %d, want 0", code)
	}
	mustContain(t, "stdout", out, dlDir)
}

func waitFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("не дочекалися умови")
}

// Тайтл, якого немає в бібліотеці: назву дістає провайдер ще до черги, і вона
// видна на диску — у папці, в імені файла, в marker і в sidecar. Без цього
// headless download лишав би скрізь слаг, який нічого не каже людині.
func TestDownloadNamesTitleFromProvider(t *testing.T) {
	_, dlDir, _ := downloadEnv(t, downloadtest.HLSOptions{})

	code, out, errOut := runCLI(t, "download", fixtureTitleID, "1", "--quality", "720", "--dir", dlDir)
	if code != 0 {
		t.Fatalf("download = %d, want 0\nstdout: %s\nstderr: %s", code, out, errOut)
	}

	titles := download.SavedAll(dlDir)
	if len(titles) != 1 || titles[0].Ref.Name != fixtureTitleName {
		t.Fatalf("SavedAll = %+v, очікував один тайтл із назвою %q", titles, fixtureTitleName)
	}

	f := savedFile(t, dlDir, 1)
	folder := filepath.Dir(f.Path)
	// Правила іменування — не справа тесту: беремо ті самі функції, що й код.
	if base, want := filepath.Base(folder), download.FolderName(fixtureTitleName); base != want {
		t.Errorf("папка = %q, очікував %q", base, want)
	}
	if base := filepath.Base(f.Path); !strings.HasPrefix(base, fixtureTitleName+" - ") {
		t.Errorf("ім'я файла = %q, очікував початок із назви тайтлу", base)
	}
	if marker, ok := download.ReadMarker(folder); !ok || marker.Name != fixtureTitleName {
		t.Errorf("marker = %+v (знайдено=%v), очікував назву %q", marker, ok, fixtureTitleName)
	}
	var side download.Sidecar
	data, err := os.ReadFile(filepath.Join(folder, "."+filepath.Base(f.Path)+".json"))
	if err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	if err := json.Unmarshal(data, &side); err != nil {
		t.Fatalf("розбір sidecar: %v", err)
	}
	if side.Name != fixtureTitleName {
		t.Errorf("sidecar.name = %q, очікував %q", side.Name, fixtureTitleName)
	}
	// Слаг лишається ключем ідентичності, але на видимі імена не потрапляє.
	for _, name := range walkDir(t, dlDir) {
		if strings.Contains(name, "4465-frren") {
			t.Errorf("на диску лишився слаг: %s", name)
		}
	}
}
