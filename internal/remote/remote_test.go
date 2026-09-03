package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/i18n"
)

const testToken = "0123456789abcdef0123456789abcdef"

// fakeCtl записує виклики й дає задати помилку. Кожна успішна команда рухає
// позицію — так тест доводить, що ехо у відповіді свіже, а не з кеша.
type fakeCtl struct {
	st      Status
	stErr   error
	cmdErr  error
	calls   []string
	seeks   []float64
	seekTos []float64
	volumes []float64
	// playlist — те, що віддає Episodes; plays — пари (покоління, серія), з
	// якими прийшов тап по рядку списку.
	playlist Playlist
	plErr    error
	plays    [][2]int
}

func (f *fakeCtl) Status() (Status, error) {
	if f.stErr != nil {
		return Status{}, f.stErr
	}
	return f.st, nil
}

func (f *fakeCtl) TogglePause() error { return f.record("pause") }
func (f *fakeCtl) Next() error        { return f.record("next") }
func (f *fakeCtl) Stop() error        { return f.record("stop") }

func (f *fakeCtl) Seek(delta float64) error {
	f.seeks = append(f.seeks, delta)
	return f.record("seek")
}

func (f *fakeCtl) SeekTo(pos float64) error {
	f.seekTos = append(f.seekTos, pos)
	return f.record("seekto")
}

func (f *fakeCtl) AddVolume(delta float64) error {
	f.volumes = append(f.volumes, delta)
	return f.record("volume")
}

// Перемикач, як і справжній контролер: стан живе в моделі, а не в запиті.
func (f *fakeCtl) ToggleStopAfter() error {
	if err := f.record("stopafter"); err != nil {
		return err
	}
	f.st.StopAfter = !f.st.StopAfter
	return nil
}

func (f *fakeCtl) Episodes() (Playlist, error) {
	if f.plErr != nil {
		return Playlist{}, f.plErr
	}
	return f.playlist, nil
}

// Play імітує справжній контролер: чужий gen — застарілий список, і до моделі
// запит не доходить.
func (f *fakeCtl) Play(gen, n int) error {
	if err := f.record("play"); err != nil {
		return err
	}
	if gen != f.playlist.Gen || gen == 0 {
		return fmt.Errorf("gen %d: %w", gen, ErrStalePlaylist)
	}
	f.plays = append(f.plays, [2]int{gen, n})
	return nil
}

func (f *fakeCtl) record(name string) error {
	f.calls = append(f.calls, name)
	if f.cmdErr != nil {
		return f.cmdErr
	}
	f.st.PositionSec++
	return nil
}

func playingCtl() *fakeCtl {
	return &fakeCtl{st: Status{Playing: true, Title: "Фрірен", Episode: 3, PositionSec: 42, DurationSec: 1440, VolumePct: 60}}
}

func newTestHandler(t *testing.T, c Controller) http.Handler {
	t.Helper()
	h, err := NewHandler(testToken, c)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h
}

