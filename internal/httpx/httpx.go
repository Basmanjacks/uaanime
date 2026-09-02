// Package httpx — спільний HTTP-клієнт застосунку.
//
// Ідентичність клієнта наскрізна: той самий User-Agent використовується провайдерами,
// екстракторами і передається плеєру. Розбіжність UA між екстракцією і відтворенням —
// типова причина відмови відеохостів.
package httpx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7; rv:128.0) Gecko/20100101 Firefox/128.0"

// MaxBody — стеля читання тіла відповіді. Найбільша реальна сторінка (тайтл
// anitube) — 184 КБ, embed відеохоста — до 50 КБ; усе, що більше за 4 МБ, це
// або не той документ, або спроба вичерпати пам'ять недовіреним хостом.
const MaxBody = 4 << 20

func NewClient(rt http.RoundTripper) *http.Client {
	if rt == nil {
		// Бейджі домівки ходять чотирма воркерами на один хост; дефолтні
		// MaxIdleConnsPerHost=2 змушували б їх щоразу робити зайвий TLS-handshake.
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConnsPerHost = 8
		rt = tr
	}
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: rt,
		// Недовірені сайти не можуть скерувати клієнт на інший хост або незахищений HTTP.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Scheme != "https" || req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("httpx: редирект на %s заборонено: %w", req.URL.Redacted(), errs.ErrProvider)
			}
			return nil
		},
	}
}

// Do виконує запит і повертає тіло, класифікуючи збої на три класи, які
// розрізняє UI: офлайн, зламане джерело, відсутність потоку (останній —
// справа викликача). Єдина точка читання HTTP-відповіді в застосунку:
// провайдери й екстрактори не мають дублювати цю класифікацію.
func Do(client *http.Client, req *http.Request) ([]byte, error) {
	res, err := client.Do(req)
	if err != nil {
		if errs.Offline(err) {
			return nil, fmt.Errorf("%s: %w: %w", req.URL, errs.ErrOffline, err)
		}
		return nil, fmt.Errorf("%s: %w: %w", req.URL, errs.ErrProvider, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %w", req.URL, res.StatusCode, errs.ErrProvider)
	}
	// +1 байт понад ліміт: рівно MaxBody не відрізнити від обрізаного більшого тіла.
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
	if err != nil {
		if errs.Offline(err) {
			return nil, fmt.Errorf("%s: читання відповіді: %w: %w", req.URL, errs.ErrOffline, err)
		}
		return nil, fmt.Errorf("%s: читання відповіді: %w: %w", req.URL, errs.ErrProvider, err)
	}
	if len(body) > MaxBody {
		return nil, fmt.Errorf("%s: відповідь завелика (> %d байт): %w", req.URL, MaxBody, errs.ErrProvider)
	}
	return body, nil
}

// ErrSkip повертає фікстурний RoundTripper, якщо запит не з його домену —
// MultiTransport тоді пробує наступний.
var ErrSkip = errors.New("httpx: транспорт не обробляє цей запит")

// MultiTransport перебирає транспорти по черзі; жодного мережевого fallback немає —
// у фікстурному режимі невідомий запит є помилкою, а не приводом піти в мережу.
type MultiTransport []http.RoundTripper

func (m MultiTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for _, rt := range m {
		res, err := rt.RoundTrip(req)
		if errors.Is(err, ErrSkip) {
			continue
		}
		return res, err
	}
	return nil, errors.New("httpx: жоден фікстурний транспорт не обробив " + req.URL.String())
}
