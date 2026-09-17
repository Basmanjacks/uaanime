package httpx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// MaxFileBytes — стеля розміру одного файла (8 ГіБ). Серія в 1080p важить
// 0,3–3 ГБ; усе, що більше, — або не те посилання, або спроба заповнити диск.
const MaxFileBytes = 8 << 30

// probeWindow — скільки байтів Probe читає, щоб відрізнити плейлист від відео.
// 1 МіБ: найбільший реальний media-плейлист (312 сегментів) — близько 20 КБ,
// тож запас стократний, а для відеофайла це один Range-запит, який нікуди
// не збережеться.
const probeWindow = 1 << 20

// maxRedirects — стеля хопів. moonanime робить рівно один (302 на CDN);
// п'ять — це вже або цикл, або спроба провести нас кудись не туди.
const maxRedirects = 5

var playlistMagic = []byte("#EXTM3U")

// utf8BOM трапляється на m3u8, відданих з Windows-хостів.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// Fetcher — HTTP для завантажувача: великі тіла, Range-докачка і недовірені
// URL з плейлиста. Окремий від NewClient, бо вимоги протилежні: там 20 с на
// весь запит і заборона крос-хостових редиректів, тут — години на тіло і
// дозволений 302 на CDN.
type Fetcher struct {
	client  *http.Client
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)

	// Validate перевіряє кожен URL, за яким Fetcher піде (початковий і кожен
	// хоп редиректу). Типово — ValidateURL; завантажувач підставляє сюди
	// обгортку над extractor.ValidStreamURL. Полем, а не прямим викликом, бо
	// extractor імпортує httpx — прямий виклик дав би цикл імпортів.
	Validate func(string) error
}

// NewFetcher збирає Fetcher. rt != nil (тести, фікстури) береться як є — тоді
// запит не доходить до мережі й анти-SSRF dialer не задіяний (його перевіряють
// окремо, через шов resolve і PublicAddr).
func NewFetcher(rt http.RoundTripper) *Fetcher {
	f := &Fetcher{}
	if rt == nil {
		// Клон значень http.DefaultTransport, крім чотирьох свідомих відмін.
		rt = &http.Transport{
			// Proxy: nil — завантажувач не ходить через проксі з оточення.
			// Інакше цільове ім'я резолвив би проксі, і вся перевірка адрес
			// у dialContext стала б декорацією (анти-SSRF).
			Proxy:       nil,
			DialContext: f.dialContext,
			// 15 с на ЗАГОЛОВКИ відповіді. Тіло не обмежене нічим: файл на
			// 3 ГБ на повільному каналі качається годину і це норма.
			ResponseHeaderTimeout: 15 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			// Якості зондуються паралельно (до 4) на тому ж хості; дефолтні
			// 2 змушували б їх робити зайвий TLS-handshake.
			MaxIdleConnsPerHost:   8,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	f.client = &http.Client{
		Transport: rt,
		// Жодного Client.Timeout: він обмежує час ДО закриття тіла, тобто
		// обірвав би качання гігабайтного файла посеред процесу. Стелі тут —
		// ResponseHeaderTimeout і контекст викликача.
		CheckRedirect: f.checkRedirect,
		// Jar навмисно nil: на чужий origin ідуть лише задані заголовки.
	}
	return f
}

// ValidateURL — типова перевірка URL редиректу. Повторює правила
// extractor.ValidStreamURL (https, ім'я хоста не IP, ≥2 мітки, алфавітний TLD,
// не localhost), бо імпортувати extractor звідси не можна — цикл.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("httpx: URL %q не розібрано: %w", raw, errs.ErrProvider)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("httpx: схема %q не https: %w", u.Scheme, errs.ErrProvider)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || net.ParseIP(host) != nil {
		return fmt.Errorf("httpx: хост %q не є іменем: %w", host, errs.ErrProvider)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("httpx: хост %q локальний: %w", host, errs.ErrProvider)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("httpx: хост %q без домену: %w", host, errs.ErrProvider)
	}
	for _, l := range labels {
		if l == "" {
			return fmt.Errorf("httpx: хост %q має порожню мітку: %w", host, errs.ErrProvider)
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return fmt.Errorf("httpx: хост %q має закороткий TLD: %w", host, errs.ErrProvider)
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			return fmt.Errorf("httpx: хост %q має неалфавітний TLD: %w", host, errs.ErrProvider)
		}
	}
	return nil
}

func (f *Fetcher) validate(raw string) error {
	v := f.Validate
	if v == nil {
		v = ValidateURL
	}
	return v(raw)
}

