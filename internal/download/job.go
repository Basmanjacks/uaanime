package download

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

const (
	// hlsWorkers — скільки сегментів тягнемо одночасно. Чотири тримають канал
	// зайнятим і водночас обмежують пам'ять: живий сегмент — 1–2 МБ, тож у
	// найгіршому разі в пам'яті (workers+1)×maxSegmentSize.
	hlsWorkers = 4
	// maxSegmentSize — стеля одного сегмента. Найбільший живий — 2 МБ; 16 МіБ
	// це восьмикратний запас і водночас межа, за якою недовірений плейлист не
	// зможе змусити нас з'їсти пам'ять.
	maxSegmentSize = 16 << 20
	// attempts — скільки разів пробуємо один сегмент або одне тіло файла.
	attempts = 3
	// writeBuffer — 256 КіБ: один запис на 64 сегменти замість одного на кожен.
	writeBuffer = 256 << 10
	// orphanAge — з якого віку власний .part вважається сирітським (kill -9).
	// Доба: довше за будь-яке реальне завантаження серії.
	orphanAge = 24 * time.Hour
	// reportEvery — не частіше за 4 оновлення прогресу на секунду (D1 «Тиха
	// черга»: рядок не має миготіти).
	reportEvery = 250 * time.Millisecond
	// spaceReserve — скільки місця лишаємо системі понад розмір серії.
	spaceReserve = 64 << 20
	// rateWindow — вікно згладжування швидкості.
	rateWindow = 3.0
)

// idleTimeout — скільки тіло відповіді може мовчати, перш ніж спробу скасує
// watchdog. httpx свідомо не має Client.Timeout (файл качається годину), а
// ResponseHeaderTimeout тіла не покриває: без цього завислий CDN тримав би
// чергу вічно. Змінна, а не константа, щоб тести не чекали хвилину.
var idleTimeout = 60 * time.Second

// backoffBase — перша пауза між спробами; далі подвоєння плюс джитер.
var backoffBase = 300 * time.Millisecond

// errStalled — тіло перестало віддавати байти й спробу скасував watchdog. Це
// транспортний збій, а не скасування завдання, тож ретрай доречний.
var errStalled = errors.New("тіло відповіді зупинилося")

// errTooBig — сегмент більший за стелю: повторювати немає сенсу.
var errTooBig = errors.New("сегмент завеликий")

// Target — те, що резолюція дала для конкретної серії: потоки обраного релізу
// і його метадані для імені файла й sidecar.
type Target struct {
	Streams []extractor.Stream
	Studio  string
	Kind    provider.Kind
	Name    string
}

// ResolveFunc повертає потоки серії. Викликається на СТАРТІ завдання, а не
// при постановці в чергу: URL потоків протухають (правило 5), і посилання,
// здобуте годину тому в черзі, вже не відкриється.
type ResolveFunc func(ctx context.Context) (Target, error)

// Job — одне завдання черги. Dir — знімок налаштування: зміна папки посеред
// завантаження не має перекидати вже запущене завдання в інше місце.
type Job struct {
	Ref     provider.TitleRef
	Episode int
	Height  int
	Dir     string
	Resolve ResolveFunc
}

// State — стан завдання. Термінальні три: Done, Failed, Cancelled.
type State int

const (
	StateQueued State = iota
	StateResolving
	StateRunning
	StateDone
	StateFailed
	StateCancelled
)

func (s State) Terminal() bool { return s >= StateDone }

func (s State) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateResolving:
		return "resolving"
	case StateRunning:
		return "running"
	case StateDone:
		return "done"
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "cancelled"
	}
	return "unknown"
}

// Progress — знімок стану завдання. Лише значення: його копіюють у модель UI
// і читають з іншої горутини, тож нічого, що можна змінити ззовні, тут немає.
type Progress struct {
	ID           int64
	Ref          provider.TitleRef
	Episode      int
	Height       int
	State        State
	Bytes        int64
	Total        int64
	SegmentsDone int
	Segments     int
	BytesPerSec  float64
	ETA          time.Duration
	Path         string
	Dir          string
	Err          error
	// Exact — Total названий сервером, а не оцінений за бітрейтом.
	Exact bool
}

