package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Тести конвеєра ганяють реальні HTTP-стенди (downloadtest) і справжню ФС у
// t.TempDir: усе, що ламається в завантажувачі, ламається саме на стику
// «мережа → диск», і мок одного з боків ховав би половину помилок.

// quick скорочує паузи ретраїв і watchdog до тестових масштабів. Обидві —
// пакетні змінні саме заради цього; тести не паралельні, тож підміна безпечна.
func quick(t *testing.T, idle time.Duration) {
	t.Helper()
	oldIdle, oldBackoff := idleTimeout, backoffBase
	idleTimeout, backoffBase = idle, 2*time.Millisecond
	t.Cleanup(func() { idleTimeout, backoffBase = oldIdle, oldBackoff })
}

func jobFor(dir string, ref provider.TitleRef, ep, height int, streamURL string) Job {
	return Job{
		Ref: ref, Episode: ep, Height: height, Dir: dir,
		Resolve: func(context.Context) (Target, error) {
			return Target{
				Streams: []extractor.Stream{{URL: streamURL, Headers: streamHeaders}},
				Studio:  "FanVoxUA",
				Kind:    provider.KindDub,
				Name:    ref.Name,
			}, nil
		},
	}
}

func fetcherFor(rt http.RoundTripper) *httpx.Fetcher { return NewFetcher(rt) }

// entries — усе, що лежить у папці тайтлу, для перевірок «нічого не лишилось».
func entries(t *testing.T, folder string) []string {
	t.Helper()
	ents, err := os.ReadDir(folder)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("ReadDir %s: %v", folder, err)
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// assertClean: після невдачі в папці немає ні .part, ні плейсхолдера, ні
// sidecar-а — лише marker належності папки.
func assertClean(t *testing.T, folder string) {
	t.Helper()
	for _, name := range entries(t, folder) {
		if name != markerName {
			t.Errorf("у папці лишилося %q", name)
		}
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("читання %s: %v", path, err)
	}
	return b
}

func TestRunHLSWritesSegmentsInOrder(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	// Ранній сегмент повільніший за пізні: без упорядкованого writer'а байти
	// лягли б у файл у порядку завершення, і саме цього тест не має пробачити.
	s.SlowSegment(1, 80*time.Millisecond)

	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(s.Rewrites())), jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if p.State != StateDone || p.SegmentsDone != 8 {
		t.Fatalf("прогрес = %+v", p)
	}
	want := s.Expected(720)
	if got := readFile(t, p.Path); !bytes.Equal(got, want) {
		t.Fatalf("байти файла не збігаються зі склейкою сегментів (%d проти %d)", len(got), len(want))
	}
	if p.Bytes != int64(len(want)) || p.Total != p.Bytes {
		t.Errorf("Bytes = %d, Total = %d, очікували %d", p.Bytes, p.Total, len(want))
	}

	folder := filepath.Dir(p.Path)
	if filepath.Base(p.Path) != "Фрірен - 06 - FanVoxUA [Дуб, 720p].ts" {
		t.Errorf("ім'я файла = %q", filepath.Base(p.Path))
	}
	sc, ok := readSidecar(p.Path)
	if !ok || sc.Slug != refFrieren.Slug || sc.Episode != 6 || sc.Height != 720 || sc.Studio != "FanVoxUA" {
		t.Errorf("sidecar = %+v, ok = %v", sc, ok)
	}
	for _, name := range entries(t, folder) {
		if strings.HasSuffix(name, partSuffix) {
			t.Errorf(".part лишився: %q", name)
		}
	}
	// Завантажене видно звичайним пошуком — заради нього все й робиться.
	if got := Saved(dir, refFrieren)[6]; len(got) != 1 || got[0].Height != 720 {
		t.Errorf("Saved = %+v", got)
	}
}

func TestRunHLSRetriesSegment(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	s.FailSegment(3, 2, http.StatusInternalServerError)

	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(s.Rewrites())), jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readFile(t, p.Path); !bytes.Equal(got, s.Expected(720)) {
		t.Fatalf("байти після ретраю не збігаються")
	}
}