func do(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func base(suffix string) string { return "/r/" + testToken + "/" + suffix }

func newOpenHandler(t *testing.T, c Controller) http.Handler {
	t.Helper()
	h, err := NewOpenHandler(c)
	if err != nil {
		t.Fatalf("NewOpenHandler: %v", err)
	}
	return h
}

// doReq — як do, але з керованим Host і заголовками: httptest підставляє
// Host "example.com", який відкритий режим відкидає як чужий.
func doReq(t *testing.T, h http.Handler, method, path, host string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if host != "" {
		req.Host = host
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const lanHost = "192.168.1.2:52430"

func TestOpenHandlerServesAtRoot(t *testing.T) {
	c := playingCtl()
	h := newOpenHandler(t, c)

	rec := doReq(t, h, http.MethodGet, "/", lanHost, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `const BASE = "/";`) {
		t.Fatalf("GET / = %d, BASE не корінь:\n%s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/status", lanHost, nil)
	if rec.Code != http.StatusOK || !decodeStatus(t, rec).Playing {
		t.Fatalf("GET /status = %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/pause", lanHost, nil)
	if rec.Code != http.StatusOK || len(c.calls) != 1 || c.calls[0] != "pause" {
		t.Fatalf("POST /pause = %d, calls %v", rec.Code, c.calls)
	}

	for _, p := range []string{"/r/" + testToken, "/r/" + testToken + "/status", "/bogus", "/r"} {
		rec := doReq(t, h, http.MethodGet, p, lanHost, nil)
		if rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q, очікував 404 без тіла", p, rec.Code, rec.Body.String())
		}
	}
}

// Чужий сайт у браузері не має слати команди: Sec-Fetch-Site, а без нього —
// Origin проти Host. Однаково в обох режимах.
func TestCrossOriginPostIsForbidden(t *testing.T) {
	modes := []struct {
		name string
		mk   func(*fakeCtl) http.Handler
		page string
		cmd  string
	}{
		{"token", func(c *fakeCtl) http.Handler { return newTestHandler(t, c) }, "/r/" + testToken, base("pause")},
		{"open", func(c *fakeCtl) http.Handler { return newOpenHandler(t, c) }, "/", "/pause"},
	}
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"same-origin", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"matching origin", map[string]string{"Origin": "http://192.168.1.2:52430"}, http.StatusOK},
		{"no headers", nil, http.StatusOK},
	}
	for _, m := range modes {
		for _, tc := range cases {
			t.Run(m.name+"/"+tc.name, func(t *testing.T) {
				c := playingCtl()
				h := m.mk(c)
				rec := doReq(t, h, http.MethodPost, m.cmd, lanHost, tc.hdr)
				if rec.Code != tc.want {
					t.Fatalf("POST = %d, очікував %d (тіло %q)", rec.Code, tc.want, rec.Body.String())
				}
				called := len(c.calls) == 1
				if called != (tc.want == http.StatusOK) {
					t.Fatalf("виклики контролера = %v при коді %d", c.calls, rec.Code)
				}
				if rec.Code == http.StatusForbidden && rec.Body.Len() != 0 {
					t.Fatalf("403 з тілом %q", rec.Body.String())
				}
				// безпечні методи не блокуються
				if rec := doReq(t, h, http.MethodGet, m.page, lanHost, tc.hdr); rec.Code != http.StatusOK {
					t.Fatalf("GET сторінки = %d", rec.Code)
				}
			})
		}
	}
}

// DNS rebinding: сайт нападника резолвиться на наш IP, але Host лишається
// його. У відкритому режимі це єдина відмінність телефона від чужого сайту.
func TestOpenHandlerRejectsForeignHost(t *testing.T) {
	c := playingCtl()
	h := newOpenHandler(t, c)
	for _, host := range []string{"evil.example:52430", "mac.evil.example", "vzmacbook.lan", "", ":52430"} {
		rec := doReq(t, h, http.MethodPost, "/pause", host, nil)
		if rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("Host %q: %d %q, очікував 404 без тіла", host, rec.Code, rec.Body.String())
		}
	}
	if len(c.calls) != 0 {
		t.Fatalf("контролер викликано з чужого Host: %v", c.calls)
	}
	for _, host := range []string{"192.168.1.2:52430", "[::1]:52430", "vzmacbook.local:52430", "VZMacBook.LOCAL", "vzmacbook", "localhost"} {
		if rec := doReq(t, h, http.MethodGet, "/status", host, nil); rec.Code != http.StatusOK {
			t.Errorf("Host %q: %d, очікував 200", host, rec.Code)
		}
	}
	// у токенному режимі перевірки Host нема — імена від роутера мають працювати
	if rec := doReq(t, newTestHandler(t, playingCtl()), http.MethodGet, base("status"), "vzmacbook.lan:52430", nil); rec.Code != http.StatusOK {
		t.Fatalf("токенний режим із Host .lan = %d", rec.Code)
	}
}

func decodeStatus(t *testing.T, rec *httptest.ResponseRecorder) Status {
	t.Helper()
	var st Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("розбір Status: %v (тіло %q)", err, rec.Body.String())
	}
	return st
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("розбір помилки: %v (тіло %q)", err, rec.Body.String())
	}
	return e.Error
}

