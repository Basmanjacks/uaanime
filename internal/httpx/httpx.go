// Package httpx — спільний HTTP-клієнт застосунку.
//
// Ідентичність клієнта наскрізна: той самий User-Agent використовується провайдерами,
// екстракторами і передається плеєру. Розбіжність UA між екстракцією і відтворенням —
// типова причина відмови відеохостів.
package httpx

import (
	"errors"
	"net/http"
	"time"
)

const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7; rv:128.0) Gecko/20100101 Firefox/128.0"

func NewClient(rt http.RoundTripper) *http.Client {
	return &http.Client{Timeout: 20 * time.Second, Transport: rt}
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
