package ashdi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestHandles(t *testing.T) {
	e := New(nil)
	if !e.Handles("https://ashdi.vip/vod/104245") {
		t.Error("має обробляти ashdi.vip")
	}
	if e.Handles("https://tortuga.tw/vod/1") || e.Handles("https://moonanime.art/x") {
		t.Error("не має обробляти чужі хости")
	}
}

func TestExtractFixture(t *testing.T) {
	e := New(httpx.NewClient(FixtureTransport("testdata")))
	streams, err := e.Extract(t.Context(), "https://ashdi.vip/vod/104245", "https://anitube.in.ua/x")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("очікував 1 потік, отримав %d", len(streams))
	}
	s := streams[0]
	if !strings.Contains(s.URL, ".m3u8") {
		t.Errorf("очікував m3u8, отримав %q", s.URL)
	}
	if s.Headers["Referer"] == "" || s.Headers["User-Agent"] == "" {
		t.Errorf("потік без обов'язкових заголовків: %+v", s.Headers)
	}
}

func TestExtractClassifiesDNSFailureAsOffline(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "ashdi.vip", IsNotFound: true}
	})}

	_, err := New(client).Extract(t.Context(), "https://ashdi.vip/vod/1", "https://anitube.in.ua/x")
	if !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("Extract error = %v, очікував ErrOffline", err)
	}
}

func TestExtractClassifiesMissingURLAsNoStream(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("<html>без плеєра</html>")),
			Header:     http.Header{},
		}, nil
	})}

	_, err := New(client).Extract(t.Context(), "https://ashdi.vip/vod/1", "https://anitube.in.ua/x")
	if !errors.Is(err, errs.ErrNoStream) {
		t.Fatalf("Extract error = %v, очікував ErrNoStream", err)
	}
}