func TestUnknownPathsAre404WithEmptyBody(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	paths := []string{
		"/r/ffffffffffffffffffffffffffffffff",       // не той токен
		"/r/ffffffffffffffffffffffffffffffff/pause", // не той токен + команда
		"/r/" + testToken[:31],                      // коротший токен
		base("bogus"),                               // невідомий суфікс
		"/",                                         // інший префікс
		"/status",
		"/r",
	}
	for _, p := range paths {
		rec := do(t, h, http.MethodGet, p)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: код %d, очікував 404", p, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s: тіло %q, очікував порожнє", p, rec.Body.String())
		}
	}
}

func TestBaseWithTrailingSlashServesPage(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	for _, p := range []string{"/r/" + testToken, "/r/" + testToken + "/"} {
		rec := do(t, h, http.MethodGet, p)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: код %d", p, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Fatalf("%s: Content-Type %q", p, got)
		}
	}
}

func TestGetOnCommandPathIs405(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	for _, cmd := range []string{"pause", "back", "forward", "back30", "forward30", "volup", "voldown", "stopafter", "next", "stop", "seek/42"} {
		rec := do(t, h, http.MethodGet, base(cmd))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: код %d, очікував 405", cmd, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Errorf("GET %s: Allow %q, очікував POST", cmd, got)
		}
	}
}

func TestPostOnPageAndStatusIs405(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	for _, p := range []string{"/r/" + testToken, base("status")} {
		rec := do(t, h, http.MethodPost, p)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: код %d, очікував 405", p, rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodGet {
			t.Errorf("POST %s: Allow %q, очікував GET", p, got)
		}
	}
}

func TestEachCommandCallsControllerOnceAndEchoesFreshStatus(t *testing.T) {
	cases := []struct{ path, want string }{
		{"pause", "pause"},
		{"back", "seek"},
		{"forward", "seek"},
		{"back30", "seek"},
		{"forward30", "seek"},
		{"volup", "volume"},
		{"voldown", "volume"},
		{"seek/42", "seekto"},
		{"stopafter", "stopafter"},
		{"next", "next"},
		{"stop", "stop"},
	}
	for _, tc := range cases {
		c := playingCtl()
		h := newTestHandler(t, c)
		before := c.st.PositionSec

		rec := do(t, h, http.MethodPost, base(tc.path))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: код %d, тіло %q", tc.path, rec.Code, rec.Body.String())
		}
		if len(c.calls) != 1 || c.calls[0] != tc.want {
			t.Fatalf("%s: виклики %v, очікував рівно [%s]", tc.path, c.calls, tc.want)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Fatalf("%s: Content-Type %q", tc.path, got)
		}
		st := decodeStatus(t, rec)
		if st.PositionSec != before+1 {
			t.Fatalf("%s: ехо position=%v, очікував свіжий %v", tc.path, st.PositionSec, before+1)
		}
		if st.Title != c.st.Title || st.Episode != c.st.Episode {
			t.Fatalf("%s: ехо %+v не збігається зі станом %+v", tc.path, st, c.st)
		}
	}
}

func TestSeekDirections(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	for _, cmd := range []string{"back", "forward", "back30", "forward30"} {
		do(t, h, http.MethodPost, base(cmd))
	}
	want := []float64{-seekStep, seekStep, -seekStepLong, seekStepLong}
	if !sameFloats(c.seeks, want) {
		t.Fatalf("seeks = %v, очікував %v", c.seeks, want)
	}
}

func TestVolumeDirections(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	do(t, h, http.MethodPost, base("volup"))
	do(t, h, http.MethodPost, base("voldown"))
	want := []float64{volumeStep, -volumeStep}
	if !sameFloats(c.volumes, want) {
		t.Fatalf("volumes = %v, очікував %v", c.volumes, want)
	}
}

