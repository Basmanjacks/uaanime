// Package remote — крихітний веб-пульт для телефона в тій самій Wi-Fi.
//
// Модель загроз — домашня мережа. Два режими:
//
//   - Токенний (config remote: on). Автентифікація — 128-бітний токен у шляху,
//     без нього пульт відповідає 404 на все; токен постійний, бо посилання живе
//     в закладці телефона.
//   - Відкритий (remote: open). Пульт живе в корені, токена немає, і керувати
//     може будь-який пристрій у тій самій мережі — користувач приймає це
//     свідомо заради короткої адреси. Що лишається закритим: чужий сайт у
//     браузері на цій же машині. CSRF відсікає CrossOriginProtection (обидва
//     режими), а DNS rebinding — перевірка Host: лише IP, localhost, ім'я без
//     крапок або *.local, тобто рівно ті адреси, які застосунок друкує.
//
// Трафік іде відкритим HTTP свідомо: сусід по Wi-Fi, який підгляне URL (або в
// відкритому режимі — будь-хто в мережі), отримає рівно право поставити на
// паузу, перемотати, перемкнути серію або зупинити відтворення на цій машині.
// Ні файлів, ні довільних URL, ні виконання команд пульт не дає, тому TLS і
// паролі — плата складністю за нуль виграшу.
//
// HTML тут ВЛАСНИЙ і рендериться html/template: правило AGENTS.md «жодного HTML
// поза provider/extractor» стосується розбору чужих сторінок, а не власної
// сторінки, яку ми самі й породжуємо.
package remote

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

//go:embed page.html
var pageHTML string

// Status — усе, що пульт показує на екрані. Значення лише скалярні: сторінка
// має рендеритися з одного JSON без додаткових запитів.
type Status struct {
	Playing     bool    `json:"playing"`
	Title       string  `json:"title"`
	Episode     int     `json:"episode"`
	PositionSec float64 `json:"position_sec"`
	DurationSec float64 `json:"duration_sec"`
	Paused      bool    `json:"paused"`
	// VolumePct — 0..100; VolumeUnknown, коли плеєр не назвав гучність
	// (нічого не грає або доріжки немає) — 0 там означало б тишу.
	VolumePct float64 `json:"volume_pct"`
	StopAfter bool    `json:"stop_after"`
	// PlaylistGen — покоління списку серій; 0 = списку немає. Сторінка стежить
	// саме за ним, а не за Episode: поза грою Episode нульовий, а список серій
	// уже може бути іншим. Наповнює його S19 — поки контролер віддає 0.
	PlaylistGen int `json:"playlist_gen"`
}

// VolumeUnknown — гучність недоступна. Дзеркалить playback.VolumeUnknown:
// remote не імпортує playback, тому константа мусить жити й тут.
const VolumeUnknown = -1.0

// ErrNotPlaying — команда, коли нічого не грає → 409. Перехідник у cmd/uaanime
// мапить сюди помилку рушія; remote не імпортує playback.
var ErrNotPlaying = errors.New("зараз нічого не грає")

// ErrBadToken — токен не має форми 128-бітного шістнадцяткового рядка. Ловимо
// на старті, бо коротший токен — це вже не автентифікація.
var ErrBadToken = errors.New("токен пульта має бути 32 шістнадцятковими символами")

// Controller — те, чим керує пульт. Інтерфейс оголошений тут, у споживача:
// remote нічого не знає про playback, а playback — про HTTP.
type Controller interface {
	Status() (Status, error) // помилка → 500; idle — не помилка, а Playing:false
	TogglePause() error
	Seek(deltaSec float64) error
	SeekTo(posSec float64) error
	AddVolume(delta float64) error
	// ToggleStopAfter — перемикач без аргументу: сторінка не надсилає бажаний
	// стан, бо між опитуванням і тапом його міг змінити TUI.
	ToggleStopAfter() error
	Next() error
	Stop() error
}

// Кроки в секундах і відсотках. Кнопкові кроки зашиті у шляхи: нуль парсингу
// вводу з мережі. Число з мережі приймає лише seek/<секунди> — тап по смузі
// прогресу інакше не виразити.
const (
	seekStep     = 10
	seekStepLong = 30
	volumeStep   = 5
)

// maxSeekDigits — стеля довжини числа в seek/<секунди>. Шість цифр — це 11 діб
// відео; довше приймати нема сенсу, а обмеження довжини відсікає сміття ще до
// арифметики.
const maxSeekDigits = 6

// pathPrefix — усе, що не починається з нього, невидиме для пульта.
const pathPrefix = "/r/"

// Тайм-аути навмисно щедрі: VLC на тому боці може думати секунди, але жоден
// запит не має права тримати з'єднання довше.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 15 * time.Second
	maxHeaderBytes    = 8 << 10
)

