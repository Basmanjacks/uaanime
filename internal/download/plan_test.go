package download

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
)

// streamHeaders — те, що вимагає стенд (і живий moonanime) на кожному запиті.
var streamHeaders = map[string]string{
	"Referer":         "https://moon.test/",
	"User-Agent":      "uaanime-test",
	"Accept":          "*/*",
	"Accept-Language": "uk-UA",
}

func hlsStream(url string) extractor.Stream {
	return extractor.Stream{URL: url, Headers: streamHeaders}
}

func TestBuildPlanHLSMaster(t *testing.T) {
	s := downloadtest.NewHLS(t, downloadtest.HLSOptions{Referer: streamHeaders["Referer"]})
	f := NewFetcher(downloadtest.Transport(s.Rewrites()))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{hlsStream(s.URL())})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Kind != KindHLS || p.Ext != ".ts" {
		t.Fatalf("Kind = %v, Ext = %q", p.Kind, p.Ext)
	}
	if got := p.Heights(); len(got) != 3 || got[0] != 1080 || got[1] != 720 || got[2] != 480 {
		t.Fatalf("висоти = %v, очікували 1080/720/480 за спаданням", got)
	}
	for i, q := range p.Qualities {
		if q.Segments != 3 || q.DurationSec != 3*downloadtest.SegmentDuration {
			t.Errorf("якість %d: Segments = %d, DurationSec = %v", i, q.Segments, q.DurationSec)
		}
		if q.Exact {
			t.Errorf("якість %d: HLS-оцінка не може бути Exact", i)
		}
		if want := int64(float64(q.Bandwidth) / 8 * q.DurationSec); q.Bytes != want || q.Bytes == 0 {
			t.Errorf("якість %d: Bytes = %d, очікували %d (bandwidth × тривалість)", i, q.Bytes, want)
		}
		if len(q.media) != 3 {
			t.Errorf("якість %d: сегменти не збережено в плані (%d)", i, len(q.media))
		}
		if q.Headers["Referer"] != streamHeaders["Referer"] {
			t.Errorf("якість %d: заголовки потоку загублено", i)
		}
	}
}

// Один медіаплейлист замість майстра — так виглядає потік без варіантів:
// висота відома лише зі Stream.Quality.
func TestBuildPlanHLSMediaOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:5.0,\nseg0.ts\n#EXTINF:5.0,\nseg1.ts\n#EXT-X-ENDLIST\n")
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(downloadtest.Transport(map[string]string{"one.test": srv.URL}))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{{URL: "https://one.test/index.m3u8", Quality: 720}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Qualities) != 1 || p.Qualities[0].Height != 720 || p.Qualities[0].Segments != 2 {
		t.Fatalf("якості = %+v", p.Qualities)
	}
	if p.Qualities[0].Bytes != 0 {
		t.Errorf("без BANDWIDTH оцінки розміру бути не може, а Bytes = %d", p.Qualities[0].Bytes)
	}
}

// Прямий файл із 302 на інший хост (moonanime Glass Moon): розмір точний,
// розширення — з фінального URL, висота — з шляху початкового.
func TestBuildPlanDirectFile(t *testing.T) {
	payload := strings.Repeat("v", 5000)
	s := downloadtest.NewFile(t, []byte(payload), downloadtest.FileOptions{Referer: streamHeaders["Referer"]})
	f := NewFetcher(downloadtest.Transport(s.Rewrites()))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{{URL: s.URL(), Headers: streamHeaders}})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Kind != KindFile || p.Ext != ".webm" {
		t.Fatalf("Kind = %v, Ext = %q", p.Kind, p.Ext)
	}
	q := p.Qualities[0]
	if q.Height != 1080 || q.Bytes != int64(len(payload)) || !q.Exact {
		t.Fatalf("якість = %+v", q)
	}
	if q.URL != s.URL() {
		t.Errorf("URL = %q, очікували початковий (підписаний), а не CDN-ний", q.URL)
	}
}