func TestRunHLSPermanentFailureLeavesNothing(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	s.FailSegment(2, 99, http.StatusInternalServerError)

	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(s.Rewrites())), jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err == nil || p.State != StateFailed {
		t.Fatalf("очікували провал, маємо %+v", p)
	}
	assertClean(t, filepath.Dir(p.Path))
	if _, err := os.Stat(p.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("плейсхолдер лишився: %v", err)
	}
}

func TestRunHLSExpiredLinkIsNotRetried(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	s.FailSegment(2, 99, http.StatusForbidden)

	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(s.Rewrites())), jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if !errors.Is(err, errs.ErrStreamExpired) {
		t.Fatalf("err = %v, очікували ErrStreamExpired", err)
	}
	if p.State != StateFailed {
		t.Errorf("стан = %v", p.State)
	}
	tries := 0
	for _, path := range s.Paths() {
		if strings.HasSuffix(path, "segment2.ts") {
			tries++
		}
	}
	if tries != 1 {
		t.Errorf("сегмент 2 запитано %d разів — протухле посилання не повторюють", tries)
	}
	assertClean(t, filepath.Dir(p.Path))
}

func TestRunHLSCancelLeavesNothing(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	var once sync.Once
	report := func(p Progress) {
		if p.SegmentsDone > 0 {
			once.Do(func() { close(started) })
		}
	}

	type result struct {
		p   Progress
		err error
	}
	done := make(chan result, 1)
	go func() {
		p, err := run(ctx, fetcherFor(downloadtest.Transport(s.Rewrites())), jobFor(dir, refFrieren, 6, 720, s.URL()), report)
		done <- result{p, err}
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("завантаження не почалося")
	}
	cancel()

	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("скасування не завершило завдання")
	}
	if !errors.Is(r.err, errs.ErrCancelled) || r.p.State != StateCancelled {
		t.Fatalf("err = %v, стан = %v", r.err, r.p.State)
	}
	assertClean(t, filepath.Dir(r.p.Path))
}

// stallOnceServer — власний мінімальний стенд: у downloadtest ручки
// «зависнути рівно один раз» немає навмисно (вона потрібна лише тут), а саме
// одноразове зависання доводить, що watchdog рятує спробу, а не вбиває
// завдання цілком.
func stallOnceServer(t *testing.T, host string, segments, stallSeg int) (string, map[string]string) {
	t.Helper()
	done := make(chan struct{})
	var stalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.m3u8") {
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
			for i := range segments {
				fmt.Fprintf(&b, "#EXTINF:5.0,\nseg%d.ts\n", i)
			}
			b.WriteString("#EXT-X-ENDLIST\n")
			_, _ = fmt.Fprint(w, b.String())
			return
		}
		i := -1
		_, _ = fmt.Sscanf(filepath.Base(r.URL.Path), "seg%d.ts", &i)
		if i < 0 || i >= segments {
			http.NotFound(w, r)
			return
		}
		body := downloadtest.Segment(i)
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if i == stallSeg && stalled.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-done:
			}
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(func() {
		close(done)
		srv.Close()
	})
	return "https://" + host + "/index.m3u8", map[string]string{host: srv.URL}
}

func TestRunHLSWatchdogRetriesStalledSegment(t *testing.T) {
	quick(t, 60*time.Millisecond)
	dir := t.TempDir()
	url, rewrites := stallOnceServer(t, "stall.test", 6, 2)

	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(rewrites)), jobFor(dir, refFrieren, 6, 0, url), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var want []byte
	for i := range 6 {
		want = append(want, downloadtest.Segment(i)...)
	}
	if got := readFile(t, p.Path); !bytes.Equal(got, want) {
		t.Fatalf("байти після watchdog-ретраю не збігаються (%d проти %d)", len(got), len(want))
	}
}

// ------------------------------------------------------------- прямий файл