type handler struct {
	// open — пульт без токена в корені; token тоді порожній і не перевіряється.
	open  bool
	token []byte
	csrf  *http.CrossOriginProtection
	ctrl  Controller
	page  []byte
}

type pageData struct {
	// Base — "/r/<token>/" (або "/" у відкритому режимі), із похилою рискою в
	// кінці: JS клеїть BASE + "status". Тип JSStr і підстановка в шаблоні БЕЗ лапок (`const BASE = {{.Base}};`):
	// усередині лапок html/template екранує кожну "/" як "\/", а в позиції
	// JS-значення JSStr виводиться дослівно й сам обгортається лапками. Це
	// безпечно, бо значення — константний префікс плюс токен, уже перевірений
	// на 32 шістнадцяткові символи: ні лапки, ні розриву рядка там не буде.
	Base    template.JSStr
	Title   string
	Idle    string
	Episode string
	Play    string
	Pause   string
	Back    string
	Forward string
	Next    string
	Stop    string
	Offline string
}

type errBody struct {
	Error string `json:"error"`
}

// NewHandler — токенний пульт без сокета: так його можна ганяти через httptest.
// Порожній токен — помилка, а не відкритий режим: випадкове "" не має тихо
// відкривати пульт усій мережі; для цього є NewOpenHandler.
func NewHandler(token string, c Controller) (http.Handler, error) {
	if !validToken(token) {
		return nil, ErrBadToken
	}
	return newHandler(token, false, c)
}

// NewOpenHandler — пульт без токена в корені (config remote: open).
func NewOpenHandler(c Controller) (http.Handler, error) {
	return newHandler("", true, c)
}

func newHandler(token string, open bool, c Controller) (http.Handler, error) {
	if c == nil {
		return nil, errors.New("remote: контролер не заданий")
	}
	tmpl, err := template.New("page").Parse(pageHTML)
	if err != nil {
		return nil, err
	}
	base := "/"
	if !open {
		base = pathPrefix + token + "/"
	}
	// Сторінка статична для даного токена, тож рендеримо її раз на старті:
	// кожен запит з телефона — це просто копія байтів.
	var buf bytes.Buffer
	data := pageData{
		Base:    template.JSStr(base),
		Title:   i18n.RemotePageTitle,
		Idle:    i18n.RemoteIdle,
		Episode: i18n.RemoteEpisodeFmt,
		Play:    i18n.RemotePlay,
		Pause:   i18n.RemotePause,
		Back:    i18n.RemoteBack,
		Forward: i18n.RemoteForward,
		Next:    i18n.RemoteNext,
		Stop:    i18n.RemoteStop,
		Offline: i18n.RemoteOffline,
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return &handler{
		open:  open,
		token: []byte(token),
		csrf:  http.NewCrossOriginProtection(),
		ctrl:  c,
		page:  buf.Bytes(),
	}, nil
}

func validToken(t string) bool {
	if len(t) != 32 {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// ServeHTTP — свій диспетчер замість ServeMux: маршрутів жменя, а ServeMux із
// методами в шаблонах віддавав би 405 і на невідомий токен, тобто підтверджував
// би його існування.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	secureHeaders(w)

	// Відкритий режим: без токена єдине, що відрізняє телефон від сайту, який
	// перерезолвив своє ім'я на наш IP (DNS rebinding), — це заголовок Host.
	if h.open && !hostAllowed(r.Host) {
		notFound(w)
		return
	}
	suffix, ok := h.route(r.URL.Path)
	if !ok {
		notFound(w)
		return
	}

	switch suffix {
	case "":
		h.onGet(w, r, h.servePage)
	case "status":
		h.onGet(w, r, h.serveStatus)
	case "pause":
		h.onPost(w, r, h.ctrl.TogglePause)
	case "back":
		h.onPost(w, r, func() error { return h.ctrl.Seek(-seekStep) })
	case "forward":
		h.onPost(w, r, func() error { return h.ctrl.Seek(seekStep) })
	case "back30":
		h.onPost(w, r, func() error { return h.ctrl.Seek(-seekStepLong) })
	case "forward30":
		h.onPost(w, r, func() error { return h.ctrl.Seek(seekStepLong) })
	case "volup":
		h.onPost(w, r, func() error { return h.ctrl.AddVolume(volumeStep) })
	case "voldown":
		h.onPost(w, r, func() error { return h.ctrl.AddVolume(-volumeStep) })
	case "stopafter":
		h.onPost(w, r, h.ctrl.ToggleStopAfter)
	case "next":
		h.onPost(w, r, h.ctrl.Next)
	case "stop":
		h.onPost(w, r, h.ctrl.Stop)
	default:
		sec, ok := parseSeekTo(suffix)
		if !ok {
			notFound(w)
			return
		}
		h.onPost(w, r, func() error { return h.ctrl.SeekTo(sec) })
	}
}

// parseSeekTo розбирає суфікс "seek/<секунди>". Парсер власний, а не strconv:
// той приймає "+42", "-42" і пробіли, а тут має пройти рівно [0-9]{1,6}. Будь-що
// інше — невідомий шлях, тобто 404, а не 400: 400 підтвердив би сторонньому, що
// токен угадано і помилка лише в аргументі.
func parseSeekTo(suffix string) (posSec float64, ok bool) {
	digits, ok := strings.CutPrefix(suffix, "seek/")
	if !ok || digits == "" || len(digits) > maxSeekDigits {
		return 0, false
	}
	sec := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		sec = sec*10 + int(c-'0')
	}
	return float64(sec), true
}

// route зводить шлях до суфікса команди: "" — сторінка, "status", "pause"...
// У відкритому режимі "/r/<щось>" лишається невідомим суфіксом → 404.
func (h *handler) route(path string) (suffix string, ok bool) {
	if h.open {
		return strings.CutPrefix(path, "/")
	}
	rest, ok := strings.CutPrefix(path, pathPrefix)
	if !ok {
		return "", false
	}
	tok, suffix, _ := strings.Cut(rest, "/")
	// Порівняння сталого часу: токен — єдина автентифікація, і різниця в часі
	// відповіді дозволила б підбирати його побайтово.
	if subtle.ConstantTimeCompare([]byte(tok), h.token) != 1 {
		return "", false
	}
	return suffix, true
}

// hostAllowed — Host, за яким телефон реально може прийти: IP, localhost, ім'я
// без крапок (mDNS/пошуковий домен) або *.local. Публічне ім'я нападника має
// крапку і публічний TLD, тож сюди не потрапить. Імена від роутера (.lan,
// .fritz.box) теж відкидаються — у відкритому режимі це ціна за безпеку, і
// README про це каже.
func hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch {
	case host == "":
		return false
	case net.ParseIP(host) != nil, host == "localhost":
		return true
	case !strings.Contains(host, "."):
		return true
	default:
		return strings.HasSuffix(host, ".local")
	}
}

