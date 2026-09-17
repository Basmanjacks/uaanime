package download

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

// planWorkers — скільки медіаплейлистів зондуємо одночасно. Чотири: варіантів
// у живих майстрах 3–5, тож більше не прискорить, а менше змусило б людину
// чекати на екран якості зайву секунду.
const planWorkers = 4

// Kind — що саме доведеться качати.
type Kind int

const (
	KindHLS  Kind = iota // склейка сегментів у один .ts
	KindFile             // готовий файл одним тілом
)

func (k Kind) String() string {
	if k == KindHLS {
		return "hls"
	}
	return "file"
}

// Quality — один варіант, який людина може вибрати на екрані якості.
//
// Bytes для HLS — оцінка (Exact == false): точний розмір знав би лише той, хто
// вже завантажив усі сегменти. Для прямого файла сервер називає розмір сам.
type Quality struct {
	Height      int
	Bandwidth   int
	URL         string
	Headers     map[string]string
	Segments    int
	DurationSec float64
	Bytes       int64
	Exact       bool

	// media — сегменти, розібрані ще на етапі плану. Тримаємо їх тут, щоб job
	// не ходив за тим самим плейлистом удруге: між планом і стартом минають
	// секунди, а зайвий запит до CDN — це ще одна точка відмови. Поле
	// неекспортоване: назовні Quality лишається значенням для UI.
	media []Segment
}

// Plan — усе, що можна завантажити для обраного релізу серії.
type Plan struct {
	Kind Kind
	Ext  string
	// Qualities — за спаданням висоти; невідома висота (0) завжди остання.
	Qualities []Quality
}

// NewFetcher збирає Fetcher завантажувача. Єдина точка, де на httpx.Fetcher
// вішається перевірка URL рівня екстрактора: httpx не може викликати її сам
// (extractor імпортує httpx — вийшов би цикл), тож якби кожен викликач
// підставляв Validate власноруч, рано чи пізно хтось забув би.
func NewFetcher(rt http.RoundTripper) *httpx.Fetcher {
	f := httpx.NewFetcher(rt)
	f.Validate = validateStreamURL
	return f
}

func validateStreamURL(raw string) error {
	if !extractor.ValidStreamURL(raw) {
		return fmt.Errorf("URL %q не пройшов перевірку потоку: %w", raw, errs.ErrProvider)
	}
	return nil
}

// BuildPlan з'ясовує, що саме лежить за потоками релізу, і складає список
// якостей. Потоки зондуються паралельно: moonanime віддає по Stream на якість,
// і послідовні чотири запити — це чотири RTT на екран, який людина бачить
// одразу після натискання клавіші.
func BuildPlan(ctx context.Context, f *httpx.Fetcher, streams []extractor.Stream) (*Plan, error) {
	valid := make([]extractor.Stream, 0, len(streams))
	for _, s := range streams {
		// Перевірка тут, а не лише у Fetcher: потік, який не пройшов би її,
		// не має навіть займати місце в таблиці результатів.
		if extractor.ValidStreamURL(s.URL) {
			valid = append(valid, s)
		}
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("жодного придатного URL потоку: %w", errs.ErrNoStream)
	}

	probed := make([]*httpx.Probed, len(valid))
	probeErr := make([]error, len(valid))
	forEach(len(valid), planWorkers, func(i int) {
		probed[i], probeErr[i] = f.Probe(ctx, valid[i].URL, valid[i].Headers)
	})

	// Перший плейлист виграє змішаний набір: склеєний .ts дає всі якості
	// майстра, тоді як прямий файл — лише свою.
	for i := range valid {
		if probeErr[i] == nil && probed[i].Kind == httpx.ProbePlaylist {
			return hlsPlan(ctx, f, valid[i], probed[i])
		}
	}
	return filePlan(valid, probed, probeErr)
}

