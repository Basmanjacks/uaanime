package downloadtest_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
)

// Стенд тестує сам себе: ним користуються тести кількох пакетів, і мовчки
// зламана ручка давала б там хибно зелені прогони.

var headers = map[string]string{
	"Referer":         "https://moon.test/",
	"User-Agent":      "uaanime-test",
	"Accept":          "*/*",
	"Accept-Language": "uk-UA",
}

// reply — те, що тестам потрібно з відповіді після закриття тіла; сам
// *http.Response назовні не віддаємо, щоб bodyclose не хибив на викликах.
type reply struct {
	Code     int
	Header   http.Header
	FinalURL string
}

func get(t *testing.T, c *http.Client, url string, withHeaders bool) (reply, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("запит: %v", err)
	}
	if withHeaders {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		t.Fatalf("читання %s: %v", url, err)
	}
	return reply{Code: res.StatusCode, Header: res.Header, FinalURL: res.Request.URL.String()}, body
}

func TestHLSServerServesPlaylistsAndSegments(t *testing.T) {
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Referer: headers["Referer"]})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}

	res, master := get(t, c, s.URL(), true)
	if res.Code != http.StatusOK {
		t.Fatalf("master: HTTP %d", res.Code)
	}
	text := string(master)
	if !strings.HasPrefix(text, "#EXTM3U\n") {
		t.Fatalf("master без магії:\n%s", text)
	}
	if strings.Count(text, "#EXT-X-STREAM-INF") != 3 {
		t.Errorf("очікували три варіанти:\n%s", text)
	}
	if !strings.Contains(text, `CODECS="avc1.64001f,mp4a.40.2"`) {
		t.Errorf("жоден варіант не має CODECS:\n%s", text)
	}
	// Порядок атрибутів навмисно різний, а URL — абсолютний, відносний і від кореня.
	if !strings.Contains(text, "BANDWIDTH=5800000,RESOLUTION=1280x720") &&
		!strings.Contains(text, "RESOLUTION=1280x720,BANDWIDTH=") {
		t.Errorf("не знайдено варіанта з переставленими атрибутами:\n%s", text)
	}
	if !strings.Contains(text, "https://hls.test/q/1080/index.m3u8") ||
		!strings.Contains(text, "\nq/720/index.m3u8") ||
		!strings.Contains(text, "\n/q/480/index.m3u8") {
		t.Errorf("очікували абсолютний, відносний і кореневий URL варіантів:\n%s", text)
	}

	_, media := get(t, c, "https://hls.test/q/720/index.m3u8", true)
	mt := string(media)
	if !strings.Contains(mt, "#EXT-X-VERSION:3") || !strings.Contains(mt, "#EXT-X-ENDLIST") {
		t.Errorf("медіаплейлист неповний:\n%s", mt)
	}
	if got := strings.Count(mt, "#EXTINF:5.0,"); got != 3 {
		t.Errorf("EXTINF = %d, очікували 3:\n%s", got, mt)
	}

	var joined []byte
	for i := range 3 {
		res, seg := get(t, c, "https://hls.test/q/720/segment"+string(rune('0'+i))+".ts", true)
		if res.Code != http.StatusOK {
			t.Fatalf("сегмент %d: HTTP %d", i, res.Code)
		}
		if !bytes.Equal(seg, downloadtest.Segment(i)) {
			t.Fatalf("сегмент %d має інші байти", i)
		}
		joined = append(joined, seg...)
	}
	if !bytes.Equal(joined, s.Expected(720)) {
		t.Error("Expected не збігається зі склейкою сегментів")
	}
	if s.RequestCount() != 5 {
		t.Errorf("RequestCount = %d, очікували 5", s.RequestCount())
	}
	if paths := s.Paths(); len(paths) != 5 || paths[0] != "/master.m3u8" {
		t.Errorf("Paths = %v", paths)
	}
}

func TestHLSServerRequiresStreamHeaders(t *testing.T) {
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Referer: headers["Referer"]})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}
	res, _ := get(t, c, s.URL(), false)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("HTTP %d, очікували 400 без заголовків потоку", res.Code)
	}
}

func TestHLSSegmentKnobs(t *testing.T) {
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 2})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}

	s.FailSegment(0, 2, http.StatusServiceUnavailable)
	for i := range 2 {
		if res, _ := get(t, c, "https://hls.test/q/720/segment0.ts", true); res.Code != http.StatusServiceUnavailable {
			t.Fatalf("спроба %d: HTTP %d, очікували 503", i, res.Code)
		}
	}
	if res, body := get(t, c, "https://hls.test/q/720/segment0.ts", true); res.Code != http.StatusOK || !bytes.Equal(body, downloadtest.Segment(0)) {
		t.Fatalf("третя спроба: HTTP %d, %d байт", res.Code, len(body))
	}

	s.TruncateSegment(1)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://hls.test/q/720/segment1.ts", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("обрізаний сегмент: %v", err)
	}
	_, err = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err == nil {
		t.Error("очікували обрив на обрізаному сегменті")
	}

	s.SlowSegment(0, 50*time.Millisecond)
	start := time.Now()
	get(t, c, "https://hls.test/q/720/segment0.ts", true)
	if time.Since(start) < 50*time.Millisecond {
		t.Error("SlowSegment не затримав відповідь")
	}
}

