// Package ashdi — екстрактор відеохоста ashdi.vip.
//
// Структура embed-сторінки, перевірена 2026-08-31: /vod/<id> — HTML з ініціалізацією
// Playerjs, потік у file:'https://ashdi.vip/.../index.m3u8' (master-плейлист з
// варіантами 1080/720/480). Потік віддається лише з Referer хоста.
package ashdi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

const host = "ashdi.vip"

type Extractor struct {
	http *http.Client
}

func New(httpClient *http.Client) *Extractor {
	return &Extractor{http: httpClient}
}

func (e *Extractor) ID() string { return "ashdi" }

func (e *Extractor) Handles(embed string) bool {
	return strings.Contains(embed, host+"/")
}

var reFile = regexp.MustCompile(`file:\s*'(https?://[^']+\.m3u8[^']*)'`)

func (e *Extractor) Extract(ctx context.Context, embed, referer string) ([]extractor.Stream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, embed, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	req.Header.Set("Referer", referer)
	res, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ashdi: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ashdi: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("ashdi: %w", err)
	}
	m := reFile.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("ashdi: у embed %s не знайдено file:'…m3u8' (хост змінив плеєр?)", embed)
	}
	return []extractor.Stream{{
		URL:     string(m[1]),
		Quality: 0, // master-плейлист, варіанти обирає плеєр
		Headers: map[string]string{
			"Referer":    "https://" + host + "/",
			"User-Agent": httpx.UserAgent,
		},
	}}, nil
}

// FixtureTransport відповідає на будь-який /vod/-запит до ashdi.vip
// канонічною фікстурою embed.html. Чужі домени пропускає.
func FixtureTransport(dir string) http.RoundTripper {
	return fixtureTransport{dir: dir}
}

type fixtureTransport struct{ dir string }

func (t fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != host {
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

// recordEmbedURL — embed канонічної фікстури (Фрірен, 1 серія, FanVoxUA).
const recordEmbedURL = "https://ashdi.vip/vod/104245"

// RecordFixtures переписує embed.html з живого хоста (вручну, не в CI).
func RecordFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, recordEmbedURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", httpx.UserAgent)
	req.Header.Set("Referer", "https://anitube.in.ua/")
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("запис embed.html: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("запис embed.html: HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "embed.html"), body, 0o644)
}
