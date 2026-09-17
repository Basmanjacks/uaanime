package httpx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/errs"
)

// streamHeaders — те, що екстрактор віддає разом із потоком; moonanime без них
// відповідає 400 на кожному хості, включно з CDN після редиректу.
var streamHeaders = map[string]string{
	"Referer":         "https://moon.test/",
	"User-Agent":      UserAgent,
	"Accept":          "*/*",
	"Accept-Language": "uk-UA",
}

// stub піднімає один сервер під фейковим іменем host і повертає Fetcher, що
// ходить туди. httptest слухає 127.0.0.1, тому без підстановки імені URL не
// пройшов би ні ValidateURL, ні анти-SSRF dialer.
func stub(t *testing.T, host string, h http.HandlerFunc) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewFetcher(downloadtest.Transport(map[string]string{host: srv.URL}))
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		"http://cdn.test/a.webm",      // не https
		"https://127.0.0.1/a.webm",    // IP-літерал
		"https://[::1]/a.webm",        // IPv6-літерал
		"https://localhost/a.webm",    // локальний
		"https://cdn/a.webm",          // одна мітка
		"https://cdn.1/a.webm",        // нealфавітний TLD
		"https://cdn..test/a.webm",    // порожня мітка
		"ftp://cdn.test/a.webm",       // чужа схема
		"https://cdn.t/a.webm",        // закороткий TLD
		"https://my.localhost/a.webm", // піддомен localhost
	}
	for _, raw := range bad {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) = nil, очікували відмову", raw)
		} else if !errors.Is(err, errs.ErrProvider) {
			t.Errorf("ValidateURL(%q) = %v, очікували ErrProvider", raw, err)
		}
	}
	good := []string{"https://s1.mooncdn.online/a.webm", "https://hls.test/master.m3u8", "https://CDN.Test./a.webm"}
	for _, raw := range good {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("ValidateURL(%q) = %v, очікували nil", raw, err)
		}
	}
}

// ------------------------------------------------------------------- Probe

func TestProbePlaylistVia200(t *testing.T) {
	// Стенд HLS віддає плейлист повним тілом, ігноруючи Range, — так поводиться
	// більшість CDN на m3u8.
	hls := downloadtest.NewHLS(t, downloadtest.HLSOptions{})
	f := NewFetcher(downloadtest.Transport(hls.Rewrites()))

	p, err := f.Probe(t.Context(), hls.URL(), streamHeaders)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.Kind != ProbePlaylist {
		t.Fatalf("Kind = %v, очікували плейлист", p.Kind)
	}
	if !bytes.Contains(p.Body, []byte("#EXT-X-STREAM-INF")) {
		t.Errorf("тіло не схоже на master:\n%s", p.Body)
	}
	if p.FinalURL != hls.URL() {
		t.Errorf("FinalURL = %q, очікували %q", p.FinalURL, hls.URL())
	}
}

func TestProbePlaylistVia206(t *testing.T) {
	const body = "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-ENDLIST\n"
	f := stub(t, "hls.test", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			t.Error("Probe не надіслав Range")
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, body)
	})
	p, err := f.Probe(t.Context(), "https://hls.test/index.m3u8", streamHeaders)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.Kind != ProbePlaylist || string(p.Body) != body {
		t.Fatalf("Kind = %v, тіло = %q", p.Kind, p.Body)
	}
}

