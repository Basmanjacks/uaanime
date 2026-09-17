package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// downloadResult — єдиний об'єкт, який команда друкує під --json. Ті самі
// чотири поля, що показує людині фінальний рядок, тільки без форматування.
type downloadResult struct {
	Path    string  `json:"path"`
	Bytes   int64   `json:"bytes"`
	Height  int     `json:"height"`
	Seconds float64 `json:"seconds"`
}

// planTimeout — скільки чекаємо на резолюцію з планом при перевірці --quality.
// Стільки ж, скільки решта команд дає провайдеру (main.go:260).
const planTimeout = 60 * time.Second

// nameTimeout — скільки чекаємо на назву тайтлу. Менше за решту запитів:
// без назви завантаження все одно піде, лише папка буде зі слагом.
const nameTimeout = 20 * time.Second

// cmdDownload зберігає серію на диск. Власний контекст із сигналами, а не
// 60-секундний ctx команд: гігабайтна серія качається довше за будь-який
// розумний тайм-аут, і єдина причина припинити — Ctrl+C.
func (a *app) cmdDownload(id string, ep int, opt options) int {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ref, ok := a.refFromID(id)
	if !ok {
		return 2
	}
	dir, ok := downloadTargetDir(opt.dir, a.cfg.DownloadDir)
	if !ok {
		errln(i18n.MsgUsage)
		return 2
	}
	// Назва тайтлу здобувається ДО підказок: з неї завантажувач збирає ім'я
	// папки, ім'я файла, sidecar і marker. Окремий короткий тайм-аут — качати
	// можна й годину, а от чекати на назву стільки ж безглуздо.
	nameCtx, cancelName := context.WithTimeout(sigCtx, nameTimeout)
	ref = a.namedRef(nameCtx, ref)
	cancelName()

	eng := a.engineWithoutPlayer()
	hints := eng.ResolveHints(ref, ep)
	// NoLocal: завантажувач іде по потоки навіть тоді, коли серія вже лежить
	// на диску — інакше після збереженого 720p не можна було б узяти 1080p.
	hints.NoLocal = true
	resolve := func(ctx context.Context) (download.Target, error) {
		res, err := eng.ResolveWith(ctx, ref, ep, hints, nil)
		if err != nil {
			return download.Target{}, err
		}
		return download.Target{
			Streams: res.Streams,
			Studio:  res.Source.Studio,
			Kind:    res.Source.Kind,
			Name:    res.Name,
		}, nil
	}

	f := download.NewFetcher(a.rt)
	if opt.quality > 0 {
		if code, ok := a.checkQuality(sigCtx, f, resolve, opt.quality); !ok {
			return code
		}
	}

	mgr := download.NewManager(f)
	defer mgr.Close()
	jobID, err := mgr.Enqueue(download.Job{
		Ref:     ref,
		Episode: ep,
		Height:  opt.quality,
		Dir:     dir,
		Resolve: resolve,
	})
	if err != nil {
		a.printCommandError(err)
		return 1
	}
	return a.watchDownload(sigCtx, mgr, jobID, opt.json)
}

// checkQuality відмовляє ще до черги, якщо просять якість, якої в релізі
// немає: інакше Plan.Pick мовчки віддав би найближчу, і людина отримала б
// 1080p замість проханих 4320p, навіть не дізнавшись про підміну.
// Резолюція тут не змарнована: job стартує з того самого мемо джерел.
func (a *app) checkQuality(ctx context.Context, f *httpx.Fetcher, resolve download.ResolveFunc, height int) (int, bool) {
	planCtx, cancel := context.WithTimeout(ctx, planTimeout)
	defer cancel()
	target, err := resolve(planCtx)
	if err != nil {
		a.printCommandError(err)
		return 1, false
	}
	plan, err := download.BuildPlan(planCtx, f, target.Streams)
	if err != nil {
		a.printCommandError(err)
		return 1, false
	}
	heights := plan.Heights()
	if slices.Contains(heights, height) {
		return 0, true
	}
	labels := make([]string, 0, len(heights))
	for _, h := range heights {
		labels = append(labels, i18n.QualityLabel(h))
	}
	errf(i18n.MsgDownloadQualityMissing+"\n", height, strings.Join(labels, ", "))
	return 1, false
}