// recorder запам'ятовує заголовки запитів: інакше «докачав» і «перекачав з
// нуля» дають однаковий файл і тест не відрізнив би їх.
type recorder struct {
	next http.RoundTripper
	// after викликається після кожного запиту до медіафайла з його номером:
	// стенд CutAfter/Replace мусить спрацювати на конкретній спробі, а не
	// «десь потім», інакше тест ловив би гонку замість поведінки.
	after func(n int)
	mu    sync.Mutex
	reqs  []recorded
}

type recorded struct {
	path    string
	rng     string
	ifRange string
}

func (r *recorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, recorded{req.URL.Path, req.Header.Get("Range"), req.Header.Get("If-Range")})
	r.mu.Unlock()
	res, err := r.next.RoundTrip(req)
	if n := len(r.media()); r.after != nil && strings.HasSuffix(req.URL.Path, ".webm") {
		r.after(n)
	}
	return res, err
}

func (r *recorder) media() []recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recorded
	for _, req := range r.reqs {
		if strings.HasSuffix(req.path, ".webm") {
			out = append(out, req)
		}
	}
	return out
}

func filePayload(n int, fill byte) []byte { return bytes.Repeat([]byte{fill}, n) }

func runFile(t *testing.T, dir string, s *downloadtest.FileServer, after func(n int)) (*recorder, Progress, error) {
	t.Helper()
	rec := &recorder{next: downloadtest.Transport(s.Rewrites()), after: after}
	p, err := run(t.Context(), fetcherFor(rec), jobFor(dir, refFrieren, 6, 0, s.URL()), nil)
	return rec, p, err
}

