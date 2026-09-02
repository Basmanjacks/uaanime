// Package moonanime — екстрактор відеохоста moonanime.art.
//
// Структура embed-сторінки, перевірена 2026-09-02 (плеєр Plyr + hls.js, статика ?v=0.18.1):
//
//   - GET /iframe/<id> відповідає 400, якщо в запиті немає заголовків Accept і
//     Accept-Language (Go, на відміну від curl, Accept сам не додає); Referer, cookies
//     та Origin не потрібні. Той самий заголовок вимагає CDN плейлистів
//     s.moonanime.art (маніфест і quality/<q>/index.m3u8); сегменти на s2.mooncdn.online — ні.
//   - Сторінка містить інлайн-скрипт (function(){var _X=atob("<~22 KB base64>"); …
//     TextDecoder … eval})(). Імена змінних випадкові, алгоритм стабільний.
//     Етап 1: b = base64; seed = b[0]; key = b[1:33]; далі для i від 0:
//     out[i] = b[i+33] ^ key[i%32] ^ state; state = (b[i+33] + key[i%32]) & 255 → JS-текст.
//   - У цьому JS: function _0xd(e){var k="<ключ>"; … atob(e) … XOR з повторюваним k …
//     decodeURIComponent(escape(r))} і var rawVideo = _0xd("<base64>").
//     Етап 2: base64 → XOR з ключем k → байти UTF-8.
//   - rawVideo — URL master-плейлиста hls:manifest.m3u8?expires=…&sig=… (підпис живе ~2 год,
//     тому URL ніколи не кешується) або Playerjs-список "[1080]url,[720]url".
//
// Плеєри: VLC відкидає всі заголовки, крім Referer/User-Agent, але Accept: */* і
// Accept-Language надсилає сам (перевірено VLC 3.0.17.3: маніфест, quality-плейлисти й
// сегменти — 200); mpv отримує їх через --http-header-fields. Якщо декодування дає не https-URL — хост змінив плеєр,
// слід виконати provider-repair.
package moonanime

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

const host = "moonanime.art"

// acceptLanguage — без ком: mpv отримує заголовки списком через кому
// (internal/player/mpv.go), а хосту достатньо самої наявності заголовка.
const acceptLanguage = "uk-UA"

// accept — хост відповідає 400 і без цього заголовка; curl шле його завжди, Go — ні.
const accept = "*/*"

// embedHeaders потрібні і при екстракції, і при записі фікстури: без них
// /iframe/<id> відповідає 400.
func embedHeaders() http.Header {
	return http.Header{
		"Accept":          {accept},
		"Accept-Language": {acceptLanguage},
	}
}

type Extractor struct {
	http *http.Client
}

func New(httpClient *http.Client) *Extractor {
	return &Extractor{http: httpClient}
}

func (e *Extractor) ID() string { return "moonanime" }

func (e *Extractor) Handles(embed string) bool {
	return extractor.HostIs(embed, host)
}

var (
	reBlob = regexp.MustCompile(`atob\("([A-Za-z0-9+/=]{1000,})"\)`)
	reKey  = regexp.MustCompile(`function\s+([\w$]+)\s*\(\s*\w+\s*\)\s*\{\s*var\s+k\s*=\s*"([^"]+)"`)
	// Хост віддає два варіанти сторінки: Plyr (`var rawVideo = _0xd("…")`) без Referer
	// і Playerjs (`file: _0xd("…")`) з Referer сайту-каталогу. Ключ і кодування спільні.
	reRawVideo = regexp.MustCompile(`(?:var\s+rawVideo\s*=|\bfile\s*:)\s*([\w$]+)\(\s*"([A-Za-z0-9+/=]*)"\s*\)`)
	reLabeled  = regexp.MustCompile(`\[([^\]]+)\](https?://[^,\[\s]+)`)
)

func noStream(what string) error {
	return fmt.Errorf("moonanime: %s (хост змінив плеєр?): %w", what, errs.ErrNoStream)
}