// checkRedirect — власна політика замість тієї, що в NewClient: крос-хостовий
// 302 тут дозволений (moonanime віддає s.moonanime.art → s1.mooncdn.online),
// але кожен хоп мусить лишатися https і пройти Validate.
func (f *Fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("httpx: більше за %d редиректів: %w", maxRedirects, errs.ErrProvider)
	}
	if err := f.validate(req.URL.String()); err != nil {
		return fmt.Errorf("редирект на %s заборонено: %w", req.URL.Redacted(), err)
	}
	// Переграємо заголовки першого запиту дослівно. Go копіює несекретні
	// заголовки сам і з 1.23 не затирає явно заданий Referer, але ми на це не
	// покладаємось: відеохости вимагають Referer/UA/Accept/Accept-Language і на
	// CDN після редиректу (без них moonanime віддає 400), тож гарантія має бути
	// наша, а не чужої версії стандартної бібліотеки.
	for k, vs := range via[0].Header {
		req.Header[k] = append([]string(nil), vs...)
	}
	return nil
}

func (f *Fetcher) newRequest(ctx context.Context, rawURL string, headers map[string]string) (*http.Request, error) {
	if err := f.validate(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx: запит %s: %w: %w", rawURL, errs.ErrProvider, err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Ідентичність клієнта наскрізна (див. доккоментар пакета): якщо викликач
	// UA не дав, ставимо спільний, а не Go-дефолтний.
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	return req, nil
}

// StatusError — відповідь із кодом, якого ми не чекали. Код лишається в типі,
// а не лише в тексті: завантажувач мусить розрізняти їх діями, а не словами —
// 5xx і 429 варті ретраю, а 403/404 на підписаному посиланні означають, що воно
// протухло і повтор дасть рівно те саме. Unwrap веде до ErrProvider, тож усі
// наявні errors.Is(err, errs.ErrProvider) лишаються правдою.
type StatusError struct {
	URL  string
	Code int
}

func (e *StatusError) Error() string { return fmt.Sprintf("%s: HTTP %d", e.URL, e.Code) }

func (e *StatusError) Unwrap() error { return errs.ErrProvider }

// Status повертає код HTTP з помилки; 0 означає «помилка не про код відповіді»
// (обрив, DNS, тайм-аут).
func Status(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

func statusErr(u string, code int) error {
	return &StatusError{URL: u, Code: code}
}

// Validator — те, чим сервер підтверджує, що файл не змінився між запитами.
// ETag зберігаємо лише сильний: W/"..." дозволяє серверу віддати інші байти
// того ж «семантичного» ресурсу, а для докачки це означало б склеїти дві різні
// версії файла.
type Validator struct {
	ETag         string
	LastModified string
}

func (v Validator) IsZero() bool { return v.ETag == "" && v.LastModified == "" }

// ifRange — значення для заголовка If-Range; сильний ETag точніший за дату.
func (v Validator) ifRange() string {
	if v.ETag != "" {
		return v.ETag
	}
	return v.LastModified
}

func validatorOf(h http.Header) Validator {
	var v Validator
	if tag := strings.TrimSpace(h.Get("ETag")); tag != "" && !strings.HasPrefix(tag, "W/") {
		v.ETag = tag
	}
	v.LastModified = strings.TrimSpace(h.Get("Last-Modified"))
	return v
}

// ProbeKind — що саме лежить за URL потоку.
type ProbeKind int

const (
	ProbePlaylist ProbeKind = iota // m3u8 (master або media)
	ProbeFile                      // прямий відеофайл
)

func (k ProbeKind) String() string {
	if k == ProbePlaylist {
		return "playlist"
	}
	return "file"
}

// Probed — результат одного зондувального запиту.
type Probed struct {
	Kind ProbeKind
	// Body заповнене лише для ProbePlaylist — це повний текст плейлиста.
	// Для ProbeFile воно nil: тримати по мегабайту відео на кожну якість
	// немає навіщо, качати все одно з нуля.
	Body        []byte
	Total       int64 // точний розмір для ProbeFile; 0 = сервер не сказав
	FinalURL    string
	ContentType string
	Validator   Validator
}

// Probe одним запитом з'ясовує, плейлист це чи файл, і який файл завбільшки.
// Один запит, а не HEAD+GET: частина CDN на HEAD віддає 405 або бреше про
// Content-Length, а Range-GET на 1 МіБ правдивий і дешевий.
func (f *Fetcher) Probe(ctx context.Context, rawURL string, headers map[string]string) (*Probed, error) {
	req, err := f.newRequest(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeWindow-1))

	res, err := f.client.Do(req)
	if err != nil {
		return nil, Classify(rawURL, "зондування", err)
	}
	defer func() { _ = res.Body.Close() }()

	p := &Probed{
		FinalURL:    finalURL(res, rawURL),
		ContentType: res.Header.Get("Content-Type"),
		Validator:   validatorOf(res.Header),
	}

	switch res.StatusCode {
	case http.StatusPartialContent:
		cr, ok := parseContentRange(res.Header.Get("Content-Range"))
		if !ok || cr.Unsatisfied || cr.Start != 0 {
			return nil, fmt.Errorf("%s: незрозумілий Content-Range %q: %w", rawURL, res.Header.Get("Content-Range"), errs.ErrProvider)
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, probeWindow+1))
		if err != nil {
			return nil, Classify(rawURL, "зондування", err)
		}
		if isPlaylist(body) {
			// cr.Total < 0 — сервер написав «*»; тоді про розмір судимо
			// за тілом, яке ми й так прочитали не більше за вікно.
			if cr.Total > probeWindow || len(body) > probeWindow {
				return nil, fmt.Errorf("%s: плейлист завеликий (> %d байт): %w", rawURL, probeWindow, errs.ErrProvider)
			}
			p.Kind, p.Body = ProbePlaylist, body
			return p, nil
		}
		p.Kind = ProbeFile
		if cr.Total > 0 {
			p.Total = cr.Total
		}
		if err := checkFileSize(rawURL, p.Total); err != nil {
			return nil, err
		}
		return p, nil

	case http.StatusOK:
		// Сервер проігнорував Range і почав віддавати весь файл. Читаємо рівно
		// стільки, щоб упізнати плейлист, і одразу закриваємо тіло: 200 МБ
		// webm тут дочитувати не можна ні за яких умов.
		body, err := io.ReadAll(io.LimitReader(res.Body, probeWindow+1))
		if err != nil {
			return nil, Classify(rawURL, "зондування", err)
		}
		if isPlaylist(body) {
			if len(body) > probeWindow {
				return nil, fmt.Errorf("%s: плейлист завеликий (> %d байт): %w", rawURL, probeWindow, errs.ErrProvider)
			}
			p.Kind, p.Body = ProbePlaylist, body
			return p, nil
		}
		p.Kind = ProbeFile
		if res.ContentLength > 0 {
			p.Total = res.ContentLength
		}
		if err := checkFileSize(rawURL, p.Total); err != nil {
			return nil, err
		}
		return p, nil

	default:
		return nil, statusErr(rawURL, res.StatusCode)
	}
}

