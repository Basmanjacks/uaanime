// Package downloadtest — стенд для тестів завантажувача: підставний транспорт
// і два httptest-сервери (HLS та прямий файл), що поводяться як живі відеохости.
//
// Живе окремим пакетом, як playertest і extractortest: стендом користуються
// тести кількох пакетів (httpx, download, cmd), а дублювати ці сервери в
// кожному — гарантовано мати три різні уявлення про те, як поводиться CDN.
// Імпортує лише стандартну бібліотеку — жодного download чи httpx, щоб пакет
// не міг створити цикл імпортів з тими, хто його тестує.
package downloadtest

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// RequiredHeaders — заголовки, без яких moonanime віддає 400 і на плейлисті, і
// на сегменті, і на CDN після редиректу. Стенд вимагає їх так само, інакше
// тести не ловили б найчастішу поломку: загублені заголовки на другому хості.
var RequiredHeaders = []string{"Referer", "User-Agent", "Accept", "Accept-Language"}

// proxylessTransport — реальний транспорт стенда. Proxy: nil, бо проксі з
// оточення CI перехопило б навіть 127.0.0.1.
var proxylessTransport = &http.Transport{Proxy: nil}

// Transport підставляє фейкові імена хостів («hls.test») на адресу httptest-
// сервера. Потрібен саме він: httptest слухає 127.0.0.1, а перевірки URL у
// завантажувачі (https, ім'я хоста, не IP-літерал) такий URL відкидають — тож
// тест ходить за https://hls.test/…, а сюди приходить http://127.0.0.1:port.
//
// rewrites: фейковий хост → base URL сервера (srv.URL).
func Transport(rewrites map[string]string) http.RoundTripper {
	hosts := make(map[string]*url.URL, len(rewrites))
	for name, base := range rewrites {
		u, err := url.Parse(base)
		if err != nil {
			panic("downloadtest: некоректний base URL " + base)
		}
		hosts[name] = u
	}
	return rewriteTransport{hosts: hosts}
}

type rewriteTransport struct{ hosts map[string]*url.URL }

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, ok := t.hosts[req.URL.Hostname()]
	if !ok {
		return nil, fmt.Errorf("downloadtest: хост %s не підставлено", req.URL.Host)
	}
	clone := req.Clone(req.Context())
	clone.URL.Scheme = target.Scheme
	clone.URL.Host = target.Host
	// Сервер має бачити фейкове ім'я — так само, як бачив би справжній CDN.
	clone.Host = req.URL.Host
	res, err := proxylessTransport.RoundTrip(clone)
	if res != nil {
		// Повертаємо клієнту оригінальний запит, щоб res.Request.URL (а з ним
		// FinalURL у тестах) лишався фейковим https-URL, а не 127.0.0.1.
		res.Request = req
	}
	return res, err
}

func headersOK(w http.ResponseWriter, r *http.Request, referer string) bool {
	for _, h := range RequiredHeaders {
		if r.Header.Get(h) == "" {
			http.Error(w, "missing "+h, http.StatusBadRequest)
			return false
		}
	}
	// Значення Referer, а не лише його наявність: Go на редиректі підставляє
	// свій Referer (URL попереднього хопа), тож стенд, що перевіряє лише
	// наявність, пропустив би загублені заголовки потоку.
	if referer != "" && r.Header.Get("Referer") != referer {
		http.Error(w, "bad Referer "+r.Header.Get("Referer"), http.StatusBadRequest)
		return false
	}
	return true
}

// ---------------------------------------------------------------- HLS стенд

// SegmentSize — розмір одного сегмента стенда. Маленький навмисно: тести
// звіряють байти, а не пропускну здатність.
const SegmentSize = 4096

// SegmentDuration — тривалість сегмента в EXTINF, як у живих плейлистах.
const SegmentDuration = 5.0

// HLSOptions — налаштування стенда. Нульове значення дає робочий стенд:
// хост hls.test, якості 1080/720/480, по 3 сегменти.
type HLSOptions struct {
	Host     string
	Heights  []int
	Segments int
	// Referer — якщо задано, сервер вимагає саме це значення заголовка.
	Referer string
}