// Тап по смузі прогресу — єдине число, яке пульт бере з мережі.
func TestSeekToParsesOnlyDigits(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	for _, tc := range []struct {
		path string
		want float64
	}{
		{"seek/42", 42},
		{"seek/0", 0},
		{"seek/999999", 999999},
	} {
		c.seekTos = nil
		rec := do(t, h, http.MethodPost, base(tc.path))
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %s: код %d", tc.path, rec.Code)
		}
		if !sameFloats(c.seekTos, []float64{tc.want}) {
			t.Fatalf("POST %s: seekTos = %v, очікував [%v]", tc.path, c.seekTos, tc.want)
		}
	}

	c.seekTos = nil
	bad := []string{
		"seek/abc", "seek/1234567", "seek/-30", "seek/+30", "seek/4.2",
		"seek/", "seek", "seek/42/x", "seek/%2042", "seek/1e3",
	}
	for _, p := range bad {
		rec := do(t, h, http.MethodPost, base(p))
		if rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("POST %s: %d %q, очікував 404 без тіла", p, rec.Code, rec.Body.String())
		}
	}
	if len(c.seekTos) != 0 {
		t.Fatalf("сміття дійшло до контролера: %v", c.seekTos)
	}
}

func sameFloats(got, want []float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// «Досидіти й зупинитись» — перемикач: сторінка малює його стан із ехо-статусу,
// тому другий тап мусить вимикати те, що ввімкнув перший.
func TestStopAfterTogglesBothWays(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	for i, want := range []bool{true, false} {
		rec := do(t, h, http.MethodPost, base("stopafter"))
		if rec.Code != http.StatusOK {
			t.Fatalf("тап %d: код %d, тіло %q", i+1, rec.Code, rec.Body.String())
		}
		if got := decodeStatus(t, rec).StopAfter; got != want {
			t.Fatalf("тап %d: stop_after = %v, очікував %v", i+1, got, want)
		}
	}
	if len(c.calls) != 2 {
		t.Fatalf("виклики контролера = %v, очікував два перемикання", c.calls)
	}
}

// Новий маршрут без захисту від CSRF — це той самий чужий сайт, що керує плеєром.
func TestCrossOriginPostIsForbiddenOnNewPaths(t *testing.T) {
	for _, cmd := range []string{"back30", "forward30", "volup", "voldown", "stopafter", "seek/42"} {
		c := playingCtl()
		h := newTestHandler(t, c)
		rec := doReq(t, h, http.MethodPost, base(cmd), lanHost, map[string]string{"Sec-Fetch-Site": "cross-site"})
		if rec.Code != http.StatusForbidden || rec.Body.Len() != 0 {
			t.Errorf("POST %s: %d %q, очікував 403 без тіла", cmd, rec.Code, rec.Body.String())
		}
		if len(c.calls) != 0 {
			t.Errorf("POST %s: контролер викликано з чужого origin: %v", cmd, c.calls)
		}
	}
}

// Сторінка розбирає JSON за іменами полів: перейменування чи зникнення поля —
// це мовчазна поломка пульта, а не помилка компіляції.
func TestStatusJSONFields(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	rec := do(t, h, http.MethodGet, base("status"))
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("розбір: %v (%q)", err, rec.Body.String())
	}
	want := []string{
		"playing", "title", "episode", "position_sec", "duration_sec", "paused",
		"volume_pct", "stop_after", "playlist_gen",
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("у Status немає поля %q: %s", k, rec.Body.String())
		}
	}
	if len(got) != len(want) {
		t.Errorf("полів %d, очікував %d: %s", len(got), len(want), rec.Body.String())
	}
}

func TestStatusEndpointReturnsStatus(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	rec := do(t, h, http.MethodGet, base("status"))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d", rec.Code)
	}
	st := decodeStatus(t, rec)
	if st != c.st {
		t.Fatalf("Status = %+v, очікував %+v", st, c.st)
	}
	if len(c.calls) != 0 {
		t.Fatalf("GET /status смикнув команди: %v", c.calls)
	}
}

func TestIdleIsNotAnError(t *testing.T) {
	h := newTestHandler(t, &fakeCtl{})
	rec := do(t, h, http.MethodGet, base("status"))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, очікував 200", rec.Code)
	}
	if st := decodeStatus(t, rec); st.Playing {
		t.Fatalf("Playing = true для порожнього стану")
	}
}