// run виконує одне завдання від резолюції до перейменування .part у фінальний
// файл. Повертає останній знімок прогресу — саме його менеджер кладе у
// Snapshot, тож стан завдання після завершення не залежить від того, чи хтось
// читав канал подій.
func run(ctx context.Context, f *httpx.Fetcher, j Job, report func(Progress)) (Progress, error) {
	if report == nil {
		report = func(Progress) {}
	}
	p := Progress{
		Ref:     j.Ref,
		Episode: j.Episode,
		Height:  j.Height,
		Dir:     j.Dir,
		State:   StateResolving,
	}
	report(p)

	err := download(ctx, f, j, &p, report)
	p.BytesPerSec, p.ETA = 0, 0
	switch {
	case err == nil:
		p.State = StateDone
		p.Total, p.Exact = p.Bytes, true
	case ctx.Err() != nil:
		// Скасування завжди має свій текст: людина натиснула X або вийшла з
		// застосунку, і «джерело зламалось» тут було б брехнею.
		p.State = StateCancelled
		p.Err = fmt.Errorf("серія %d: %w", j.Episode, errs.ErrCancelled)
	default:
		p.State = StateFailed
		p.Err = err
	}
	report(p)
	return p, p.Err
}

func download(ctx context.Context, f *httpx.Fetcher, j Job, p *Progress, report func(Progress)) error {
	if j.Resolve == nil {
		return fmt.Errorf("завдання без резолюції: %w", errs.ErrNoStream)
	}
	tgt, err := j.Resolve(ctx)
	if err != nil {
		return err
	}
	plan, err := BuildPlan(ctx, f, tgt.Streams)
	if err != nil {
		return err
	}
	q, ok := plan.Pick(j.Height)
	if !ok {
		return fmt.Errorf("якості %dp немає (є %v): %w", j.Height, plan.Heights(), errs.ErrNoStream)
	}

	p.Height, p.Total, p.Exact = q.Height, q.Bytes, q.Exact
	p.Segments = q.Segments
	p.State = StateRunning
	report(*p)

	if err := store.EnsureDownloadDir(j.Dir); err != nil {
		return err
	}
	// Лок власності на тайтл — до вибору папки і до кінця публікації: саме він
	// робить резервацію final і прибирання застарілого плейсхолдера безпечними.
	lock, err := store.Lock(RefLockPath(j.Dir, j.Ref), errs.ErrDownloadBusy)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	name := tgt.Name
	if name == "" {
		name = j.Ref.Name
	}
	folder, err := FolderFor(j.Dir, j.Ref, name)
	if err != nil {
		return err
	}
	final := Path(j.Dir, folder, Parts{
		Title:   name,
		Episode: j.Episode,
		Kind:    tgt.Kind,
		Studio:  tgt.Studio,
		Height:  q.Height,
		Ext:     plan.Ext,
	})
	p.Path = final
	sc := Sidecar{
		Provider: j.Ref.Provider,
		Slug:     j.Ref.Slug,
		Name:     name,
		Episode:  j.Episode,
		Studio:   tgt.Studio,
		Kind:     tgt.Kind,
		Height:   q.Height,
	}

	if err := reserve(final, folder, j.Ref, sc); err != nil {
		return err
	}
	var part *os.File
	published := false
	defer func() {
		if published {
			return
		}
		// Прибирання під тим самим локом: .part і sidecar — наші завжди,
		// final — лише поки він наш нульовий плейсхолдер. Зареєстровано одразу
		// після резервації, щоб відмова за місцем не лишала порожній файл.
		if part != nil {
			_ = part.Close()
			_ = os.Remove(part.Name())
		}
		_ = os.Remove(sidecarPath(final))
		if info, err := os.Stat(final); err == nil && info.Size() == 0 {
			_ = os.Remove(final)
		}
	}()
	if err := checkSpace(folder, q.Bytes); err != nil {
		return err
	}
	sweepOrphans(final)

	part, err = os.CreateTemp(folder, filepath.Base(final)+".*"+partSuffix)
	if err != nil {
		return diskError(folder, err)
	}

	t := &transfer{f: f, part: part, bw: bufio.NewWriterSize(part, writeBuffer), p: p, report: report}
	t.lastAt = time.Now()
	if plan.Kind == KindHLS {
		err = t.hls(ctx, q)
	} else {
		err = t.file(ctx, q)
	}
	if err != nil {
		return err
	}

	if err := t.bw.Flush(); err != nil {
		return diskError(part.Name(), err)
	}
	// Sync до перейменування: інакше після падіння живлення на місці серії
	// лежав би файл потрібного розміру з нулями всередині.
	if err := part.Sync(); err != nil {
		return diskError(part.Name(), err)
	}
	if err := part.Close(); err != nil {
		return diskError(part.Name(), err)
	}
	// Sidecar раніше за медіа: файл без паспорта застосунок ще підхопить за
	// іменем, а паспорт без файла — сміття, яке нікому не заважає.
	if err := WriteSidecar(final, sc); err != nil {
		return err
	}
	if err := os.Rename(part.Name(), final); err != nil {
		return diskError(final, err)
	}
	published = true
	return nil
}