func TestProbeRejectsHugePlaylist(t *testing.T) {
	f := stub(t, "hls.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", probeWindow-1, 4<<20))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(append([]byte("#EXTM3U\n"), bytes.Repeat([]byte("#EXTINF:5.0,\nx.ts\n"), 1000)...))
	})
	_, err := f.Probe(t.Context(), "https://hls.test/index.m3u8", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestProbeFileVia206FollowsCrossHostRedirect(t *testing.T) {
	payload := bytes.Repeat([]byte{7}, 5000)
	// Referer вимагається дослівно на обох хостах: Go на крос-хостовому
	// редиректі підставляє власний Referer, тож без переграних заголовків
	// потоку CDN відповів би 400.
	file := downloadtest.NewFile(t, payload, downloadtest.FileOptions{Referer: streamHeaders["Referer"]})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))

	p, err := f.Probe(t.Context(), file.URL(), streamHeaders)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if p.Kind != ProbeFile {
		t.Fatalf("Kind = %v, очікували файл", p.Kind)
	}
	if p.Total != int64(len(payload)) {
		t.Errorf("Total = %d, очікували %d", p.Total, len(payload))
	}
	if p.FinalURL != file.FileURL() {
		t.Errorf("FinalURL = %q, очікували %q (крос-хостовий 302 має бути пройдений)", p.FinalURL, file.FileURL())
	}
	if p.ContentType != "video/webm" {
		t.Errorf("ContentType = %q", p.ContentType)
	}
	if p.Validator.ETag != file.ETag() {
		t.Errorf("ETag = %q, очікували %q", p.Validator.ETag, file.ETag())
	}
	if p.Body != nil {
		t.Errorf("для файла тіло зберігати не треба, маємо %d байт", len(p.Body))
	}
}

// Обидва сервери стенда вимагають Referer/UA/Accept/Accept-Language. Якщо
// заголовки не переграти на другому хості, CDN відповість 400 — саме це й
// перевіряє попередній тест; тут перевіряємо зворотне: без заголовків 400 є.
func TestProbeFailsWithoutStreamHeaders(t *testing.T) {
	file := downloadtest.NewFile(t, []byte("abc"), downloadtest.FileOptions{})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))
	_, err := f.Probe(t.Context(), file.URL(), map[string]string{"User-Agent": UserAgent})
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestProbeFileVia200DoesNotDrainBody(t *testing.T) {
	const declared = 200 << 20
	var mu sync.Mutex
	written := 0
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/webm")
		w.Header().Set("Content-Length", strconv.Itoa(declared))
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		for n := 0; n < declared; n += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			mu.Lock()
			written += len(chunk)
			mu.Unlock()
		}
	})

	start := time.Now()
	p, err := f.Probe(t.Context(), "https://cdn.test/big.webm", streamHeaders)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Probe тривав %s — схоже, тіло читалося цілком", elapsed)
	}
	if p.Kind != ProbeFile || p.Total != declared {
		t.Fatalf("Kind = %v, Total = %d, очікували файл на %d", p.Kind, p.Total, int64(declared))
	}
	mu.Lock()
	got := written
	mu.Unlock()
	if got >= 32<<20 {
		t.Errorf("сервер віддав %d байт — Probe дочитує велике тіло", got)
	}
}

func TestProbeRejectsOversizedFile(t *testing.T) {
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-3/%d", int64(MaxFileBytes)+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("\x1aE\xdf\xa3"))
	})
	_, err := f.Probe(t.Context(), "https://cdn.test/huge.webm", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestProbeClassifiesServerError(t *testing.T) {
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	_, err := f.Probe(t.Context(), "https://cdn.test/a.webm", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
	if errors.Is(err, errs.ErrOffline) {
		t.Errorf("err = %v: 500 — це не офлайн", err)
	}
}

func TestProbeClassifiesTransportFailureAsOffline(t *testing.T) {
	f := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("network is unreachable")}
	}))
	_, err := f.Probe(t.Context(), "https://cdn.test/a.webm", streamHeaders)
	if !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("err = %v, очікували ErrOffline", err)
	}
}

// ---------------------------------------------------------------- редиректи

func TestRedirectToHTTPRefused(t *testing.T) {
	f := stub(t, "v.test", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://cdn.test/file.webm", http.StatusFound)
	})
	_, err := f.Probe(t.Context(), "https://v.test/v/1080/", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
	if !strings.Contains(err.Error(), "редирект") {
		t.Errorf("err = %v, очікували згадку редиректу", err)
	}
}