// watchDownload веде завдання до термінального стану. Стан читається зі
// Snapshot, а канал подій лише будить: так загублений сигнал (ємність 1)
// нічого не ламає, а тикер однаково перемальовує рядок прогресу.
func (a *app) watchDownload(ctx context.Context, mgr *download.Manager, jobID int64, jsonOut bool) int {
	pr := newProgressPrinter(jsonOut)
	events := mgr.Events()
	tick := time.NewTicker(redrawEvery)
	defer tick.Stop()
	started := time.Now()

	for {
		p, ok := jobProgress(mgr, jobID)
		switch {
		case !ok:
			// Завдання зникло зі знімка — такого не буває без Forget, якого
			// в CLI немає, але мовчки крутитися вічно гірше за чесний вихід.
			pr.clear()
			errln(i18n.MsgDownloadCancelled)
			return 1
		case p.State == download.StateDone:
			pr.clear()
			if jsonOut {
				return printJSON(downloadResult{
					Path:    p.Path,
					Bytes:   p.Bytes,
					Height:  p.Height,
					Seconds: roundSeconds(time.Since(started)),
				})
			}
			outf(i18n.MsgDownloadDone+"\n", p.Path)
			return 0
		case p.State == download.StateCancelled:
			pr.clear()
			errln(i18n.MsgDownloadCancelled)
			return 1
		case p.State == download.StateFailed:
			pr.clear()
			a.printCommandError(p.Err)
			return 1
		}
		pr.print(p)

		select {
		case <-events:
		case <-tick.C:
		case <-ctx.Done():
			// Close скасовує активне завдання і ЧЕКАЄ, доки воно прибере .part:
			// після Ctrl+C на диску не лишається ані огризка, ані плейсхолдера.
			pr.clear()
			mgr.Close()
			errln(i18n.MsgDownloadCancelled)
			return 1
		}
	}
}

func jobProgress(mgr *download.Manager, id int64) (download.Progress, bool) {
	for _, p := range mgr.Snapshot() {
		if p.ID == id {
			return p, true
		}
	}
	return download.Progress{}, false
}

func roundSeconds(d time.Duration) float64 {
	return math.Round(d.Seconds()*100) / 100
}

// downloadTargetDir обирає папку: --dir важливіший за налаштування. Шлях із
// командного рядка мусить бути абсолютним — відносний означав би, що та сама
// команда з іншого каталогу кладе гігабайти в інше місце.
func downloadTargetDir(flag, configured string) (string, bool) {
	if flag == "" {
		return configured, configured != ""
	}
	dir := filepath.Clean(store.ExpandHome(flag))
	if !filepath.IsAbs(dir) {
		return "", false
	}
	return dir, true
}

const (
	// redrawEvery — крок перемальовки рядка в терміналі (4 рази на секунду).
	redrawEvery = 250 * time.Millisecond
	// logEvery — крок того самого рядка, коли вивід іде в файл чи в пайп:
	// там кожен рядок лишається назавжди, тож частіше — це сміття в логах.
	logEvery = 5 * time.Second
)

// progressPrinter друкує прогрес одним рядком. У терміналі рядок
// переписується через \r; у пайпі — дописується новий раз на п'ять секунд,
// бо \r у файлі перетворює лог на кашу. Під --json мовчить зовсім: на stdout
// має бути рівно один об'єкт.
type progressPrinter struct {
	quiet bool
	tty   bool
	last  time.Time
	width int // довжина попереднього рядка, щоб затерти його хвіст
}

func newProgressPrinter(jsonOut bool) *progressPrinter {
	return &progressPrinter{quiet: jsonOut, tty: stdoutIsTTY()}
}

func (p *progressPrinter) print(pr download.Progress) {
	if p.quiet || pr.State != download.StateRunning {
		return
	}
	every := logEvery
	if p.tty {
		every = redrawEvery
	}
	if !p.last.IsZero() && time.Since(p.last) < every {
		return
	}
	p.last = time.Now()
	line := progressLine(pr)
	if !p.tty {
		outln(line)
		return
	}
	pad := ""
	if n := p.width - len([]rune(line)); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	p.width = len([]rune(line))
	outf("\r%s%s", line, pad)
}

// clear прибирає недописаний рядок прогресу, щоб фінальне повідомлення не
// приклеїлося до його хвоста.
func (p *progressPrinter) clear() {
	if p.quiet || !p.tty || p.width == 0 {
		return
	}
	outf("\r%s\r", strings.Repeat(" ", p.width))
	p.width = 0
}

func progressLine(p download.Progress) string {
	total := "?"
	percent := 0
	if p.Total > 0 {
		total = i18n.Bytes(p.Total)
		percent = int(float64(p.Bytes) / float64(p.Total) * 100)
		percent = min(percent, 100)
	}
	eta := "—"
	if p.ETA > 0 {
		if s := i18n.HumanDuration(p.ETA.Seconds()); s != "" {
			eta = s
		}
	}
	return fmt.Sprintf(i18n.MsgDownloadProgress,
		p.Episode, i18n.QualityLabel(p.Height), percent,
		i18n.Bytes(p.Bytes), total, i18n.Bytes(int64(p.BytesPerSec)), eta)
}

// stdoutIsTTY — чи пише команда просто в термінал. Перевірка йде по
// os.Stdout, тому вона правдива лише поки stdout не підмінено: тести
// збирають вивід у буфер, і \r-перемальовка в ньому була б нечитабельною.
func stdoutIsTTY() bool {
	if stdout != os.Stdout {
		return false
	}
	fi, _ := os.Stdout.Stat()
	return fi != nil && fi.Mode()&os.ModeCharDevice != 0
}