func TestNotPlayingIs409(t *testing.T) {
	c := playingCtl()
	c.cmdErr = fmt.Errorf("x: %w", ErrNotPlaying)
	h := newTestHandler(t, c)
	rec := do(t, h, http.MethodPost, base("pause"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("код %d, очікував 409", rec.Code)
	}
	if got := decodeErr(t, rec); got != i18n.RemoteIdle {
		t.Fatalf("error = %q, очікував %q", got, i18n.RemoteIdle)
	}
}

func TestControllerErrorIs500(t *testing.T) {
	c := playingCtl()
	c.cmdErr = errors.New("VLC не відповідає")
	h := newTestHandler(t, c)
	rec := do(t, h, http.MethodPost, base("next"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("код %d, очікував 500", rec.Code)
	}
	if got := decodeErr(t, rec); got != "VLC не відповідає" {
		t.Fatalf("error = %q", got)
	}
}

func TestStatusErrorIs500OnGetAndAfterCommand(t *testing.T) {
	c := playingCtl()
	c.stErr = errors.New("сокет плеєра закрито")
	h := newTestHandler(t, c)

	rec := do(t, h, http.MethodGet, base("status"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("GET /status: код %d, очікував 500", rec.Code)
	}

	rec = do(t, h, http.MethodPost, base("pause"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("POST /pause: код %d, очікував 500", rec.Code)
	}
	if len(c.calls) != 1 {
		t.Fatalf("команда мала виконатися рівно раз, маємо %v", c.calls)
	}
	if got := decodeErr(t, rec); got != "сокет плеєра закрито" {
		t.Fatalf("error = %q", got)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	c := playingCtl()
	h := newTestHandler(t, c)
	reqs := []struct{ method, path string }{
		{http.MethodGet, "/r/" + testToken},
		{http.MethodGet, base("status")},
		{http.MethodPost, base("pause")},
		{http.MethodGet, base("pause")},
		{http.MethodGet, "/r/ffffffffffffffffffffffffffffffff"},
		{http.MethodGet, "/nope"},
	}
	want := map[string]string{
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	}
	for _, r := range reqs {
		rec := do(t, h, r.method, r.path)
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s %s: %s = %q, очікував %q", r.method, r.path, k, got, v)
			}
		}
	}
}

func TestPageIsSelfContained(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	rec := do(t, h, http.MethodGet, "/r/"+testToken)
	body := rec.Body.String()

	if n := len(body); n >= 8192 {
		t.Fatalf("сторінка %d байтів, стеля 8192", n)
	}
	if want := `const BASE = "/r/` + testToken + `/";`; !strings.Contains(body, want) {
		t.Fatalf("немає %q у сторінці", want)
	}

	// Усі підписи приходять із i18n. Порівнюємо з розекранованим тілом: html/template
	// перетворює апостроф на &#39;, і браузер повертає його назад під час розбору.
	plain := html.UnescapeString(body)
	labels := map[string]string{
		"RemotePageTitle":  i18n.RemotePageTitle,
		"RemoteIdle":       i18n.RemoteIdle,
		"RemoteEpisodeFmt": i18n.RemoteEpisodeFmt,
		"RemotePlay":       i18n.RemotePlay,
		"RemotePause":      i18n.RemotePause,
		"RemoteBack":       i18n.RemoteBack,
		"RemoteForward":    i18n.RemoteForward,
		"RemoteNext":       i18n.RemoteNext,
		"RemoteStop":       i18n.RemoteStop,
		"RemoteOffline":    i18n.RemoteOffline,
	}
	for name, v := range labels {
		if !strings.Contains(plain, v) {
			t.Errorf("на сторінці немає i18n.%s (%q)", name, v)
		}
	}

	// Динаміка йде виключно через textContent: жодного шляху для HTML-ін'єкції.
	if re := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write`); re.MatchString(body) {
		t.Errorf("сторінка вставляє HTML замість textContent: %q", re.FindString(body))
	}
	// Нуль зовнішніх ресурсів: пульт має працювати в мережі без інтернету.
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`(?i)https?:`),
		regexp.MustCompile(`["'(\s]//[A-Za-z0-9]`),
		regexp.MustCompile(`(?i)(src|href)\s*=`),
	} {
		if re.MatchString(body) {
			t.Errorf("зовнішнє посилання на сторінці: %q", re.FindString(body))
		}
	}
}

func TestHostileTitleIsEscapedAsJSONData(t *testing.T) {
	const hostile = `<script>alert(1)</script>`
	c := playingCtl()
	c.st.Title = hostile
	h := newTestHandler(t, c)

	// Сама сторінка назви не містить — вона приходить лише JSON-ом і лягає
	// в textContent, тому перевіряємо саме екранування в JSON.
	if strings.Contains(do(t, h, http.MethodGet, "/r/"+testToken).Body.String(), "alert(1)") {
		t.Fatal("сторінка вбудувала назву тайтла")
	}
	rec := do(t, h, http.MethodGet, base("status"))
	if strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("JSON не екранував '<': %s", rec.Body.String())
	}
	if st := decodeStatus(t, rec); st.Title != hostile {
		t.Fatalf("Title = %q, очікував %q", st.Title, hostile)
	}
}

// Кожен шлях, який JS клеїть із BASE, має існувати — інакше кнопка мовчки нічого не робить.
func TestEveryPathUsedByJSExists(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	cases := []struct {
		suffix, method string
	}{
		{"status", http.MethodGet},
		{"pause", http.MethodPost},
		{"back", http.MethodPost},
		{"forward", http.MethodPost},
		{"next", http.MethodPost},
		{"stop", http.MethodPost},
	}
	for _, tc := range cases {
		rec := do(t, h, tc.method, base(tc.suffix))
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s: код %d, очікував 200", tc.method, tc.suffix, rec.Code)
		}
	}
}

func TestStartAndCloseServeOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skip("пісочниця забороняє TCP loopback")
		}
		t.Fatalf("Listen: %v", err)
	}
	srv := Start(ln, newTestHandler(t, playingCtl()))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	url := "http://" + ln.Addr().String() + base("status")
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("читання тіла: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код %d, тіло %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"playing":true`) {
		t.Fatalf("тіло %s", body)
	}
}