// reserve займає ім'я фінального файла нульовим плейсхолдером під ref-локом.
// Так другий процес (або друга якість тієї ж серії) одразу бачить, що місце
// зайняте, ще до першого байта з мережі.
func reserve(final, folder string, ref provider.TitleRef, sc Sidecar) error {
	for range 2 {
		f, err := os.OpenFile(final, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return diskError(final, err)
		}
		info, serr := os.Stat(final)
		if serr != nil {
			if errors.Is(serr, os.ErrNotExist) {
				continue // зник між OpenFile і Stat — пробуємо ще раз
			}
			return diskError(final, serr)
		}
		if info.Size() > 0 {
			reconcile(final, folder, ref, sc)
			return fmt.Errorf("%s: %w", filepath.Base(final), errs.ErrAlreadySaved)
		}
		// Нуль байтів під нашим локом означає, що живого власника немає:
		// це резервація процесу, вбитого kill -9.
		if err := os.Remove(final); err != nil && !errors.Is(err, os.ErrNotExist) {
			return diskError(final, err)
		}
	}
	return fmt.Errorf("%s: не вдалося зарезервувати ім'я: %w", final, errs.ErrProvider)
}

// reconcile дописує sidecar до файла, збереженого старішою версією або
// перенесеного руками. Мовчки, бо це не мета операції: людина просила
// завантажити серію, і «вже є» — достатня відповідь.
//
// Чужу папку не чіпаємо: sidecar іншого ref поруч означає, що файл із
// однаковим іменем належить не нам, і приписувати йому наш паспорт не можна.
func reconcile(final, folder string, ref provider.TitleRef, sc Sidecar) {
	if _, ok := readSidecar(final); ok {
		return
	}
	if ownedByOther(folder, ref) {
		return
	}
	_ = WriteSidecar(final, sc)
}

// checkSpace відмовляє ДО першого байта з мережі: витратити півгодини трафіку,
// щоб упертися в ENOSPC на останньому сегменті, — найгірший можливий сценарій.
// Запас: 5 % понад оцінку (вона може бути занижена) плюс 64 МіБ системі.
func checkSpace(folder string, want int64) error {
	if want <= 0 {
		return nil
	}
	free, ok := store.FreeBytes(folder)
	if !ok {
		return nil
	}
	if need := want + want/20 + spaceReserve; free < need {
		return fmt.Errorf("%s: потрібно ~%d байт, вільно %d: %w", folder, need, free, errs.ErrDiskFull)
	}
	return nil
}

// sweepOrphans прибирає ВЛАСНІ незавершені файли цієї ж серії, старші за добу.
// Лише свій шаблон і лише старі: у папці тайтлу лежать файли користувача, і
// прибиральник, що чіпає чуже, гірший за сміття.
func sweepOrphans(final string) {
	folder := filepath.Dir(final)
	prefix := filepath.Base(final) + "."
	ents, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, partSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < orphanAge {
			continue
		}
		_ = os.Remove(filepath.Join(folder, name))
	}
}

// transfer — стан одного переливання в .part: файл, буфер, прогрес.
type transfer struct {
	f      *httpx.Fetcher
	part   *os.File
	bw     *bufio.Writer
	p      *Progress
	report func(Progress)

	lastReport time.Time
	lastAt     time.Time
	lastBytes  int64
	ema        float64
}