func TestRunFileResumesWithRange(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	payload := filePayload(20000, 'a')
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{Referer: streamHeaders["Referer"]})
	// Обрив саме на качанні: перший запит до CDN — це зондування плану.
	rec, p, err := runFile(t, dir, s, func(n int) {
		if n == 1 {
			s.CutAfter(8000)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readFile(t, p.Path); !bytes.Equal(got, payload) {
		t.Fatalf("файл після докачки (%d байт) не дорівнює оригіналу (%d)", len(got), len(payload))
	}
	if filepath.Ext(p.Path) != ".webm" {
		t.Errorf("розширення = %q, очікували .webm", filepath.Ext(p.Path))
	}
	reqs := rec.media()
	// Перший запит — зондування плану, другий — качання з нуля, третій — докачка.
	last := reqs[len(reqs)-1]
	if last.rng != "bytes=8000-" || last.ifRange != s.ETag() {
		t.Errorf("остання спроба = %+v, очікували Range з 8000 і If-Range з ETag", last)
	}
}

func TestRunFileRestartsWhenServerIgnoresRange(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	payload := filePayload(20000, 'b')
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{IgnoreRange: true})
	_, p, err := runFile(t, dir, s, func(n int) {
		if n == 1 {
			s.CutAfter(8000)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readFile(t, p.Path); !bytes.Equal(got, payload) {
		t.Fatalf("сервер проігнорував Range — файл мав бути перекачаний з нуля, а він %d байт", len(got))
	}
}

func TestRunFileWithoutValidatorRestartsFromZero(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	payload := filePayload(20000, 'c')
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{NoValidator: true})
	rec, p, err := runFile(t, dir, s, func(n int) {
		if n == 1 {
			s.CutAfter(8000)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := readFile(t, p.Path); !bytes.Equal(got, payload) {
		t.Fatalf("файл (%d байт) не дорівнює оригіналу (%d)", len(got), len(payload))
	}
	// Перший запит — зондування плану, воно завжди з Range; далі Range бути не може.
	for _, req := range rec.media()[1:] {
		if req.rng != "" {
			t.Errorf("без валідатора Range слати не можна, а був %q", req.rng)
		}
	}
}

func TestRunFileReplacedPayloadIsNotStitched(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	first := filePayload(20000, 'd')
	second := filePayload(20000, 'e')
	s := downloadtest.NewFile(t, first, downloadtest.FileOptions{})

	// Обрив на качанні, а одразу після нього — підміна файла на CDN: валідатор
	// змінюється, і докачка мусить перетворитися на качання з нуля.
	_, p, err := runFile(t, dir, s, func(n int) {
		switch n {
		case 1:
			s.CutAfter(8000)
		case 2:
			s.Replace(second)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := readFile(t, p.Path)
	if !bytes.Equal(got, second) {
		t.Fatalf("файл не дорівнює новій версії: перші байти %q, довжина %d", got[:8], len(got))
	}
}

// ------------------------------------------------------------- власність цілі

func TestRunAlreadySavedWritesMissingSidecar(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	// Перший прогін створює файл; другий має впертися в нього.
	first, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("перший run: %v", err)
	}
	if err := os.Remove(sidecarPath(first.Path)); err != nil {
		t.Fatalf("прибрати sidecar: %v", err)
	}
	before := len(s.Paths())

	_, err = run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if !errors.Is(err, errs.ErrAlreadySaved) {
		t.Fatalf("err = %v, очікували ErrAlreadySaved", err)
	}
	for _, path := range s.Paths()[before:] {
		if strings.Contains(path, "segment") {
			t.Errorf("по наявний файл ходили в мережу за %s", path)
		}
	}
	if _, ok := readSidecar(first.Path); !ok {
		t.Error("sidecar не дописано (reconciliation)")
	}
}

func TestRunAlreadySavedKeepsForeignFolderIntact(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	first, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("перший run: %v", err)
	}
	folder := filepath.Dir(first.Path)
	if err := os.Remove(sidecarPath(first.Path)); err != nil {
		t.Fatalf("прибрати sidecar: %v", err)
	}
	// У папці лежить файл ІНШОГО тайтлу зі своїм sidecar — приписувати їй наш
	// паспорт не можна.
	writeMedia(t, folder, "чужий.ts", 10, &Sidecar{Provider: refOther.Provider, Slug: refOther.Slug, Episode: 1})

	if _, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil); !errors.Is(err, errs.ErrAlreadySaved) {
		t.Fatalf("err = %v, очікували ErrAlreadySaved", err)
	}
	if _, ok := readSidecar(first.Path); ok {
		t.Error("sidecar дописано в папку з файлами іншого тайтлу")
	}
}

func TestRunReusesStalePlaceholder(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	folder, err := FolderFor(dir, refFrieren, refFrieren.Name)
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	final := filepath.Join(folder, Filename(Parts{
		Title: refFrieren.Name, Episode: 6, Kind: provider.KindDub,
		Studio: "FanVoxUA", Height: 720, Ext: ".ts",
	}))
	if err := os.WriteFile(final, nil, 0o644); err != nil {
		t.Fatalf("плейсхолдер: %v", err)
	}

	p, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if p.Path != final {
		t.Fatalf("шлях = %q, очікували %q", p.Path, final)
	}
	if got := readFile(t, final); !bytes.Equal(got, s.Expected(720)) {
		t.Error("застарілий плейсхолдер не перевикористано")
	}
}

func TestRunSecondJobOnSameRefIsBusy(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 8, Referer: streamHeaders["Referer"]})
	release := make(chan struct{})
	s.BlockSegment(5, release)
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	started := make(chan struct{})
	var once sync.Once
	done := make(chan error, 1)
	go func() {
		_, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), func(p Progress) {
			if p.SegmentsDone > 0 {
				once.Do(func() { close(started) })
			}
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("перше завдання не почалося")
	}

	// Друге завдання того самого тайтлу (інша серія, порожній Name — так
	// виглядає headless `download`): лок стабільний за provider+slug.
	second := jobFor(dir, provider.TitleRef{Provider: refFrieren.Provider, Slug: refFrieren.Slug}, 7, 720, s.URL())
	if _, err := run(t.Context(), f, second, nil); !errors.Is(err, errs.ErrDownloadBusy) {
		t.Fatalf("err = %v, очікували ErrDownloadBusy", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("перше завдання: %v", err)
	}
}

func TestRunSameFolderWithAndWithoutName(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	withName, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("run з назвою: %v", err)
	}
	bare := provider.TitleRef{Provider: refFrieren.Provider, Slug: refFrieren.Slug}
	job := jobFor(dir, bare, 7, 720, s.URL())
	job.Resolve = func(context.Context) (Target, error) {
		return Target{
			Streams: []extractor.Stream{{URL: s.URL(), Headers: streamHeaders}},
			Studio:  "FanVoxUA", Kind: provider.KindDub,
		}, nil
	}
	without, err := run(t.Context(), f, job, nil)
	if err != nil {
		t.Fatalf("run без назви: %v", err)
	}
	if filepath.Dir(withName.Path) != filepath.Dir(without.Path) {
		t.Fatalf("папки різні: %q і %q", filepath.Dir(withName.Path), filepath.Dir(without.Path))
	}
}

func TestRunTwoHeightsGiveTwoFiles(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	lo, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil)
	if err != nil {
		t.Fatalf("720: %v", err)
	}
	hi, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 1080, s.URL()), nil)
	if err != nil {
		t.Fatalf("1080: %v", err)
	}
	if lo.Path == hi.Path {
		t.Fatal("обидві якості лягли в один файл")
	}
	if got := Saved(dir, refFrieren)[6]; len(got) != 2 || got[0].Height != 1080 {
		t.Fatalf("Saved = %+v", got)
	}
}

func TestRunNoWriteAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root пише куди завгодно")
	}
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	folder, err := FolderFor(dir, refFrieren, refFrieren.Name)
	if err != nil {
		t.Fatalf("FolderFor: %v", err)
	}
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(folder, 0o755) })

	if _, err := run(t.Context(), f, jobFor(dir, refFrieren, 6, 720, s.URL()), nil); !errors.Is(err, errs.ErrNoWriteAccess) {
		t.Fatalf("err = %v, очікували ErrNoWriteAccess", err)
	}
}

