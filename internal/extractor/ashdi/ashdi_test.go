package ashdi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/extractortest"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestContract(t *testing.T) {
	extractortest.Run(t,
		func(c *http.Client) extractor.Extractor { return New(c) },
		extractortest.Case{
			Host:        host,
			Embed:       "https://ashdi.vip/vod/104245",
			FixtureDir:  "testdata",
			WantStreams: 1,
			WantHeader: map[string]string{
				"Referer":    "https://ashdi.vip/",
				"User-Agent": httpx.UserAgent,
			},
			ExtraFixtureCheck: func(t *testing.T, streams []extractor.Stream) {
				// master-плейлист: варіанти 1080/720/480 обирає плеєр
				if !strings.Contains(streams[0].URL, ".m3u8") {
					t.Errorf("очікував m3u8, отримав %q", streams[0].URL)
				}
				if streams[0].Quality != 0 {
					t.Errorf("Quality = %d, want 0 (master)", streams[0].Quality)
				}
			},
		})
}

func TestExtractRejectsSuspiciousURL(t *testing.T) {
	for _, streamURL := range []string{
		"http://127.0.0.1/x.m3u8",
		"https://localhost/x.m3u8",
		"http://cdn.example/x.m3u8",
	} {
		t.Run(streamURL, func(t *testing.T) {
			page := `<script>new Playerjs({id:"p", file:'` + streamURL + `'})</script>`
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(page)),
					Header:     http.Header{},
				}, nil
			})}

			_, err := New(client).Extract(t.Context(), "https://ashdi.vip/vod/1", "https://anitube.in.ua/x")
			if !errors.Is(err, errs.ErrNoStream) || !strings.Contains(err.Error(), "підозрілий URL") {
				t.Fatalf("Extract error = %v, want ErrNoStream про підозрілий URL", err)
			}
		})
	}
}