// progress звітує не частіше за reportEvery; force — на завершальних подіях,
// які людина мусить побачити негайно.
func (t *transfer) progress(force bool) {
	now := time.Now()
	if !force && now.Sub(t.lastReport) < reportEvery {
		return
	}
	t.rate(now)
	t.lastReport = now
	t.report(*t.p)
}

// rate — експоненційне згладжування швидкості. Миттєве значення стрибає на
// порядок між сегментами, і ETA з нього був би марним.
func (t *transfer) rate(now time.Time) {
	dt := now.Sub(t.lastAt).Seconds()
	if dt < 0.05 {
		return
	}
	v := float64(t.p.Bytes-t.lastBytes) / dt
	if t.ema == 0 {
		t.ema = v
	} else {
		t.ema += (1 - math.Exp(-dt/rateWindow)) * (v - t.ema)
	}
	t.lastAt, t.lastBytes = now, t.p.Bytes
	t.p.BytesPerSec = t.ema
	t.p.ETA = 0
	if t.ema > 0 && t.p.Total > t.p.Bytes {
		t.p.ETA = time.Duration(float64(t.p.Total-t.p.Bytes) / t.ema * float64(time.Second))
	}
}

// slot — результат одного сегмента, відданий writer'у в порядку плейлиста.
type slot struct {
	buf *bytes.Buffer
	err error
}

// hls — впорядкований паралельний конвеєр. Диспетчер іде плейлистом по
// порядку, кожному сегменту видає буфер (їх рівно workers+1 — це і є впуск:
// поки writer не звільнив буфер, новий сегмент не стартує) і кладе у futures
// канал-обіцянку. Writer читає futures в тому ж порядку, тож .part завжди
// містить цілу кількість сегментів підряд — саме на цьому інваріанті в v2
// стане можливим резюм за індексом.
func (t *transfer) hls(ctx context.Context, q Quality) error {
	segs := q.media
	if len(segs) == 0 {
		var err error
		if segs, err = t.fetchMedia(ctx, q); err != nil {
			return err
		}
	}
	t.p.Segments = len(segs)

	jctx, cancel := context.WithCancel(ctx)
	defer cancel()

	free := make(chan *bytes.Buffer, hlsWorkers+1)
	for range hlsWorkers + 1 {
		free <- new(bytes.Buffer)
	}
	futures := make(chan chan slot, hlsWorkers)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(futures)
		for _, seg := range segs {
			var buf *bytes.Buffer
			select {
			case buf = <-free:
			case <-jctx.Done():
				return
			}
			ch := make(chan slot, 1)
			select {
			case futures <- ch:
			case <-jctx.Done():
				return
			}
			wg.Add(1)
			go func(rawURL string, buf *bytes.Buffer, ch chan slot) {
				defer wg.Done()
				// Канал ємності 1: воркер ніколи не блокується на видачі,
				// навіть якщо writer уже пішов через помилку.
				ch <- slot{buf: buf, err: t.segment(jctx, rawURL, q.Headers, buf)}
			}(seg.URL, buf, ch)
		}
	}()

	var err error
	for ch := range futures {
		if err != nil {
			continue // дочитуємо обіцянки, щоб диспетчер не завис на send
		}
		s := <-ch
		if s.err != nil {
			err = s.err
			cancel()
			continue
		}
		if _, werr := t.bw.Write(s.buf.Bytes()); werr != nil {
			err = diskError(t.part.Name(), werr)
			cancel()
			continue
		}
		t.p.Bytes += int64(s.buf.Len())
		t.p.SegmentsDone++
		s.buf.Reset()
		free <- s.buf
		t.progress(false)
	}
	cancel()
	wg.Wait()

	if err == nil && t.p.SegmentsDone != len(segs) {
		// Диспетчер зупинився через скасування контексту: файл неповний, і
		// мовчки видати його за готовий не можна.
		cause := ctx.Err()
		if cause == nil {
			cause = errs.ErrProvider
		}
		err = fmt.Errorf("записано %d з %d сегментів: %w", t.p.SegmentsDone, len(segs), cause)
	}
	if err != nil {
		return err
	}
	t.progress(true)
	return nil
}

