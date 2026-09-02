// Package ashdi — екстрактор відеохоста ashdi.vip.
//
// Структура embed-сторінки, перевірена 2026-08-31: /vod/<id> — HTML з ініціалізацією
// Playerjs, потік у file:'https://ashdi.vip/.../index.m3u8' (master-плейлист з
// варіантами 1080/720/480). Потік віддається лише з Referer хоста.
package ashdi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/Basmanjacks/uaanime/internal/errs"
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
	return extractor.HostIs(embed, host)
}

var reFile = regexp.MustCompile(`file:\s*'(https?://[^']+\.m3u8[^']*)'`)

func (e *Extractor) Extract(ctx context.Context, embed, referer string) ([]extractor.Stream, error) {
	body, err := extractor.FetchEmbed(ctx, e.http, embed, referer, nil)
	if err != nil {
		return nil, fmt.Errorf("ashdi: %w", err)
	}
	m := reFile.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("ashdi: у embed %q не знайдено file:'…m3u8' (хост змінив плеєр?): %w", embed, errs.ErrNoStream)
	}
	streamURL := string(m[1])
	// Regex пускає http:// і будь-який хост, тому URL із недовіреної сторінки
	// проходить ту саму перевірку, що й у решти екстракторів.
	if !extractor.ValidStreamURL(streamURL) {
		return nil, fmt.Errorf("ashdi: підозрілий URL потоку: %w", errs.ErrNoStream)
	}
	return []extractor.Stream{{
		URL:     streamURL,
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
	return extractor.FixtureTransport(host, dir)
}

// recordEmbedURL — embed канонічної фікстури (Фрірен, 1 серія, FanVoxUA).
const recordEmbedURL = "https://ashdi.vip/vod/104245"

// RecordFixtures переписує embed.html з живого хоста (вручну, не в CI).
func RecordFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	return extractor.RecordEmbed(ctx, httpClient, recordEmbedURL, dir, nil)
}
