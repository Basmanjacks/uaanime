package extractor

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

func TestHostIs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"exact HTTPS host", "https://tortuga.tw/vod/1", true},
		{"HTTP", "http://tortuga.tw/vod/1", false},
		{"port", "https://tortuga.tw:8443/x", false},
		{"subdomain", "https://evil.tortuga.tw/", false},
		{"host in path", "https://127.0.0.1/tortuga.tw/", false},
		{"invalid URL", "not a url", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostIs(tt.raw, "tortuga.tw"); got != tt.want {
				t.Errorf("HostIs(%q, %q) = %v, want %v", tt.raw, "tortuga.tw", got, tt.want)
			}
		})
	}
}

func TestValidStreamURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://cdn.example/x.m3u8", true},
		{"https://calypso.tortuga.tw/hls/index.m3u8", true},
		{"https://example.com./x", true},
		{"https://s.moonanime.art/a/hls:manifest.m3u8?expires=1&sig=ab", true},
		{"http://cdn.example/x.m3u8", false},
		{"https://127.0.0.1/x", false},
		{"https://127.1/x", false},
		{"https://0x7f.0.0.1/x", false},
		{"https://2130706433/x", false},
		{"https://[::1]/x", false},
		{"https://localhost/x", false},
		{"https://localhost./x", false},
		{"https://x.localhost/x", false},
		{"https://LOCALHOST/x", false},
		{"https://nodot/x", false},
		{"https://cdn..example/x", false},
		{"https://cdn.example.c0m/x", false},
		{"not a url", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := ValidStreamURL(tt.raw); got != tt.want {
				t.Errorf("ValidStreamURL(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFixtureTransport(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "embed.html"), []byte("<html>фікстура</html>"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rt := FixtureTransport("host.test", dir)

	other, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://other.test/vod/1", nil)
	skipped, err := rt.RoundTrip(other)
	if skipped != nil {
		_ = skipped.Body.Close()
	}
	if !errors.Is(err, httpx.ErrSkip) {
		t.Fatalf("чужий хост: err = %v, want ErrSkip", err)
	}

	own, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://host.test/vod/1", nil)
	res, err := rt.RoundTrip(own)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("StatusCode = %d, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "<html>фікстура</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestFetchEmbedSetsHeaders(t *testing.T) {
	var seen http.Header
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Clone()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("page")),
			Header:     http.Header{},
		}, nil
	})}

	extra := http.Header{"Accept": {"*/*"}, "Accept-Language": {"uk-UA"}}
	body, err := FetchEmbed(t.Context(), client, "https://host.test/vod/1", "https://anitube.in.ua/x", extra)
	if err != nil {
		t.Fatalf("FetchEmbed: %v", err)
	}
	if string(body) != "page" {
		t.Fatalf("body = %q, want %q", body, "page")
	}
	for k, want := range map[string]string{
		"User-Agent":      httpx.UserAgent,
		"Referer":         "https://anitube.in.ua/x",
		"Accept":          "*/*",
		"Accept-Language": "uk-UA",
	} {
		if got := seen.Get(k); got != want {
			t.Errorf("Header[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestFetchEmbedClassifiesHTTPErrorAsProvider(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}, nil
	})}
	_, err := FetchEmbed(t.Context(), client, "https://host.test/vod/1", "https://anitube.in.ua/", nil)
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("FetchEmbed error = %v, want ErrProvider", err)
	}
}