func (o HLSOptions) withDefaults() HLSOptions {
	if o.Host == "" {
		o.Host = "hls.test"
	}
	if len(o.Heights) == 0 {
		o.Heights = []int{1080, 720, 480}
	}
	if o.Segments == 0 {
		o.Segments = 3
	}
	return o
}

type segmentRule struct {
	failStatus int
	failTimes  int
	slow       time.Duration
	truncate   bool
	stall      bool
	block      <-chan struct{}
}

// HLSServer — master + медіаплейлисти + сегменти з ручками поломок.
type HLSServer struct {
	srv  *httptest.Server
	opts HLSOptions
	done chan struct{}

	mu       sync.Mutex
	rules    map[int]*segmentRule
	requests int
	paths    []string
}

// NewHLS піднімає HLS-стенд і закриває його на виході з тесту.
func NewHLS(t testing.TB, opts HLSOptions) *HLSServer {
	t.Helper()
	s := &HLSServer{opts: opts.withDefaults(), done: make(chan struct{}), rules: map[int]*segmentRule{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(func() {
		// Спершу відпускаємо застряглі сегменти: httptest.Close чекає на
		// обробники, і без цього тест із StallSegment завис би на виході.
		close(s.done)
		s.srv.Close()
	})
	return s
}

// URL — адреса master-плейлиста на фейковому хості.
func (s *HLSServer) URL() string { return "https://" + s.opts.Host + "/master.m3u8" }

// Rewrites — таблиця для Transport.
func (s *HLSServer) Rewrites() map[string]string {
	return map[string]string{s.opts.Host: s.srv.URL}
}

// RequestCount — скільки запитів стенд обслужив (разом з невдалими).
func (s *HLSServer) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// Paths — шляхи всіх запитів у порядку надходження.
func (s *HLSServer) Paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

func (s *HLSServer) rule(i int) *segmentRule {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rules[i]
	if !ok {
		r = &segmentRule{}
		s.rules[i] = r
	}
	return r
}

// FailSegment змушує сегмент i віддати status перші times разів.
func (s *HLSServer) FailSegment(i, times, status int) {
	r := s.rule(i)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.failTimes, r.failStatus = times, status
}

// SlowSegment затримує відповідь на сегмент i.
func (s *HLSServer) SlowSegment(i int, d time.Duration) {
	r := s.rule(i)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.slow = d
}

// TruncateSegment віддає половину сегмента i при заявленому повному
// Content-Length — клієнт має побачити обрив, а не коротший сегмент.
func (s *HLSServer) TruncateSegment(i int) {
	r := s.rule(i)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.truncate = true
}

// BlockSegment тримає сегмент i, доки не закриють release.
func (s *HLSServer) BlockSegment(i int, release <-chan struct{}) {
	r := s.rule(i)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.block = release
}

// StallSegment віддає заголовки і не пише тіла ніколи — так виглядає зависла
// TCP-сесія, яку рятує лише тайм-аут клієнта.
func (s *HLSServer) StallSegment(i int) {
	r := s.rule(i)
	s.mu.Lock()
	defer s.mu.Unlock()
	r.stall = true
}

// Segment — детерміновані байти сегмента i.
func Segment(i int) []byte { return bytes.Repeat([]byte{byte(i)}, SegmentSize) }

// Expected — очікуваний результат склейки якості height. Байти сегмента
// залежать лише від номера, тож для всіх якостей вони однакові: стенд перевіряє
// цілісність склейки, а не те, яку якість обрали — для цього є Paths().
func (s *HLSServer) Expected(height int) []byte {
	out := make([]byte, 0, s.opts.Segments*SegmentSize)
	for i := range s.opts.Segments {
		out = append(out, Segment(i)...)
	}
	return out
}

func (s *HLSServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	s.paths = append(s.paths, r.URL.Path)
	s.mu.Unlock()
	if !headersOK(w, r, s.opts.Referer) {
		return
	}
	switch {
	case r.URL.Path == "/master.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(s.master()))
	case strings.HasSuffix(r.URL.Path, "/index.m3u8"):
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(s.media()))
	case strings.Contains(r.URL.Path, "/segment"):
		s.serveSegment(w, r)
	default:
		http.NotFound(w, r)
	}
}

// master — порядок атрибутів навмисно різний між варіантами, один варіант із
// CODECS, один URL абсолютний, один відносний, один від кореня: саме так
// виглядають ashdi, tortuga і moonanime, і парсер мусить витримати всі три.
func (s *HLSServer) master() string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i, h := range s.opts.Heights {
		bw := 1400000 + i*1700000
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.64001f,mp4a.40.2\"\n", bw, h*16/9, h)
			fmt.Fprintf(&b, "https://%s/q/%d/index.m3u8\n", s.opts.Host, h)
		case 1:
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:RESOLUTION=%dx%d,BANDWIDTH=%d\n", h*16/9, h, bw)
			fmt.Fprintf(&b, "q/%d/index.m3u8\n", h)
		default:
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\n", bw, h*16/9, h)
			fmt.Fprintf(&b, "/q/%d/index.m3u8\n", h)
		}
	}
	return b.String()
}