func TestRedirectToIPLiteralRefused(t *testing.T) {
	f := stub(t, "v.test", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	_, err := f.Probe(t.Context(), "https://v.test/v/1080/", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestRedirectLoopStopsAtLimit(t *testing.T) {
	f := stub(t, "v.test", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://v.test"+r.URL.Path+"x", http.StatusFound)
	})
	_, err := f.Probe(t.Context(), "https://v.test/a", streamHeaders)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

// -------------------------------------------------------------------- Open

func TestOpenFromZeroReturnsWholeFile(t *testing.T) {
	payload := bytes.Repeat([]byte{3}, 2048)
	file := downloadtest.NewFile(t, payload, downloadtest.FileOptions{})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))

	res, err := f.Open(t.Context(), file.URL(), streamHeaders, 0, 0, Validator{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Ranged {
		t.Error("Ranged = true при from = 0")
	}
	if res.Total != int64(len(payload)) {
		t.Errorf("Total = %d, очікували %d", res.Total, len(payload))
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("читання: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Error("тіло не збіглося з файлом")
	}
	if res.Validator.ETag != file.ETag() {
		t.Errorf("ETag = %q", res.Validator.ETag)
	}
}

func TestOpenResumeAcceptsCorrectRange(t *testing.T) {
	payload := bytes.Repeat([]byte{9}, 3000)
	for i := range payload {
		payload[i] = byte(i)
	}
	file := downloadtest.NewFile(t, payload, downloadtest.FileOptions{})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))

	const from = 1000
	res, err := f.Open(t.Context(), file.URL(), streamHeaders, from, int64(len(payload)), Validator{ETag: file.ETag()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if !res.Ranged {
		t.Fatal("Ranged = false, очікували продовження")
	}
	if res.Total != int64(len(payload)) {
		t.Errorf("Total = %d, очікували %d", res.Total, len(payload))
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("читання: %v", err)
	}
	if !bytes.Equal(got, payload[from:]) {
		t.Error("хвіст не збігся")
	}
}

func TestOpenSendsIfRangeOnlyWithValidator(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("If-Range"))
		mu.Unlock()
		w.Header().Set("Content-Range", "bytes 10-19/20")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(bytes.Repeat([]byte{1}, 10))
	})
	for _, want := range []Validator{{ETag: `"abc"`}, {}, {LastModified: "Wed, 21 Oct 2026 07:28:00 GMT"}} {
		res, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 10, 20, want)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("запитів %d, очікували 3", len(seen))
	}
	if seen[0] != `"abc"` {
		t.Errorf("If-Range = %q, очікували ETag", seen[0])
	}
	if seen[1] != "" {
		t.Errorf("If-Range = %q без валідатора — мав бути відсутній", seen[1])
	}
	if seen[2] != "Wed, 21 Oct 2026 07:28:00 GMT" {
		t.Errorf("If-Range = %q, очікували Last-Modified", seen[2])
	}
}