// Pick обирає якість під побажання людини. height == 0 — «найкраща».
// Точного збігу немає → беремо найближчу НИЖЧУ: 720p замість проханих 1080p
// чесніше, ніж мовчки віддати вдвічі більший файл. Нижчої немає — беремо
// найближчу вищу, бо відмовити людині через відсутність рівно 480p було б
// гірше за файл, який вона все одно зможе подивитися.
func (p *Plan) Pick(height int) (Quality, bool) {
	if p == nil || len(p.Qualities) == 0 {
		return Quality{}, false
	}
	if height <= 0 {
		return p.Qualities[0], true
	}
	var lower, higher *Quality
	for i := range p.Qualities {
		q := &p.Qualities[i]
		switch {
		case q.Height == height:
			return *q, true
		case q.Height <= 0:
			continue
		case q.Height < height:
			if lower == nil || q.Height > lower.Height {
				lower = q
			}
		default:
			if higher == nil || q.Height < higher.Height {
				higher = q
			}
		}
	}
	switch {
	case lower != nil:
		return *lower, true
	case higher != nil:
		return *higher, true
	}
	// Лишилися самі якості з невідомою висотою («авто» одного медіаплейлиста):
	// вона задовольняє будь-яке прохання, бо іншої просто не існує.
	return p.Qualities[0], true
}

// Heights — висоти плану для повідомлення «є лише такі якості».
func (p *Plan) Heights() []int {
	if p == nil {
		return nil
	}
	out := make([]int, 0, len(p.Qualities))
	for _, q := range p.Qualities {
		out = append(out, q.Height)
	}
	return out
}

func hlsPlan(ctx context.Context, f *httpx.Fetcher, s extractor.Stream, pr *httpx.Probed) (*Plan, error) {
	base, err := url.Parse(pr.FinalURL)
	if err != nil {
		return nil, fmt.Errorf("URL плейлиста %q: %w", pr.FinalURL, errs.ErrProvider)
	}
	// Розширення для HLS завжди .ts: сегменти MPEG-TS склеюються конкатенацією,
	// і жодного ремуксу без ffmpeg ми не робимо.
	p := &Plan{Kind: KindHLS, Ext: ".ts"}

	if !IsMaster(pr.Body) {
		m, err := ParseMedia(base, pr.Body)
		if err != nil {
			return nil, err
		}
		p.Qualities = []Quality{{
			Height:      s.Quality,
			URL:         pr.FinalURL,
			Headers:     s.Headers,
			Segments:    len(m.Segments),
			DurationSec: m.TotalSec,
			media:       m.Segments,
		}}
		return p, nil
	}

	variants, err := ParseMaster(base, pr.Body)
	if err != nil {
		return nil, err
	}
	out := make([]Quality, len(variants))
	ok := make([]bool, len(variants))
	errsOut := make([]error, len(variants))
	forEach(len(variants), planWorkers, func(i int) {
		q, err := variantQuality(ctx, f, s, variants[i])
		if err != nil {
			errsOut[i] = err
			return
		}
		out[i], ok[i] = q, true
	})
	for i := range variants {
		if ok[i] {
			p.Qualities = append(p.Qualities, out[i])
		}
	}
	if len(p.Qualities) == 0 {
		// Усі варіанти відпали — віддаємо першу справжню причину, а не
		// узагальнене «нічого не вийшло»: серед них буває ErrEncryptedStream,
		// і людині треба показати саме його.
		return nil, fmt.Errorf("жоден варіант майстер-плейлиста не прочитано: %w", firstErr(errsOut))
	}
	sortQualities(p.Qualities)
	return p, nil
}

// variantQuality читає медіаплейлист одного варіанта. Варіант, який не
// прочитався, відкидається викликачем: краще дати людині 720p, ніж нічого.
func variantQuality(ctx context.Context, f *httpx.Fetcher, s extractor.Stream, v Variant) (Quality, error) {
	pr, err := f.Probe(ctx, v.URL, s.Headers)
	if err != nil {
		return Quality{}, err
	}
	if pr.Kind != httpx.ProbePlaylist {
		return Quality{}, fmt.Errorf("варіант %s не плейлист: %w", v.URL, errs.ErrProvider)
	}
	base, err := url.Parse(pr.FinalURL)
	if err != nil {
		return Quality{}, fmt.Errorf("URL варіанта %q: %w", pr.FinalURL, errs.ErrProvider)
	}
	m, err := ParseMedia(base, pr.Body)
	if err != nil {
		return Quality{}, err
	}
	q := Quality{
		Height:      v.Height,
		Bandwidth:   v.Bandwidth,
		URL:         pr.FinalURL,
		Headers:     s.Headers,
		Segments:    len(m.Segments),
		DurationSec: m.TotalSec,
		media:       m.Segments,
	}
	// Оцінка розміру: бітрейт × тривалість. BANDWIDTH у майстрі — піковий, тож
	// оцінка радше завищена, і це правильний бік помилки для перевірки місця.
	if v.Bandwidth > 0 && m.TotalSec > 0 {
		q.Bytes = int64(float64(v.Bandwidth) / 8 * m.TotalSec)
	}
	return q, nil
}

