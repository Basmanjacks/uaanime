package moonanime

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/extractortest"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

// fixtureStreamURL — маніфест із канонічної фікстури (Фрірен, 1 серія, FanVoxUA).
const fixtureStreamURL = "https://s.moonanime.art/content/stream/anime/12/twgwasggpzgkktvozfktjplgtpax/hls/video:video_s1_e1_FanVoxUA_4011/hls:manifest.m3u8?expires=1788344018&sig=0abb248fadc820a0"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAcceptLanguageHasNoComma(t *testing.T) {
	// mpv отримує заголовки списком через кому — значення з комою розпалося б на поля
	if strings.Contains(acceptLanguage, ",") {
		t.Fatalf("acceptLanguage %q містить кому", acceptLanguage)
	}
}

func TestDecodeFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/embed.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got, err := decodeVideo(body)
	if err != nil {
		t.Fatalf("decodeVideo: %v", err)
	}
	if got != fixtureStreamURL {
		t.Fatalf("decodeVideo = %q, want %q", got, fixtureStreamURL)
	}
}

func TestUnwrapRejects(t *testing.T) {
	tests := []struct {
		name string
		blob string
	}{
		{"bad base64", "%%%%"},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 33))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := unwrap(tt.blob)
			if !errors.Is(err, errs.ErrNoStream) {
				t.Fatalf("unwrap error = %v, want ErrNoStream", err)
			}
		})
	}
}

func TestWrapRoundTrip(t *testing.T) {
	js := `function f(e){var k="abc"} var rawVideo = f("QUJD")`
	got, err := unwrap(wrap(7, js))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != js {
		t.Fatalf("unwrap(wrap(js)) = %q, want %q", got, js)
	}
	if s, _ := xorKey(xorEncode("key", "текст"), "key"); s != "текст" {
		t.Fatalf("xorKey(xorEncode()) = %q", s)
	}
}

func TestParseVideoURLs(t *testing.T) {
	list := parseVideoURLs("[480]https://cdn.example/480.m3u8,[1080]https://cdn.example/1080.m3u8,[720]https://cdn.example/720.m3u8")
	if len(list) != 3 || list[0].quality != 1080 || list[1].quality != 720 || list[2].quality != 480 {
		t.Fatalf("список якостей = %+v", list)
	}
	if list[0].url != "https://cdn.example/1080.m3u8" {
		t.Fatalf("url[0] = %q", list[0].url)
	}
	single := parseVideoURLs(" https://cdn.example/master.m3u8 ")
	if len(single) != 1 || single[0].quality != 0 || single[0].url != "https://cdn.example/master.m3u8" {
		t.Fatalf("одиночний URL = %+v", single)
	}
	if got := parseVideoURLs("  "); got != nil {
		t.Fatalf("порожній рядок = %+v", got)
	}
}

func TestContract(t *testing.T) {
	extractortest.Run(t,
		func(c *http.Client) extractor.Extractor { return New(c) },
		extractortest.Case{
			Host:        host,
			Embed:       "https://moonanime.art/iframe/twgwasggpzgkktvozfktjplgtpax",
			FixtureDir:  "testdata",
			WantStreams: 1,
			WantHeader: map[string]string{
				"Referer":         "https://moonanime.art/",
				"User-Agent":      httpx.UserAgent,
				"Accept":          "*/*",
				"Accept-Language": acceptLanguage,
			},
			ExtraFixtureCheck: func(t *testing.T, streams []extractor.Stream) {
				if streams[0].URL != fixtureStreamURL || streams[0].Quality != 0 {
					t.Fatalf("stream = %+v", streams[0])
				}
			},
		})
}

// Хост віддає потік лише запиту, схожому на браузерний: без Accept-Language
// і Accept сторінка приходить без блоба. Контракт перевіряє заголовки ПОТОКУ,
// а тут — заголовки самого запиту embed.
func TestExtractSendsBrowserHeaders(t *testing.T) {
	var seen http.Header
	fixture := FixtureTransport("testdata")
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Clone()
		return fixture.RoundTrip(req)
	})}
	if _, err := New(client).Extract(t.Context(), "https://moonanime.art/iframe/twgwasggpzgkktvozfktjplgtpax", "https://anitube.in.ua/"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if seen.Get("Accept-Language") != acceptLanguage || seen.Get("Accept") != "*/*" {
		t.Fatalf("запит embed без обов'язкових заголовків: %v", seen)
	}
}

func pageWith(js string) *http.Client {
	// реальний блоб ~22 KB; reBlob навмисно не ловить короткі atob("…") інших скриптів
	js += "\n/*" + strings.Repeat("x", 1200) + "*/"
	page := `<script>(function(){var _a=atob("` + wrap(42, js) + `");var _b=new TextDecoder();})();</script>`
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(page)),
			Header:     http.Header{},
		}, nil
	})}
}

func TestExtractRejectsSuspiciousURL(t *testing.T) {
	for _, streamURL := range []string{
		"https://localhost/x.m3u8",
		"https://127.1/x.m3u8",
		"http://cdn.example/x.m3u8",
	} {
		t.Run(streamURL, func(t *testing.T) {
			js := `function _0xd(e){var k="k3y"} var rawVideo = _0xd("` + xorEncode("k3y", streamURL) + `")`
			_, err := New(pageWith(js)).Extract(t.Context(), "https://moonanime.art/iframe/x", "https://anitube.in.ua/")
			if !errors.Is(err, errs.ErrNoStream) || !strings.Contains(err.Error(), "підозрілий URL") {
				t.Fatalf("Extract error = %v, want ErrNoStream про підозрілий URL", err)
			}
		})
	}
}

func TestExtractPlayerjsVariantAndQualityList(t *testing.T) {
	list := "[720]https://s.moonanime.art/720.m3u8,[1080]https://s.moonanime.art/1080.m3u8"
	js := `function _0xd(e){var k="k3y"} new Playerjs({id:"p", file: _0xd("` + xorEncode("k3y", list) + `"), poster: _0xd("QQ==")})`
	streams, err := New(pageWith(js)).Extract(t.Context(), "https://moonanime.art/iframe/x", "https://anitube.in.ua/")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(streams) != 2 || streams[0].Quality != 1080 || streams[0].URL != "https://s.moonanime.art/1080.m3u8" || streams[1].Quality != 720 {
		t.Fatalf("streams = %+v", streams)
	}
}

func TestExtractMismatchedDecoderName(t *testing.T) {
	js := `function _0xd(e){var k="k3y"} var rawVideo = other("QQ==")`
	_, err := New(pageWith(js)).Extract(t.Context(), "https://moonanime.art/iframe/x", "https://anitube.in.ua/")
	if !errors.Is(err, errs.ErrNoStream) || !strings.Contains(err.Error(), "не тією функцією") {
		t.Fatalf("Extract error = %v, want ErrNoStream про невідповідну функцію", err)
	}
}

// wrap — обернення unwrap (етап 1) для тестових сторінок: детермінований 32-байтний ключ.
func wrap(seed byte, js string) string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	raw := make([]byte, 0, 33+len(js))
	raw = append(raw, seed)
	raw = append(raw, key...)
	state := seed
	for i := 0; i < len(js); i++ {
		k := key[i%32]
		c := js[i] ^ k ^ state
		raw = append(raw, c)
		state = c + k
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// xorEncode — обернення xorKey (етап 2).
func xorEncode(key, s string) string {
	out := make([]byte, len(s))
	for i := range out {
		out[i] = s[i] ^ key[i%len(key)]
	}
	return base64.StdEncoding.EncodeToString(out)
}