// unwrap — етап 1: base64-блоб інлайн-скрипта → розшифрований JS-текст.
func unwrap(blob string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil || len(raw) < 34 {
		return nil, noStream("не вдалося розпакувати конфіг плеєра")
	}
	key := raw[1:33]
	body := raw[33:]
	out := make([]byte, len(body))
	state := raw[0]
	for i, c := range body {
		k := key[i%32]
		out[i] = c ^ k ^ state
		state = c + k // переповнення byte і є "& 255"
	}
	return out, nil
}

// xorKey — етап 2: base64 → XOR з повторюваним ключем → рядок UTF-8.
func xorKey(b64, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 || key == "" {
		return "", noStream("не вдалося розшифрувати адресу відео")
	}
	out := make([]byte, len(raw))
	for i, c := range raw {
		out[i] = c ^ key[i%len(key)]
	}
	return string(out), nil
}

// videoURL — один варіант із Playerjs-списку "[1080]url,[720]url"; quality 0 = master.
type videoURL struct {
	quality int
	url     string
}

// parseVideoURLs віддзеркалює MaSource.parseVideoUrls зі скрипта хоста.
func parseVideoURLs(s string) []videoURL {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []videoURL
	for _, m := range reLabeled.FindAllStringSubmatch(s, -1) {
		q, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		out = append(out, videoURL{quality: q, url: m[2]})
	}
	if len(out) == 0 {
		return []videoURL{{url: s}}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].quality > out[j].quality })
	return out
}

// decodeVideo — обидва етапи: HTML embed-сторінки → рядок rawVideo.
func decodeVideo(page []byte) (string, error) {
	m := reBlob.FindSubmatch(page)
	if m == nil {
		return "", noStream("не знайдено конфіг плеєра atob(\"…\")")
	}
	js, err := unwrap(string(m[1]))
	if err != nil {
		return "", err
	}
	km := reKey.FindSubmatch(js)
	vm := reRawVideo.FindSubmatch(js)
	if km == nil || vm == nil {
		return "", noStream("не знайдено rawVideo або ключ декодера")
	}
	if !bytes.Equal(km[1], vm[1]) {
		return "", noStream("rawVideo декодується не тією функцією, що очікувалось")
	}
	return xorKey(string(vm[2]), string(km[2]))
}

func (e *Extractor) Extract(ctx context.Context, embed, referer string) ([]extractor.Stream, error) {
	body, err := extractor.FetchEmbed(ctx, e.http, embed, referer, embedHeaders())
	if err != nil {
		return nil, fmt.Errorf("moonanime: %w", err)
	}
	video, err := decodeVideo(body)
	if err != nil {
		return nil, err
	}
	var streams []extractor.Stream
	for _, v := range parseVideoURLs(video) {
		if !extractor.ValidStreamURL(v.url) {
			continue
		}
		streams = append(streams, extractor.Stream{
			URL:     v.url,
			Quality: v.quality, // 0 = master-плейлист, варіанти обирає плеєр
			Headers: map[string]string{
				// CDN плейлистів вимагає Accept і Accept-Language так само, як embed.
				// VLC шле обидва сам; mpv отримує їх через --http-header-fields.
				"Referer":         "https://" + host + "/",
				"User-Agent":      httpx.UserAgent,
				"Accept":          accept,
				"Accept-Language": acceptLanguage,
			},
		})
	}
	if len(streams) == 0 {
		return nil, fmt.Errorf("moonanime: підозрілий URL потоку: %w", errs.ErrNoStream)
	}
	return streams, nil
}

// FixtureTransport відповідає на будь-який /iframe/-запит до moonanime.art
// канонічною фікстурою embed.html. Чужі домени пропускає.
func FixtureTransport(dir string) http.RoundTripper {
	return extractor.FixtureTransport(host, dir)
}

// recordEmbedURL — embed канонічної фікстури (Фрірен, 1 серія, FanVoxUA).
const recordEmbedURL = "https://moonanime.art/iframe/twgwasggpzgkktvozfktjplgtpax"

// RecordFixtures переписує embed.html з живого хоста (вручну, не в CI).
func RecordFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	return extractor.RecordEmbed(ctx, httpClient, recordEmbedURL, dir, embedHeaders())
}