// Два РІЗНІ тайтли з однаковою назвою качаються одночасно: папки мають
// роз'їхатися, а файли й sidecar-и — лишитися своїми.
func TestRunConcurrentFolderCollision(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 4, Referer: streamHeaders["Referer"]})
	f := fetcherFor(downloadtest.Transport(s.Rewrites()))

	var wg sync.WaitGroup
	out := make([]Progress, 2)
	errsOut := make([]error, 2)
	for i, ref := range []provider.TitleRef{refFrieren, refOther} {
		wg.Add(1)
		go func(i int, ref provider.TitleRef) {
			defer wg.Done()
			out[i], errsOut[i] = run(t.Context(), f, jobFor(dir, ref, 6, 720, s.URL()), nil)
		}(i, ref)
	}
	wg.Wait()

	for i, err := range errsOut {
		if err != nil {
			t.Fatalf("завдання %d: %v", i, err)
		}
	}
	if filepath.Dir(out[0].Path) == filepath.Dir(out[1].Path) {
		t.Fatalf("обидва тайтли зайняли одну папку %q", filepath.Dir(out[0].Path))
	}
	for i, ref := range []provider.TitleRef{refFrieren, refOther} {
		if got := readFile(t, out[i].Path); !bytes.Equal(got, s.Expected(720)) {
			t.Errorf("файл тайтлу %s пошкоджено", ref.Slug)
		}
		sc, ok := readSidecar(out[i].Path)
		if !ok || sc.Slug != ref.Slug {
			t.Errorf("sidecar %s = %+v, ok = %v", ref.Slug, sc, ok)
		}
	}
}

func TestRunResolveFailure(t *testing.T) {
	quick(t, time.Second)
	dir := t.TempDir()
	job := Job{Ref: refFrieren, Episode: 6, Dir: dir, Resolve: func(context.Context) (Target, error) {
		return Target{}, fmt.Errorf("джерело: %w", errs.ErrNoStream)
	}}
	p, err := run(t.Context(), fetcherFor(downloadtest.Transport(nil)), job, nil)
	if !errors.Is(err, errs.ErrNoStream) || p.State != StateFailed {
		t.Fatalf("err = %v, стан = %v", err, p.State)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("до резолюції на диску нічого створювати не можна: %v", got)
	}
}