// fetchMedia — запасний шлях, коли Quality прийшла без розібраних сегментів
// (план склав не той, хто качає). Ціна — один зайвий запит, тож звичайний
// шлях бере сегменти з плану.
func (t *transfer) fetchMedia(ctx context.Context, q Quality) ([]Segment, error) {
	pr, err := t.f.Probe(ctx, q.URL, q.Headers)
	if err != nil {
		return nil, err
	}
	if pr.Kind != httpx.ProbePlaylist {
		return nil, fmt.Errorf("%s: не плейлист: %w", q.URL, errs.ErrProvider)
	}
	base, err := url.Parse(pr.FinalURL)
	if err != nil {
		return nil, fmt.Errorf("URL плейлиста %q: %w", pr.FinalURL, errs.ErrProvider)
	}
	m, err := ParseMedia(base, pr.Body)
	if err != nil {
		return nil, err
	}
	return m.Segments, nil
}

// segment тягне один сегмент у буфер із ретраями.
func (t *transfer) segment(ctx context.Context, rawURL string, headers map[string]string, buf *bytes.Buffer) error {
	var last error
	for attempt := range attempts {
		if attempt > 0 {
			if err := backoff(ctx, attempt); err != nil {
				return err
			}
		}
		buf.Reset()
		err := t.segmentOnce(ctx, rawURL, headers, buf)
		if err == nil {
			return nil
		}
		if expired(err) {
			// Підписане посилання протухло: ретрай дасть той самий 403,
			// а людині треба просто натиснути D ще раз.
			return fmt.Errorf("%s: %w", rawURL, errs.ErrStreamExpired)
		}
		last = err
		if !retryable(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return last
}

func (t *transfer) segmentOnce(ctx context.Context, rawURL string, headers map[string]string, buf *bytes.Buffer) error {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(idleTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()

	res, err := t.f.Open(actx, rawURL, headers, 0, 0, httpx.Validator{})
	if err != nil {
		return stallOr(&stalled, err)
	}
	defer func() { _ = res.Body.Close() }()

	n, err := pump(func(p []byte) error { buf.Write(p); return nil }, res.Body, timer, maxSegmentSize)
	if err != nil {
		return stallOr(&stalled, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: порожній сегмент: %w", rawURL, errs.ErrProvider)
	}
	return nil
}

// file качає прямий файл одним тілом. Докачка дозволена лише коли сервер дав
// валідатор: без нього однакова довжина не доводить, що байти ті самі, і
// склеєні префікси двох версій дали б файл, який ніде не програється.
func (t *transfer) file(ctx context.Context, q Quality) error {
	var (
		written int64
		total   = q.Bytes
		valid   httpx.Validator
		last    error
	)
	for attempt := range attempts {
		if attempt > 0 {
			if err := backoff(ctx, attempt); err != nil {
				return err
			}
		}
		done, err := t.fileAttempt(ctx, q, &written, &total, &valid)
		if done {
			return nil
		}
		last = err
		if expired(err) {
			return fmt.Errorf("%s: %w", q.URL, errs.ErrStreamExpired)
		}
		if !retryable(err) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if valid.IsZero() {
			// Нічим підтвердити незмінність — починаємо з нуля.
			if rerr := t.restart(&written); rerr != nil {
				return rerr
			}
		}
	}
	return last
}

// fileAttempt — одна спроба. done == true означає «тіло дочитано до кінця»;
// written/total/valid переживають спробу, бо саме вони роблять наступну
// докачкою, а не повторним качанням з нуля.
func (t *transfer) fileAttempt(ctx context.Context, q Quality, written, total *int64, valid *httpx.Validator) (bool, error) {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	var stalled atomic.Bool
	timer := time.AfterFunc(idleTimeout, func() { stalled.Store(true); cancel() })
	defer timer.Stop()

	res, err := t.f.Open(actx, q.URL, q.Headers, *written, *total, *valid)
	if err != nil {
		return false, stallOr(&stalled, err)
	}
	defer func() { _ = res.Body.Close() }()

	if !res.Ranged && *written > 0 {
		// Сервер віддав файл з нуля (проігнорував Range, підмінив валідатор
		// або змінив розмір) — старий префікс злити з новим тілом не можна.
		if rerr := t.restart(written); rerr != nil {
			return false, rerr
		}
	}
	if *written == 0 {
		*total, *valid = res.Total, res.Validator
		t.p.Total, t.p.Exact = *total, *total > 0
	}

	n, err := pump(func(p []byte) error {
		if _, werr := t.bw.Write(p); werr != nil {
			return diskError(t.part.Name(), werr)
		}
		t.p.Bytes += int64(len(p))
		if t.p.Bytes > httpx.MaxFileBytes {
			return fmt.Errorf("файл більший за %d байт: %w", httpx.MaxFileBytes, errs.ErrProvider)
		}
		t.progress(false)
		return nil
	}, res.Body, timer, 0)
	*written += n
	if err != nil {
		return false, stallOr(&stalled, err)
	}
	t.p.Bytes = *written
	t.progress(true)
	return true, nil
}

// restart скидає .part у нуль: і буфер, і довжину, і позицію, і прогрес.
func (t *transfer) restart(written *int64) error {
	t.bw.Reset(t.part)
	if err := t.part.Truncate(0); err != nil {
		return diskError(t.part.Name(), err)
	}
	if _, err := t.part.Seek(0, io.SeekStart); err != nil {
		return diskError(t.part.Name(), err)
	}
	*written, t.p.Bytes = 0, 0
	return nil
}

// pump переливає src у write, перезаводячи watchdog на кожній порції байтів.
// limit > 0 обмежує обсяг; перевищення — errTooBig. io.Copy тут не годиться
// саме через watchdog: тайм-аут має бути на паузу між байтами, а не на весь
// файл.
func pump(write func([]byte) error, src io.Reader, timer *time.Timer, limit int64) (int64, error) {
	buf := make([]byte, 128<<10)
	var n int64
	for {
		m, err := src.Read(buf)
		if m > 0 {
			timer.Reset(idleTimeout)
			n += int64(m)
			if limit > 0 && n > limit {
				return n, fmt.Errorf("перевищено %d байт: %w", limit, errTooBig)
			}
			if werr := write(buf[:m]); werr != nil {
				return n, werr
			}
		}
		if errors.Is(err, io.EOF) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
	}
}

// stallOr перекладає скасування за watchdog у транспортну помилку: для
// викликача це обрив, який варто повторити, а не рішення користувача.
func stallOr(stalled *atomic.Bool, err error) error {
	if stalled.Load() {
		return fmt.Errorf("%w: %w", errStalled, err)
	}
	return err
}

// expired — підписане посилання протухло. 403/404 на сегменті означають саме
// це: сам плейлист щойно читався успішно.
func expired(err error) bool {
	switch httpx.Status(err) {
	case http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return errors.Is(err, errs.ErrStreamExpired)
}

// retryable — чи має сенс повторювати. Повторюємо обриви, зависання, 5xx і
// 429; не повторюємо рішення (скасування), відмови диска й вироки сервера.
func retryable(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, errStalled):
		return true
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, errTooBig),
		errors.Is(err, errs.ErrStreamExpired),
		errors.Is(err, errs.ErrDiskFull),
		errors.Is(err, errs.ErrNoWriteAccess),
		errors.Is(err, errs.ErrEncryptedStream),
		errors.Is(err, errs.ErrUnsupportedStream):
		return false
	}
	if code := httpx.Status(err); code != 0 {
		return code == http.StatusTooManyRequests || code >= 500
	}
	return true
}

// backoff — 300 мс × 2ⁿ плюс джитер до половини паузи. Джитер потрібен, бо
// чотири воркери падають на одному 503 одночасно і без розсіювання били б у
// сервер синхронно.
func backoff(ctx context.Context, attempt int) error {
	d := backoffBase << (attempt - 1)
	if half := int64(d / 2); half > 0 {
		d += time.Duration(rand.Int64N(half))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// diskError зводить відмови ФС до сентинелів, за якими i18n дає людині різні
// поради. ENOSPC і EROFS свого відповідника в os немає — звідси syscall.
func diskError(path string, err error) error {
	switch {
	case errors.Is(err, syscall.ENOSPC):
		return fmt.Errorf("%s: %w", path, errs.ErrDiskFull)
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EROFS), errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s: %w", path, errs.ErrNoWriteAccess)
	}
	return fmt.Errorf("%s: %w", path, err)
}