func filePlan(streams []extractor.Stream, probed []*httpx.Probed, probeErr []error) (*Plan, error) {
	p := &Plan{Kind: KindFile}
	for i, s := range streams {
		if probeErr[i] != nil {
			continue
		}
		pr := probed[i]
		q := Quality{
			Height:  s.Quality,
			URL:     s.URL,
			Headers: s.Headers,
			Bytes:   pr.Total,
			// Exact — «розмір названий сервером, а не вгаданий». Bytes == 0
			// при цьому означає «сервер розміру не сказав», і UI покаже
			// «розмір невідомий», а не «~0».
			Exact: true,
		}
		if q.Height <= 0 {
			q.Height = heightFromURL(s.URL)
		}
		if q.Height <= 0 {
			q.Height = heightFromURL(pr.FinalURL)
		}
		if p.Ext == "" {
			p.Ext = fileExt(pr.FinalURL)
		}
		p.Qualities = append(p.Qualities, q)
	}
	if len(p.Qualities) == 0 {
		return nil, fmt.Errorf("жоден потік не зондувався: %w", firstErr(probeErr))
	}
	sortQualities(p.Qualities)
	return p, nil
}

// fileExt — розширення для збереженого прямого файла. Свідомо звужене до
// .webm і .mp4: name.go вміє розібрати назад лише ts/webm/mp4, тож зберегти
// .mkv під його справжнім іменем ми не можемо, а перейменувати його на .ts
// означало б збрехати плеєру про контейнер. Невідоме розширення → .mp4:
// це найчастіший контейнер прямих посилань і найбезпечніша здогадка.
func fileExt(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ".mp4"
	}
	path := u.Path
	i := strings.LastIndexByte(path, '.')
	if i < 0 || strings.IndexByte(path[i:], '/') >= 0 {
		return ".mp4"
	}
	switch strings.ToLower(path[i:]) {
	case ".webm":
		return ".webm"
	case ".mp4":
		return ".mp4"
	}
	return ".mp4"
}

// heightFromURL шукає висоту в шляху: «…/content/v/12345/1080/?expires=…».
// Ходимо по сегментах шляху, а не регулярним виразом по всьому URL, щоб не
// прийняти за якість шматок ідентифікатора. Остання підхожа мітка виграє —
// вона ближча до файла, тобто конкретніша.
func heightFromURL(raw string) int {
	u, err := url.Parse(raw)
	if err != nil {
		return 0
	}
	out := 0
	for _, seg := range strings.Split(u.Path, "/") {
		if len(seg) < 3 || len(seg) > 4 {
			continue
		}
		n, err := strconv.Atoi(seg)
		if err != nil || n < 100 || n > 4320 {
			continue
		}
		out = n
	}
	return out
}

// sortQualities: вища якість перша, невідома (0) — остання. За однакової
// висоти першим іде товщий бітрейт: це той самий кадр, але менше артефактів.
func sortQualities(qs []Quality) {
	sort.SliceStable(qs, func(i, j int) bool {
		a, b := qs[i], qs[j]
		switch {
		case (a.Height <= 0) != (b.Height <= 0):
			return b.Height <= 0
		case a.Height != b.Height:
			return a.Height > b.Height
		}
		return a.Bandwidth > b.Bandwidth
	})
}

func firstErr(list []error) error {
	for _, err := range list {
		if err != nil {
			return err
		}
	}
	return errs.ErrProvider
}

// forEach виконує fn(0..n-1) не більш ніж workers горутинами одночасно.
// Помилки збирає сам fn у свій зріз за індексом — так результат не залежить
// від порядку завершення.
func forEach(n, workers int, fn func(i int)) {
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}