// Кілька прямих потоків — по якості на потік; висота береться зі шляху там,
// де екстрактор її не дав.
func TestBuildPlanDirectFilesPerStream(t *testing.T) {
	bodies := map[string]string{"1080": strings.Repeat("a", 9000), "720": strings.Repeat("b", 4000)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h, ok := strings.CutPrefix(r.URL.Path, "/v/"); ok {
			http.Redirect(w, r, "/media/"+strings.TrimSuffix(h, "/")+".webm", http.StatusFound)
			return
		}
		name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/media/"), ".webm")
		body, ok := bodies[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/webm")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(downloadtest.Transport(map[string]string{"files.test": srv.URL}))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{
		{URL: "https://files.test/v/720/"},
		{URL: "https://files.test/v/1080/"},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(p.Qualities) != 2 || p.Qualities[0].Height != 1080 || p.Qualities[1].Height != 720 {
		t.Fatalf("якості = %+v", p.Qualities)
	}
	if p.Qualities[0].Bytes != 9000 || p.Qualities[1].Bytes != 4000 {
		t.Errorf("розміри = %d/%d", p.Qualities[0].Bytes, p.Qualities[1].Bytes)
	}
	for _, q := range p.Qualities {
		if !q.Exact {
			t.Errorf("розмір прямого файла названий сервером — Exact має бути true: %+v", q)
		}
	}
}

// masterServer — майстер із двох варіантів, де частина медіаплейлистів
// відповідає помилкою. Стенд downloadtest такої ручки не має навмисно:
// поломка конкретного варіанта цікава лише плану.
func masterServer(t *testing.T, broken map[int]int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/master.m3u8" {
			var b strings.Builder
			b.WriteString("#EXTM3U\n")
			for _, h := range []int{1080, 720} {
				fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d\nq/%d/index.m3u8\n", h*4000, h*16/9, h, h)
			}
			_, _ = fmt.Fprint(w, b.String())
			return
		}
		h := 0
		_, _ = fmt.Sscanf(r.URL.Path, "/q/%d/index.m3u8", &h)
		if code, ok := broken[h]; ok {
			http.Error(w, "broken", code)
			return
		}
		_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg0.ts\n#EXT-X-ENDLIST\n")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBuildPlanDropsBrokenVariant(t *testing.T) {
	srv := masterServer(t, map[int]int{720: http.StatusInternalServerError})
	f := NewFetcher(downloadtest.Transport(map[string]string{"m.test": srv.URL}))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{hlsStream("https://m.test/master.m3u8")})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if got := p.Heights(); len(got) != 1 || got[0] != 1080 {
		t.Fatalf("висоти = %v, очікували лише 1080", got)
	}
}

func TestBuildPlanAllVariantsBroken(t *testing.T) {
	srv := masterServer(t, map[int]int{1080: http.StatusInternalServerError, 720: http.StatusNotFound})
	f := NewFetcher(downloadtest.Transport(map[string]string{"m.test": srv.URL}))

	_, err := BuildPlan(t.Context(), f, []extractor.Stream{hlsStream("https://m.test/master.m3u8")})
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

func TestBuildPlanRejectsInvalidStreamURL(t *testing.T) {
	f := NewFetcher(downloadtest.Transport(nil))
	_, err := BuildPlan(t.Context(), f, []extractor.Stream{{URL: "http://127.0.0.1/evil.m3u8"}})
	if !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("err = %v, очікували ErrNoStream", err)
	}
}

// Змішаний набір: HLS виграє, бо один плейлист дає всі якості.
func TestBuildPlanPrefersHLSOverDirectFile(t *testing.T) {
	hls := downloadtest.NewHLS(t, downloadtest.HLSOptions{})
	file := downloadtest.NewFile(t, []byte("xxxx"), downloadtest.FileOptions{})
	rw := hls.Rewrites()
	for k, v := range file.Rewrites() {
		rw[k] = v
	}
	f := NewFetcher(downloadtest.Transport(rw))

	p, err := BuildPlan(t.Context(), f, []extractor.Stream{
		{URL: file.URL(), Headers: streamHeaders},
		{URL: hls.URL(), Headers: streamHeaders},
	})
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if p.Kind != KindHLS {
		t.Fatalf("Kind = %v, очікували HLS", p.Kind)
	}
}

func TestPlanPick(t *testing.T) {
	full := &Plan{Qualities: []Quality{{Height: 1080}, {Height: 720}, {Height: 480}}}
	auto := &Plan{Qualities: []Quality{{Height: 0}}}
	cases := []struct {
		name string
		p    *Plan
		ask  int
		want int
		ok   bool
	}{
		{"найкраща за замовчуванням", full, 0, 1080, true},
		{"точний збіг", full, 720, 720, true},
		{"немає 900 — беремо нижчу 720", full, 900, 720, true},
		{"немає 360 — беремо вищу 480", full, 360, 480, true},
		{"вище за все — найвища", full, 4320, 1080, true},
		{"лише авто", auto, 1080, 0, true},
		{"порожній план", &Plan{}, 0, 0, false},
		{"nil-план", nil, 720, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, ok := c.p.Pick(c.ask)
			if ok != c.ok || q.Height != c.want {
				t.Fatalf("Pick(%d) = %d, %v; очікували %d, %v", c.ask, q.Height, ok, c.want, c.ok)
			}
		})
	}
}