func (s *HLSServer) media() string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:5\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	for i := range s.opts.Segments {
		fmt.Fprintf(&b, "#EXTINF:%.1f,\nsegment%d.ts\n", SegmentDuration, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

func (s *HLSServer) serveSegment(w http.ResponseWriter, r *http.Request) {
	i := segmentIndex(r.URL.Path)
	if i < 0 || i >= s.opts.Segments {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	rule := s.rules[i]
	var cur segmentRule
	if rule != nil {
		cur = *rule
		if rule.failTimes > 0 {
			rule.failTimes--
		}
	}
	s.mu.Unlock()

	if cur.failTimes > 0 {
		http.Error(w, "fail", cur.failStatus)
		return
	}
	if cur.block != nil {
		select {
		case <-cur.block:
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		}
	}
	if cur.slow > 0 {
		select {
		case <-time.After(cur.slow):
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		}
	}
	body := Segment(i)
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if cur.stall {
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		select {
		case <-s.done:
		case <-r.Context().Done():
		}
		return
	}
	if cur.truncate {
		_, _ = w.Write(body[:len(body)/2])
		return
	}
	_, _ = w.Write(body)
}

func segmentIndex(path string) int {
	_, after, ok := strings.Cut(path, "segment")
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSuffix(after, ".ts"))
	if err != nil {
		return -1
	}
	return n
}

// ------------------------------------------------------------- файловий стенд

// FileOptions — налаштування файлового стенда. Нульове значення: хости
// v.test → cdn.test, Range підтримується, сильний ETag є.
type FileOptions struct {
	Host    string // перший сервер, що робить 302
	CDNHost string // другий сервер, що віддає файл
	// IgnoreRange — сервер відповідає 200 і повним тілом навіть на Range.
	IgnoreRange bool
	// NoValidator — жодного ETag/Last-Modified: докачку нічим підтвердити.
	NoValidator bool
	// Referer — якщо задано, обидва сервери вимагають саме це значення.
	// Так перевіряється, що заголовки потоку переграно і на другому хості.
	Referer string
}

func (o FileOptions) withDefaults() FileOptions {
	if o.Host == "" {
		o.Host = "v.test"
	}
	if o.CDNHost == "" {
		o.CDNHost = "cdn.test"
	}
	return o
}

// FileServer — два сервери на різних хостах: перший робить 302 на другий, як
// s.moonanime.art → s1.mooncdn.online. Саме на цьому переході губляться
// заголовки, тому обидва вимагають RequiredHeaders.
type FileServer struct {
	origin *httptest.Server
	cdn    *httptest.Server
	opts   FileOptions

	mu       sync.Mutex
	payload  []byte
	etag     string
	cutAfter int
	requests int
}

// NewFile піднімає файловий стенд і закриває його на виході з тесту.
func NewFile(t testing.TB, payload []byte, opts FileOptions) *FileServer {
	t.Helper()
	s := &FileServer{opts: opts.withDefaults(), payload: payload, etag: etagOf(payload)}
	s.origin = httptest.NewServer(http.HandlerFunc(s.handleOrigin))
	s.cdn = httptest.NewServer(http.HandlerFunc(s.handleCDN))
	t.Cleanup(func() {
		s.origin.Close()
		s.cdn.Close()
	})
	return s
}

// URL — адреса, з якої починається завантаження (та, що робить 302).
func (s *FileServer) URL() string { return "https://" + s.opts.Host + "/v/1080/" }

// FileURL — адреса файла на CDN, тобто очікуваний FinalURL після редиректу.
func (s *FileServer) FileURL() string { return "https://" + s.opts.CDNHost + "/file.webm" }

// Rewrites — таблиця для Transport (обидва хости).
func (s *FileServer) Rewrites() map[string]string {
	return map[string]string{s.opts.Host: s.origin.URL, s.opts.CDNHost: s.cdn.URL}
}

// RequestCount — скільки запитів обслужив CDN.
func (s *FileServer) RequestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// ETag — поточний сильний валідатор файла.
func (s *FileServer) ETag() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.etag
}

// CutAfter обриває наступну відповідь після n байтів (один раз).
func (s *FileServer) CutAfter(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutAfter = n
}

// Replace підміняє вміст файла (і валідатор) — так виглядає перезалитий
// на CDN файл посеред докачки.
func (s *FileServer) Replace(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payload, s.etag = payload, etagOf(payload)
}

func etagOf(payload []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(payload)
	return fmt.Sprintf("\"%d-%x\"", len(payload), h.Sum64())
}

func (s *FileServer) handleOrigin(w http.ResponseWriter, r *http.Request) {
	if !headersOK(w, r, s.opts.Referer) {
		return
	}
	// Абсолютний Location на інший хост: відносний не перевіряв би крос-хостову
	// політику редиректів.
	http.Redirect(w, r, s.FileURL(), http.StatusFound)
}

func (s *FileServer) handleCDN(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests++
	payload, etag, cut := s.payload, s.etag, s.cutAfter
	s.cutAfter = 0
	s.mu.Unlock()

	if !headersOK(w, r, s.opts.Referer) {
		return
	}
	h := w.Header()
	h.Set("Content-Type", "video/webm")
	h.Set("Accept-Ranges", "bytes")
	if !s.opts.NoValidator {
		h.Set("ETag", etag)
	}

	start := int64(-1)
	if rng := r.Header.Get("Range"); rng != "" && !s.opts.IgnoreRange {
		start = parseRangeStart(rng)
	}
	// If-Range з чужим валідатором → віддаємо весь файл, як вимагає RFC 9110.
	if ir := r.Header.Get("If-Range"); ir != "" && ir != etag {
		start = -1
	}

	if start < 0 {
		h.Set("Content-Length", strconv.Itoa(len(payload)))
		w.WriteHeader(http.StatusOK)
		writeBody(w, payload, cut)
		return
	}
	if start >= int64(len(payload)) {
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", len(payload)))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	part := payload[start:]
	h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(payload)-1, len(payload)))
	h.Set("Content-Length", strconv.Itoa(len(part)))
	w.WriteHeader(http.StatusPartialContent)
	writeBody(w, part, cut)
}

// writeBody пише тіло, за потреби обриваючи його на cut байтах: заявлений
// Content-Length лишається повним, тож клієнт бачить саме обрив з'єднання.
func writeBody(w http.ResponseWriter, body []byte, cut int) {
	if cut > 0 && cut < len(body) {
		_, _ = w.Write(body[:cut])
		return
	}
	_, _ = w.Write(body)
}

func parseRangeStart(v string) int64 {
	spec, ok := strings.CutPrefix(strings.TrimSpace(v), "bytes=")
	if !ok {
		return -1
	}
	startStr, _, ok := strings.Cut(spec, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(startStr), 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}
