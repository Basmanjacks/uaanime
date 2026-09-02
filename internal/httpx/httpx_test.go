package httpx

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// bodyOfSize — відповідь 200 із тілом заданого розміру, без мережі.
func bodyOfSize(n int) http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("a"), n))),
			Header:     http.Header{},
		}, nil
	})
}

func mustRequest(t *testing.T, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func TestNewClientRejectsHTTPSRedirectToHTTP(t *testing.T) {
	var hit atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
	}))
	defer destination.Close()

	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, destination.URL+"/", http.StatusFound)
	}))
	defer origin.Close()

	res, err := NewClient(origin.Client().Transport).Get(origin.URL)
	if res != nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Get error = %v, want ErrProvider", err)
	}
	if hit.Load() {
		t.Fatal("redirect destination handler was hit")
	}
}

func TestNewClientRejectsRedirectToOtherHost(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "https://other.example/", http.StatusFound)
	}))
	defer origin.Close()

	res, err := NewClient(origin.Client().Transport).Get(origin.URL)
	if res != nil {
		_ = res.Body.Close()
	}
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Get error = %v, want ErrProvider", err)
	}
}

func TestNewClientAllowsSameHostRelativeRedirect(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			_, _ = io.WriteString(w, "final body")
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer origin.Close()

	res, err := NewClient(origin.Client().Transport).Get(origin.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got := string(body); got != "final body" {
		t.Fatalf("body = %q, want %q", got, "final body")
	}
	if strings.TrimSpace(res.Request.URL.Path) != "/final" {
		t.Fatalf("final path = %q, want /final", res.Request.URL.Path)
	}
}

func TestDoRejectsOversizedBody(t *testing.T) {
	client := &http.Client{Transport: bodyOfSize(MaxBody + 1)}
	_, err := Do(client, mustRequest(t, "https://example.test/big"))
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Do error = %v, want ErrProvider", err)
	}
	if !strings.Contains(err.Error(), "завелика") {
		t.Fatalf("Do error = %v, want повідомлення про завелику відповідь", err)
	}
}

func TestDoAcceptsBodyAtLimit(t *testing.T) {
	client := &http.Client{Transport: bodyOfSize(MaxBody)}
	body, err := Do(client, mustRequest(t, "https://example.test/limit"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(body) != MaxBody {
		t.Fatalf("len(body) = %d, want %d", len(body), MaxBody)
	}
}

func TestDoClassifiesDialFailureAsOffline(t *testing.T) {
	// Закритий порт на loopback: помилка з'єднання без жодного зовнішнього запиту.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = Do(NewClient(nil), mustRequest(t, "http://"+addr+"/x"))
	if !errs.Offline(err) {
		t.Fatalf("Do error = %v, want offline", err)
	}
}

func TestDoClassifiesHTTPErrorAsProvider(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}, nil
	})}
	_, err := Do(client, mustRequest(t, "https://example.test/x"))
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Do error = %v, want ErrProvider", err)
	}
	if errs.Offline(err) {
		t.Fatalf("503 класифіковано як офлайн: %v", err)
	}
}

func TestMultiTransportSkipsToNext(t *testing.T) {
	var second atomic.Bool
	mt := MultiTransport{
		roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ErrSkip }),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			second.Store(true)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     http.Header{},
			}, nil
		}),
	}
	body, err := Do(&http.Client{Transport: mt}, mustRequest(t, "https://example.test/x"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !second.Load() || string(body) != "ok" {
		t.Fatalf("body = %q, second hit = %v", body, second.Load())
	}
}

func TestMultiTransportFailsWhenNobodyHandles(t *testing.T) {
	mt := MultiTransport{
		roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ErrSkip }),
		roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, ErrSkip }),
	}
	_, err := Do(&http.Client{Transport: mt}, mustRequest(t, "https://example.test/x"))
	if err == nil {
		t.Fatal("Do: очікував помилку, коли жоден транспорт не обробив запит")
	}
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("Do error = %v, want ErrProvider", err)
	}
}
