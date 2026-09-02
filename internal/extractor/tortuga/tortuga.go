// Package tortuga — екстрактор відеохоста tortuga.tw.
//
// Структура embed-сторінки, перевірена 2026-09-01 зі скриптом tor.core.min.js?v=0.114:
// /vod/<id> містить new TortugaCore({ id: "...", file: "<blob>", ... }). Значення
// file завершується двосимвольним сентинелом "=="; після його точного видалення
// base64-дані декодуються XOR-ключем (seed + 7*i + 13) % 256. Якщо результат не
// починається з https://, хост змінив плеєр — слід виконати provider-repair.
package tortuga

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

const host = "tortuga.tw"

type Extractor struct {
	http *http.Client
}

func New(httpClient *http.Client) *Extractor {
	return &Extractor{http: httpClient}
}

func (e *Extractor) ID() string { return "tortuga" }

func (e *Extractor) Handles(embed string) bool {
	return extractor.HostIs(embed, host)
}

var reFile = regexp.MustCompile(`file:\s*"([A-Za-z0-9+/=]+)"`)

func decode(v string) (string, error) {
	if !strings.HasSuffix(v, "==") {
		return "", fmt.Errorf("tortuga: не вдалося декодувати file (хост змінив плеєр?): %w", errs.ErrNoStream)
	}
	raw, err := base64.StdEncoding.DecodeString(v[:len(v)-2])
	if err != nil || len(raw) < 2 {
		return "", fmt.Errorf("tortuga: не вдалося декодувати file (хост змінив плеєр?): %w", errs.ErrNoStream)
	}
	seed := raw[0]
	out := make([]byte, len(raw)-1)
	for i := 1; i < len(raw); i++ {
		out[i-1] = raw[i] ^ byte((int(seed)+7*(i-1)+13)%256)
	}
	return string(out), nil
}

func (e *Extractor) Extract(ctx context.Context, embed, referer string) ([]extractor.Stream, error) {
	body, err := extractor.FetchEmbed(ctx, e.http, embed, referer, nil)
	if err != nil {
		return nil, fmt.Errorf("tortuga: %w", err)
	}
	m := reFile.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("tortuga: у embed %q не знайдено file:\"…\" (хост змінив плеєр?): %w", embed, errs.ErrNoStream)
	}
	decoded, err := decode(string(m[1]))
	if err != nil {
		return nil, err
	}
	if !extractor.ValidStreamURL(decoded) {
		return nil, fmt.Errorf("tortuga: підозрілий URL потоку: %w", errs.ErrNoStream)
	}
	return []extractor.Stream{{
		URL:     decoded,
		Quality: 0, // master-плейлист, варіанти обирає плеєр
		Headers: map[string]string{
			// CDN наразі не перевіряє Referer; хост надсилаємо як безпечне типове значення.
			"Referer":    "https://" + host + "/",
			"User-Agent": httpx.UserAgent,
		},
	}}, nil
}

// FixtureTransport відповідає на будь-який /vod/-запит до tortuga.tw
// канонічною фікстурою embed.html. Чужі домени пропускає.
func FixtureTransport(dir string) http.RoundTripper {
	return extractor.FixtureTransport(host, dir)
}

// recordEmbedURL — embed канонічної фікстури (Атака титанів OVA, 1 серія, FanVoxUA).
const recordEmbedURL = "https://tortuga.tw/vod/44420"

// RecordFixtures переписує embed.html з живого хоста (вручну, не в CI).
func RecordFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	return extractor.RecordEmbed(ctx, httpClient, recordEmbedURL, dir, nil)
}