func checkFileSize(rawURL string, total int64) error {
	if total > MaxFileBytes {
		return fmt.Errorf("%s: файл завеликий (%d байт): %w", rawURL, total, errs.ErrProvider)
	}
	return nil
}

func isPlaylist(body []byte) bool {
	return bytes.HasPrefix(bytes.TrimPrefix(body, utf8BOM), playlistMagic)
}

// Response — відкрите тіло для качання.
type Response struct {
	Body  io.ReadCloser
	Total int64 // повний розмір файла, не залишку; 0 = невідомо
	// FinalURL — URL після редиректів; підписані посилання CDN у ньому
	// протухають, тому зберігати його нікуди не можна.
	FinalURL string
	// Ranged=true — тіло продовжує файл з позиції from. Ranged=false означає,
	// що тіло починається з нуля і викликач мусить писати файл наново.
	Ranged    bool
	Validator Validator
}

// Open відкриває тіло для качання з позиції from. total — відомий викликачеві
// повний розмір (0 = невідомий): якщо сервер назве інший, це вже інший файл.
//
// Контракт Ranged жорсткий: false ЗАВЖДИ означає «тіло з нульового байта».
// Тому на відповідь, яка не збігається з проханням (інший старт діапазону,
// інший total, підмінений валідатор), Open не віддає чуже тіло з прапорцем —
// він закриває його і сам перевідкриває файл з нуля.
func (f *Fetcher) Open(ctx context.Context, rawURL string, headers map[string]string, from, total int64, want Validator) (*Response, error) {
	if from <= 0 {
		return f.openFull(ctx, rawURL, headers)
	}

	req, err := f.newRequest(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", from))
	if !want.IsZero() {
		// If-Range: сервер сам вирішить — віддати діапазон (файл той самий)
		// чи 200 з усім файлом (файл змінився). Дешевше за окремий HEAD.
		req.Header.Set("If-Range", want.ifRange())
	}

	res, err := f.client.Do(req)
	if err != nil {
		return nil, Classify(rawURL, "відкриття діапазону", err)
	}

	switch res.StatusCode {
	case http.StatusPartialContent:
		cr, ok := parseContentRange(res.Header.Get("Content-Range"))
		got := validatorOf(res.Header)
		mismatch := !ok || cr.Unsatisfied || cr.Start != from ||
			(total > 0 && cr.Total > 0 && cr.Total != total) ||
			(!want.IsZero() && !got.IsZero() && got != want)
		if mismatch {
			_ = res.Body.Close()
			return f.openFull(ctx, rawURL, headers)
		}
		out := &Response{
			Body:      res.Body,
			FinalURL:  finalURL(res, rawURL),
			Ranged:    true,
			Validator: got,
		}
		if cr.Total > 0 {
			out.Total = cr.Total
		}
		return out, nil

	case http.StatusOK:
		// Сервер відповів усім файлом (проігнорував Range або If-Range не
		// зійшовся) — тіло справді з нуля, віддаємо як є.
		return fullResponse(res, rawURL), nil

	case http.StatusRequestedRangeNotSatisfiable:
		cr, ok := parseContentRange(res.Header.Get("Content-Range"))
		_ = res.Body.Close()
		if ok && cr.Unsatisfied && cr.Total == from {
			// Ми просили байт за кінцем файла, і кінець рівно там, де ми
			// зупинилися: файл уже повний. Порожнє тіло, Ranged=true —
			// викликач нічого не допише і закриє завантаження.
			return &Response{
				Body:      io.NopCloser(strings.NewReader("")),
				Total:     cr.Total,
				FinalURL:  finalURL(res, rawURL),
				Ranged:    true,
				Validator: validatorOf(res.Header),
			}, nil
		}
		return nil, fmt.Errorf("%s: 416 на діапазон з %d (Content-Range %q): %w",
			rawURL, from, res.Header.Get("Content-Range"), errs.ErrProvider)

	default:
		_ = res.Body.Close()
		return nil, statusErr(rawURL, res.StatusCode)
	}
}

func (f *Fetcher) openFull(ctx context.Context, rawURL string, headers map[string]string) (*Response, error) {
	req, err := f.newRequest(ctx, rawURL, headers)
	if err != nil {
		return nil, err
	}
	res, err := f.client.Do(req)
	if err != nil {
		return nil, Classify(rawURL, "відкриття", err)
	}
	switch res.StatusCode {
	case http.StatusOK:
		return fullResponse(res, rawURL), nil
	case http.StatusPartialContent:
		// Range ми не просили, але дехто віддає 206 і так; приймаємо лише
		// діапазон з нуля — тільки він еквівалентний повному тілу.
		cr, ok := parseContentRange(res.Header.Get("Content-Range"))
		if !ok || cr.Unsatisfied || cr.Start != 0 {
			_ = res.Body.Close()
			return nil, fmt.Errorf("%s: 206 на запит без Range (Content-Range %q): %w",
				rawURL, res.Header.Get("Content-Range"), errs.ErrProvider)
		}
		out := fullResponse(res, rawURL)
		if cr.Total > 0 {
			out.Total = cr.Total
		}
		return out, nil
	default:
		_ = res.Body.Close()
		return nil, statusErr(rawURL, res.StatusCode)
	}
}

// finalURL — URL після редиректів. Фікстурні RoundTripper'и збирають
// http.Response вручну і Request у ньому можуть не заповнити, тому падати тут
// не можна (правило 3): тоді лишається URL, який просили.
func finalURL(res *http.Response, fallback string) string {
	if res.Request != nil && res.Request.URL != nil {
		return res.Request.URL.String()
	}
	return fallback
}

func fullResponse(res *http.Response, rawURL string) *Response {
	out := &Response{
		Body:      res.Body,
		FinalURL:  finalURL(res, rawURL),
		Ranged:    false,
		Validator: validatorOf(res.Header),
	}
	if res.ContentLength > 0 {
		out.Total = res.ContentLength
	}
	return out
}

// contentRange — розібраний заголовок Content-Range. Total < 0 означає, що
// сервер написав «*»; Unsatisfied — форма «bytes */N» у відповіді 416.
type contentRange struct {
	Start       int64
	End         int64
	Total       int64
	Unsatisfied bool
}

func parseContentRange(v string) (contentRange, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(v), "bytes ")
	if !ok {
		return contentRange{}, false
	}
	spec, totalStr, ok := strings.Cut(rest, "/")
	if !ok {
		return contentRange{}, false
	}
	cr := contentRange{Start: -1, End: -1, Total: -1}
	if totalStr != "*" {
		n, err := strconv.ParseInt(totalStr, 10, 64)
		if err != nil || n < 0 {
			return contentRange{}, false
		}
		cr.Total = n
	}
	if spec == "*" {
		cr.Unsatisfied = true
		return cr, true
	}
	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok {
		return contentRange{}, false
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return contentRange{}, false
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return contentRange{}, false
	}
	cr.Start, cr.End = start, end
	return cr, true
}
