// Package extractortest — спільні contract-тести для всіх екстракторів,
// рідний брат providertest. Коли ламається відеохост, страждають усі провайдери
// одразу, тому кожен екстрактор мусить однаково поводитись у чотирьох ситуаціях:
// жива фікстура, мережі немає, хост віддав помилку, сторінка без потоку.
// Саморобні тести різної якості давали різне покриття цих гілок у трьох пакетах.
package extractortest

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

// referer — умовний сайт-каталог: фікстурному транспорту він байдужий, а
// адреса реального сайту поза internal/provider не належить (правило 1).
const referer = "https://catalog.invalid/"

// Case — канонічні входи одного екстрактора.
type Case struct {
	Host        string // точний хост, який екстрактор обробляє
	Embed       string // канонічний embed-URL (https://<Host>/…)
	FixtureDir  string // каталог із embed.html
	WantStreams int    // очікувана кількість потоків; 0 = «щонайменше один»
	// WantHeader — заголовки, які мусить нести КОЖЕН потік (плеєр отримає лише їх).
	WantHeader map[string]string
	// ExtraFixtureCheck — перевірки, специфічні для хоста (точний URL, якості).
	ExtraFixtureCheck func(t *testing.T, streams []extractor.Stream)
}

// Run проганяє контракт. Готовий екстрактор ховає свій HTTP-клієнт, тому
// harness будує новий екстрактор під кожен сценарій — із власним RoundTripper.
func Run(t *testing.T, newExtractor func(*http.Client) extractor.Extractor, c Case) {
	t.Helper()

	t.Run("handles", func(t *testing.T) {
		e := newExtractor(nil)
		if !e.Handles(c.Embed) {
			t.Errorf("Handles(%q) = false, має обробляти власний хост", c.Embed)
		}
		// Усе це — форми, якими недовірений embed із розмітки провайдера
		// намагається виглядати «своїм»: незахищена схема, піддомен, інший порт.
		for _, embed := range []string{
			"http://" + c.Host + "/x",
			"https://x." + c.Host + "/x",
			"https://" + c.Host + ":8443/x",
			"https://other.invalid/x",
		} {
			if e.Handles(embed) {
				t.Errorf("Handles(%q) = true, не має обробляти", embed)
			}
		}
	})

	t.Run("fixture", func(t *testing.T) {
		e := newExtractor(httpx.NewClient(extractor.FixtureTransport(c.Host, c.FixtureDir)))
		streams, err := e.Extract(t.Context(), c.Embed, referer)
		if err != nil {
			t.Fatalf("Extract: %v", err)
		}
		switch {
		case c.WantStreams > 0 && len(streams) != c.WantStreams:
			t.Fatalf("len(streams) = %d, want %d", len(streams), c.WantStreams)
		case c.WantStreams == 0 && len(streams) == 0:
			t.Fatal("Extract: порожній список потоків на канонічній фікстурі")
		}
		for i, s := range streams {
			// Той самий рубіж, що і в playback: у потоці не має бути URL,
			// яким недовірений embed скерував би плеєр у локальну мережу.
			if !extractor.ValidStreamURL(s.URL) {
				t.Errorf("streams[%d].URL = %q не проходить ValidStreamURL", i, s.URL)
			}
			for k, want := range c.WantHeader {
				if got := s.Headers[k]; got != want {
					t.Errorf("streams[%d].Headers[%s] = %q, want %q", i, k, got, want)
				}
			}
		}
		if c.ExtraFixtureCheck != nil {
			c.ExtraFixtureCheck(t, streams)
		}
	})

	// Три класи збоїв, які UI показує різними повідомленнями (жорстке правило 6).
	t.Run("offline", func(t *testing.T) {
		e := newExtractor(clientOf(func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		}))
		if _, err := e.Extract(t.Context(), c.Embed, referer); !errors.Is(err, errs.ErrOffline) {
			t.Fatalf("Extract error = %v, want ErrOffline", err)
		}
	})

	t.Run("http-503", func(t *testing.T) {
		e := newExtractor(clientOf(func(*http.Request) (*http.Response, error) {
			return response(http.StatusServiceUnavailable, ""), nil
		}))
		if _, err := e.Extract(t.Context(), c.Embed, referer); !errors.Is(err, errs.ErrProvider) {
			t.Fatalf("Extract error = %v, want ErrProvider", err)
		}
	})

	t.Run("empty-body", func(t *testing.T) {
		e := newExtractor(clientOf(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, ""), nil
		}))
		if _, err := e.Extract(t.Context(), c.Embed, referer); !errors.Is(err, errs.ErrNoStream) {
			t.Fatalf("Extract error = %v, want ErrNoStream", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// clientOf — той самий httpx.NewClient, що й у бою: перевіряємо екстрактор
// разом із його справжніми правилами редиректів і таймаутом.
func clientOf(f roundTripFunc) *http.Client { return httpx.NewClient(f) }

func response(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}