// ---- список серій і тап по рядку (S20) ----

func testPlaylist() Playlist {
	return Playlist{Gen: 7, Title: "Фрірен", Episodes: []Episode{
		{Number: 1, Watched: true},
		{Number: 2, PositionSec: 581, Current: true},
		{Number: 3},
	}}
}

// Сторінка малює список рівно тим, що прийшло: порядок і прапорці — частина
// контракту, як і імена полів.
func TestEpisodesEndpointReturnsPlaylist(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)

	rec := do(t, h, http.MethodGet, base("episodes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тіло %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type %q", got)
	}
	var got Playlist
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("розбір: %v (%q)", err, rec.Body.String())
	}
	if got.Gen != 7 || got.Title != "Фрірен" || len(got.Episodes) != 3 {
		t.Fatalf("Playlist = %+v", got)
	}
	for i, want := range c.playlist.Episodes {
		if got.Episodes[i] != want {
			t.Errorf("серія %d = %+v, очікував %+v", i, got.Episodes[i], want)
		}
	}
	// імена полів — контракт зі сторінкою
	var raw struct {
		Gen      int                          `json:"gen"`
		Episodes []map[string]json.RawMessage `json:"episodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"number", "watched", "position_sec", "current"} {
		if _, ok := raw.Episodes[0][key]; !ok {
			t.Errorf("у серії немає поля %q: %s", key, rec.Body.String())
		}
	}
	if len(c.calls) != 0 {
		t.Fatalf("GET episodes смикнув команди: %v", c.calls)
	}
}

// Списку немає — це не помилка: сторінка має отримати порожній масив, а не null.
func TestEpisodesEndpointEmptyPlaylist(t *testing.T) {
	h := newTestHandler(t, playingCtl())
	rec := do(t, h, http.MethodGet, base("episodes"))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"gen":0,"episodes":[]}` {
		t.Fatalf("тіло = %s, очікував порожній список", got)
	}
}