func (h *handler) onGet(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter)) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	fn(w)
}

func (h *handler) onPost(w http.ResponseWriter, r *http.Request, fn func() error) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	// Чужий сайт у браузері не має слати команди пульту: Sec-Fetch-Site, а в
	// старих браузерах — Origin проти Host. Запити без цих заголовків (curl,
	// тести) проходять — вони й так не з браузера.
	if err := h.csrf.Check(r); err != nil {
		forbidden(w)
		return
	}
	if err := fn(); err != nil {
		writeError(w, err)
		return
	}
	// Ехо свіжого стану: телефон малює результат зі своєї ж відповіді й не
	// чекає наступного опитування.
	h.serveStatus(w)
}

func (h *handler) servePage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.page)
}

func (h *handler) serveStatus(w http.ResponseWriter) {
	st, err := h.ctrl.Status()
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func secureHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

// notFound — порожнє тіло навмисно: сторонній не має відрізнити «не той токен»
// від «немає такого шляху».
func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
}

// forbidden — POST із чужого origin; тіло порожнє з тієї ж причини, що й у 404.
func forbidden(w http.ResponseWriter) {
	w.WriteHeader(http.StatusForbidden)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotPlaying) {
		writeJSON(w, http.StatusConflict, errBody{Error: i18n.RemoteIdle})
		return
	}
	// Текст помилки вже українською — його склав шар плеєра. Пульт не знає ні
	// про провайдера, ні про класифікацію помилок, тож пропускає як є.
	writeJSON(w, http.StatusInternalServerError, errBody{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Server — http.Server поверх уже відкритого слухача.
type Server struct {
	srv    *http.Server
	served chan struct{}
}

// Start піднімає пульт на готовому слухачі. Слухач і хендлер приймає ззовні:
// порт треба знати ще до старту (його показують у TUI та в doctor), а режим
// пульта — токенний чи відкритий — обирає викликач через NewHandler/NewOpenHandler.
func Start(ln net.Listener, h http.Handler) *Server {
	s := &Server{
		srv: &http.Server{
			Handler:           h,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			// net/http за замовчуванням пише в stderr, а stderr тут — це
			// альтернативний екран TUI: один розрив з'єднання зіпсував би кадр.
			ErrorLog: log.New(io.Discard, "", 0),
		},
		served: make(chan struct{}),
	}
	go func() {
		defer close(s.served)
		_ = s.srv.Serve(ln)
	}()
	return s
}

// Close глушить пульт м'яко: слухач закривається одразу, відкриті запити
// дограються до ctx.
func (s *Server) Close(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	<-s.served
	return err
}