func TestHLSBlockAndStall(t *testing.T) {
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Segments: 2})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites()), Timeout: 300 * time.Millisecond}

	release := make(chan struct{})
	s.BlockSegment(0, release)
	done := make(chan struct{})
	go func() {
		defer close(done)
		get(t, c, "https://hls.test/q/720/segment0.ts", true)
	}()
	select {
	case <-done:
		t.Fatal("сегмент віддано, хоча його заблоковано")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("сегмент не відпустили після release")
	}

	// StallSegment: заголовки є, тіла немає — клієнт мусить впертися у свій
	// тайм-аут, а стенд — закритися без зависання (t.Cleanup відпускає обробник).
	s.StallSegment(1)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://hls.test/q/720/segment1.ts", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err == nil {
		_, err = io.ReadAll(res.Body)
		_ = res.Body.Close()
	}
	if err == nil {
		t.Error("очікували тайм-аут на зависному сегменті")
	}
}

func TestFileServerRedirectsAndHonoursRange(t *testing.T) {
	payload := make([]byte, 1000)
	for i := range payload {
		payload[i] = byte(i)
	}
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{Referer: headers["Referer"]})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}

	first, body := get(t, c, s.URL(), true)
	if first.Code != http.StatusOK || !bytes.Equal(body, payload) {
		t.Fatalf("HTTP %d, %d байт", first.Code, len(body))
	}
	if first.FinalURL != s.FileURL() {
		t.Errorf("фінальний URL = %s, очікували %s", first.FinalURL, s.FileURL())
	}
	if first.Header.Get("ETag") != s.ETag() || first.Header.Get("Accept-Ranges") != "bytes" {
		t.Errorf("заголовки файла: %v", first.Header)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL(), nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", "bytes=400-")
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	part, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || !bytes.Equal(part, payload[400:]) {
		t.Fatalf("HTTP %d, %d байт", res.StatusCode, len(part))
	}
	if got, want := res.Header.Get("Content-Range"), "bytes 400-999/1000"; got != want {
		t.Errorf("Content-Range = %q, очікували %q", got, want)
	}

	// За кінцем файла — 416 з «bytes */N».
	req.Header.Set("Range", "bytes=1000-")
	res, err = c.Do(req)
	if err != nil {
		t.Fatalf("416: %v", err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusRequestedRangeNotSatisfiable || res.Header.Get("Content-Range") != "bytes */1000" {
		t.Fatalf("HTTP %d, Content-Range %q", res.StatusCode, res.Header.Get("Content-Range"))
	}
}

func TestFileServerIgnoreRangeAndReplace(t *testing.T) {
	payload := bytes.Repeat([]byte{1}, 500)
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{IgnoreRange: true})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL(), nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", "bytes=100-")
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || len(body) != len(payload) {
		t.Fatalf("HTTP %d, %d байт — Range мав бути проігнорований", res.StatusCode, len(body))
	}

	old := s.ETag()
	s.Replace(bytes.Repeat([]byte{2}, 500))
	if s.ETag() == old {
		t.Error("Replace не змінив ETag")
	}
	// Рахуються лише запити до CDN; 302 першого сервера в лічильник не йде.
	if s.RequestCount() != 1 {
		t.Errorf("RequestCount = %d, очікували 1", s.RequestCount())
	}
}

func TestFileServerCutAfter(t *testing.T) {
	payload := bytes.Repeat([]byte{7}, 4000)
	s := downloadtest.NewFile(t, payload, downloadtest.FileOptions{})
	c := &http.Client{Transport: downloadtest.Transport(s.Rewrites())}

	s.CutAfter(100)
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, s.URL(), nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_, err = io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err == nil {
		t.Fatal("очікували обрив з'єднання після CutAfter")
	}
	// Ручка одноразова: наступна відповідь повна.
	if _, body := get(t, c, s.URL(), true); len(body) != len(payload) {
		t.Errorf("друга відповідь = %d байт, очікували %d", len(body), len(payload))
	}
}

func TestTransportRejectsUnknownHost(t *testing.T) {
	c := &http.Client{Transport: downloadtest.Transport(map[string]string{})}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://unknown.test/x", nil)
	if res, err := c.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatal("очікували помилку для непідставленого хоста")
	}
}
