// Package extractor — контракт відеохоста: embed-посилання → придатний до
// відтворення потік. Коли ламається один хост, страждають усі провайдери одразу,
// тому екстрактори мають найвищий пріоритет за якістю і тестами.
package extractor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

// Stream — те, що можна віддати плеєру. URL ніколи не кешується: він протухає.
type Stream struct {
	URL     string            `json:"url"`
	Quality int               `json:"quality"` // 1080, 720; 0 = невідомо/авто (master-плейлист)
	Headers map[string]string `json:"headers"` // Referer, User-Agent — доносимо до плеєра
}

type Extractor interface {
	ID() string
	Handles(embed string) bool
	Extract(ctx context.Context, embed, referer string) ([]Stream, error)
}

// HostIs суворо звіряє HTTPS-хост, бо embed-посилання надходять із недовіреного HTML.
func HostIs(raw, host string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == host
}

// Find обирає перший екстрактор, що вміє цей embed.
func Find(list []Extractor, embed string) (Extractor, bool) {
	for _, e := range list {
		if e.Handles(embed) {
			return e, true
		}
	}
	return nil, false
}

// FetchEmbed завантажує embed-сторінку відеохоста: UA застосунку (той самий, що
// піде плеєру), Referer сайту-каталогу і, за потреби, додаткові заголовки хоста.
// Класифікацію збоїв робить httpx.Do — екстрактор лише додає свій ID як префікс.
func FetchEmbed(ctx context.Context, client *http.Client, embed, referer string, extra http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, embed, nil)
	if err != nil {
		return nil, fmt.Errorf("створення запиту: %w: %w", errs.ErrProvider, err)
	}
	setEmbedHeaders(req, referer, extra)
	return httpx.Do(client, req)
}

func setEmbedHeaders(req *http.Request, referer string, extra http.Header) {
	req.Header.Set("User-Agent", httpx.UserAgent)
	req.Header.Set("Referer", referer)
	for k, vals := range extra {
		for _, v := range vals {
			req.Header.Set(k, v)
		}
	}
}

// FixtureTransport відповідає канонічною фікстурою embed.html на будь-який запит
// до host; запити до інших доменів віддає MultiTransport через ErrSkip.
func FixtureTransport(host, dir string) http.RoundTripper {
	return fixtureTransport{host: host, dir: dir}
}

type fixtureTransport struct{ host, dir string }

func (t fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != t.host {
		return nil, httpx.ErrSkip
	}
	b, err := os.ReadFile(filepath.Join(t.dir, "embed.html"))
	if err != nil {
		return nil, fmt.Errorf("фікстура embed.html: %w", err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     http.Header{},
	}, nil
}

// recordReferer — сайт-каталог, з якого embed відкривається насправді: хости
// віддають різні варіанти сторінки залежно від Referer, і фікстура має бути тим
// самим варіантом, що його бачить застосунок у бою.
const recordReferer = "https://anitube.in.ua/"

// RecordEmbed переписує embed.html з живого хоста (вручну, ніколи в CI).
func RecordEmbed(ctx context.Context, client *http.Client, embedURL, dir string, extra http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, embedURL, nil)
	if err != nil {
		return err
	}
	setEmbedHeaders(req, recordReferer, extra)
	body, err := httpx.Do(client, req)
	if err != nil {
		return fmt.Errorf("запис embed.html: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "embed.html"), body, 0o644)
}

// ValidStreamURL відсікає URL потоку, який недовірений embed міг підставити, щоб
// скерувати плеєр у локальну мережу. Самого net.ParseIP мало: «127.1», «0x7f.0.0.1»
// чи «localhost.» він IP не вважає, а libc резолвить їх у loopback. Тому hostname
// канонізується, а остання мітка має бути алфавітною — це відсікає всі числові й
// шістнадцяткові записи адрес.
//
// Межа довіри тут явна і вужча, ніж здається: перевіряється ЛИШЕ текст URL.
// Резолюцію імені (яке цілком може вказувати на приватну адресу), редиректи і
// абсолютні URL усередині HLS-плейлиста виконує зовнішній плеєр, і застосунок
// їх не контролює. Це свідомий залишковий ризик v1: перехопити той трафік можна
// лише локальним проксі (власна DNS/IP-перевірка + розбір m3u8) — окремим
// мережевим компонентом, несумісним із «легким продуктом», а per-host
// allowlist CDN ламався б при кожній зміні хоста, тобто на найчастішому класі
// поломок provider-repair.
func ValidStreamURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" || net.ParseIP(host) != nil {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" {
			return false
		}
	}
	tld := labels[len(labels)-1]
	if len(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