// Тап по рядку списку: покоління доходить до контролера як є, і у відповідь
// летить свіжий статус.
func TestPlayByNumberReachesController(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)

	rec := do(t, h, http.MethodPost, base("play/7/2"))
	if rec.Code != http.StatusOK {
		t.Fatalf("код %d, тіло %q", rec.Code, rec.Body.String())
	}
	if len(c.plays) != 1 || c.plays[0] != [2]int{7, 2} {
		t.Fatalf("plays = %v, очікував [[7 2]]", c.plays)
	}
	if st := decodeStatus(t, rec); st.PositionSec != 43 {
		t.Fatalf("ехо position = %v, очікував свіже 43", st.PositionSec)
	}
}

// Тайтл змінився між GET episodes і тапом: 409 із поясненням, і жодного запиту
// в модель — інакше пульт запустив би серію чужого тайтлу.
func TestPlayWithStaleGenIs409(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)
	c.playlist.Gen = 8 // у застосунку вже інший список

	rec := do(t, h, http.MethodPost, base("play/7/2"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("код %d, очікував 409 (тіло %q)", rec.Code, rec.Body.String())
	}
	if got := decodeErr(t, rec); got != i18n.RemoteNoPlaylist {
		t.Fatalf("error = %q, очікував %q", got, i18n.RemoteNoPlaylist)
	}
	if len(c.plays) != 0 {
		t.Fatalf("застарілий тап дійшов до моделі: %v", c.plays)
	}
}

// Розбір шляху той самий суворий, що й у seek: усе непередбачене — 404, а не
// 400, щоб не підтверджувати вгаданий токен.
func TestPlayPathParsing(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)
	bad := []string{
		"play/7/0", "play/a/2", "play/7", "play//2", "play/7/", "play/",
		"play", "play/7/2/3", "play/7/-2", "play/7/+2", "play/7/2.0",
		"play/1234567/2", "play/7/12345", "play/7/abc", "play/%207/2",
	}
	for _, p := range bad {
		rec := do(t, h, http.MethodPost, base(p))
		if rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("POST %s: %d %q, очікував 404 без тіла", p, rec.Code, rec.Body.String())
		}
	}
	if len(c.plays) != 0 || len(c.calls) != 0 {
		t.Fatalf("сміття дійшло до контролера: plays %v, calls %v", c.plays, c.calls)
	}
	// gen 0 — коректний шлях, але завідомо застарілий список
	if rec := do(t, h, http.MethodPost, base("play/0/2")); rec.Code != http.StatusConflict {
		t.Fatalf("POST play/0/2 = %d, очікував 409", rec.Code)
	}
}

// Чужий токен не має видавати ні списку, ні можливості грати.
func TestPlaylistPathsNeedToken(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)
	foreign := "/r/ffffffffffffffffffffffffffffffff/"
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, foreign + "episodes"},
		{http.MethodPost, foreign + "play/7/2"},
	} {
		rec := do(t, h, tc.method, tc.path)
		if rec.Code != http.StatusNotFound || rec.Body.Len() != 0 {
			t.Errorf("%s %s: %d %q, очікував 404 без тіла", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if len(c.plays) != 0 || len(c.calls) != 0 {
		t.Fatalf("чужий токен дістався контролера: %v %v", c.plays, c.calls)
	}
}

// Методи й CSRF — як на решті маршрутів: список лише читається, тап лише
// постить, і чужий сайт не може ні того, ні того.
func TestPlaylistMethodsAndCSRF(t *testing.T) {
	c := playingCtl()
	c.playlist = testPlaylist()
	h := newTestHandler(t, c)

	if rec := do(t, h, http.MethodPost, base("episodes")); rec.Code != http.StatusMethodNotAllowed ||
		rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("POST episodes = %d (Allow %q)", rec.Code, rec.Header().Get("Allow"))
	}
	if rec := do(t, h, http.MethodGet, base("play/7/2")); rec.Code != http.StatusMethodNotAllowed ||
		rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET play = %d (Allow %q)", rec.Code, rec.Header().Get("Allow"))
	}
	rec := doReq(t, h, http.MethodPost, base("play/7/2"), lanHost, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden || rec.Body.Len() != 0 {
		t.Errorf("чужий origin: %d %q, очікував 403 без тіла", rec.Code, rec.Body.String())
	}
	if len(c.plays) != 0 {
		t.Fatalf("контролер викликано попри 405/403: %v", c.plays)
	}
}