// Сервер проігнорував Range і віддав 200 — тіло справді з нуля, тож Ranged=false
// і викликач переписує файл наново.
func TestOpenResumeFallsBackWhenServerIgnoresRange(t *testing.T) {
	payload := bytes.Repeat([]byte{5}, 1024)
	file := downloadtest.NewFile(t, payload, downloadtest.FileOptions{IgnoreRange: true})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))

	res, err := f.Open(t.Context(), file.URL(), streamHeaders, 512, int64(len(payload)), Validator{ETag: file.ETag()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Ranged {
		t.Fatal("Ranged = true на відповідь 200")
	}
	got, _ := io.ReadAll(res.Body)
	if !bytes.Equal(got, payload) {
		t.Error("очікували повний файл з нуля")
	}
}

// Змінився файл — ETag інший, сервер на If-Range відповідає 200.
func TestOpenResumeRestartsWhenValidatorChanged(t *testing.T) {
	payload := bytes.Repeat([]byte{1}, 1024)
	file := downloadtest.NewFile(t, payload, downloadtest.FileOptions{})
	f := NewFetcher(downloadtest.Transport(file.Rewrites()))
	stale := file.ETag()
	updated := bytes.Repeat([]byte{2}, 1024)
	file.Replace(updated)

	res, err := f.Open(t.Context(), file.URL(), streamHeaders, 512, int64(len(payload)), Validator{ETag: stale})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.Ranged {
		t.Fatal("Ranged = true попри зміну файла")
	}
	got, _ := io.ReadAll(res.Body)
	if !bytes.Equal(got, updated) {
		t.Error("очікували новий файл цілком")
	}
}

// 206 не з тієї позиції віддавати як продовження не можна; Open мовчки
// перевідкриває файл з нуля, щоб Ranged=false завжди означало «тіло з нуля».
func TestOpenResumeRestartsOnWrongRange(t *testing.T) {
	full := bytes.Repeat([]byte{4}, 600)
	cases := map[string]string{
		"інший старт": "bytes 0-599/600",
		"інший total": "bytes 300-899/900",
	}
	for name, cr := range cases {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			n := 0
			f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				n++
				first := n == 1
				mu.Unlock()
				if first {
					w.Header().Set("Content-Range", cr)
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(bytes.Repeat([]byte{8}, 100))
					return
				}
				if r.Header.Get("Range") != "" {
					t.Error("повторний запит мав іти без Range")
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(full)))
				_, _ = w.Write(full)
			})
			res, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 300, 600, Validator{})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.Ranged {
				t.Fatal("Ranged = true на невідповідний 206")
			}
			got, _ := io.ReadAll(res.Body)
			if !bytes.Equal(got, full) {
				t.Errorf("очікували повний файл, отримали %d байт", len(got))
			}
		})
	}
}

func TestOpenTreatsSatisfied416AsComplete(t *testing.T) {
	const size = 700
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	})
	res, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, size, size, Validator{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if !res.Ranged || res.Total != size {
		t.Fatalf("Ranged = %v, Total = %d", res.Ranged, res.Total)
	}
	got, _ := io.ReadAll(res.Body)
	if len(got) != 0 {
		t.Errorf("тіло = %d байт, очікували порожнє", len(got))
	}
}

func TestOpenRejectsBare416(t *testing.T) {
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	})
	_, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 700, 700, Validator{})
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestOpenRejects416WithOtherTotal(t *testing.T) {
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes */900")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	})
	_, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 700, 700, Validator{})
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestOpenClassifiesErrors(t *testing.T) {
	f := stub(t, "cdn.test", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusForbidden)
	})
	if _, err := f.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 0, 0, Validator{}); !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}

	off := NewFetcher(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}
	}))
	if _, err := off.Open(t.Context(), "https://cdn.test/a.webm", streamHeaders, 10, 20, Validator{}); !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("err = %v, очікували ErrOffline", err)
	}
}

func TestParseContentRange(t *testing.T) {
	cases := []struct {
		in    string
		ok    bool
		start int64
		total int64
		unsat bool
	}{
		{"bytes 0-1048575/205000000", true, 0, 205000000, false},
		{"bytes 100-199/*", true, 100, -1, false},
		{"bytes */700", true, -1, 700, true},
		{"bytes 200-100/300", false, 0, 0, false},
		{"items 0-1/2", false, 0, 0, false},
		{"", false, 0, 0, false},
		{"bytes 0-1", false, 0, 0, false},
	}
	for _, c := range cases {
		cr, ok := parseContentRange(c.in)
		if ok != c.ok {
			t.Errorf("parseContentRange(%q) ok = %v", c.in, ok)
			continue
		}
		if !ok {
			continue
		}
		if cr.Total != c.total || cr.Unsatisfied != c.unsat || (!c.unsat && cr.Start != c.start) {
			t.Errorf("parseContentRange(%q) = %+v", c.in, cr)
		}
	}
}

func TestValidatorIgnoresWeakETag(t *testing.T) {
	h := http.Header{}
	h.Set("ETag", `W/"weak"`)
	h.Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")
	v := validatorOf(h)
	if v.ETag != "" {
		t.Errorf("слабкий ETag збережено: %q", v.ETag)
	}
	if v.IsZero() {
		t.Error("Last-Modified мав лишитися")
	}
	if v.ifRange() != h.Get("Last-Modified") {
		t.Errorf("ifRange = %q", v.ifRange())
	}
}
