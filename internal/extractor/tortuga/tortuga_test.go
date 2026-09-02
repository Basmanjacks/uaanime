package tortuga

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

const fixtureStreamURL = "https://calypso.tortuga.tw/hls/serials/shingeki.no.kyojin.ova01e01.fanvoxua.mvo_44420/hls/index.m3u8"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestDecodeFixture(t *testing.T) {
	body, err := os.ReadFile("testdata/embed.html")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	m := reFile.FindSubmatch(body)
	if m == nil {
		t.Fatal("fixture does not contain file")
	}
	got, err := decode(string(m[1]))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != fixtureStreamURL {
		t.Fatalf("decode = %q, want %q", got, fixtureStreamURL)
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name string
		blob string
	}{
		{"missing sentinel", "YWJj"},
		{"bad base64", "%%%%=="},
		{"too short", base64.StdEncoding.EncodeToString([]byte{1}) + "=="},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decode(tt.blob)
			if !errors.Is(err, errs.ErrNoStream) {
				t.Fatalf("decode error = %v, want ErrNoStream", err)
			}
		})
	}
}

func TestContract(t *testing.T) {
	extractortest.Run(t,
		func(c *http.Client) extractor.Extractor { return New(c) },
		extractortest.Case{
			Host:        host,
			Embed:       "https://tortuga.tw/vod/44420",
			FixtureDir:  "testdata",
			WantStreams: 1,
			WantHeader: map[string]string{
				"Referer":    "https://tortuga.tw/",
				"User-Agent": httpx.UserAgent,
			},
			ExtraFixtureCheck: func(t *testing.T, streams []extractor.Stream) {
				if streams[0].URL != fixtureStreamURL {
					t.Fatalf("stream URL = %q, want %q", streams[0].URL, fixtureStreamURL)
				}
			},
		})
}

func TestExtractRejectsSuspiciousURL(t *testing.T) {
	for _, streamURL := range []string{
		"https://localhost/x.m3u8",
		"http://cdn.example/x.m3u8",
		"https://127.1/x.m3u8",
	} {
		t.Run(streamURL, func(t *testing.T) {
			page := `new TortugaCore({ file: "` + encode(42, streamURL) + `" })`
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(page)),
					Header:     http.Header{},
				}, nil
			})}

			_, err := New(client).Extract(t.Context(), "https://tortuga.tw/vod/1", "https://anitube.in.ua/")
			if !errors.Is(err, errs.ErrNoStream) {
				t.Fatalf("Extract error = %v, want ErrNoStream", err)
			}
		})
	}
}

func encode(seed byte, s string) string {
	raw := make([]byte, len(s)+1)
	raw[0] = seed
	for i := range len(s) {
		raw[i+1] = s[i] ^ byte((int(seed)+7*i+13)%256)
	}
	return base64.StdEncoding.EncodeToString(raw) + "=="
}
