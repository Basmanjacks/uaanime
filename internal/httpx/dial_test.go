package httpx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestPublicAddr(t *testing.T) {
	// По представнику на кожен рядок deny-таблиці: саме ці діапазони й дають
	// SSRF, якщо покладатися на «здоровий глузд» замість явного списку.
	denied := []string{
		"0.0.0.0", "0.1.2.3",
		"10.0.0.1", "10.255.255.255",
		"100.64.0.1", "100.127.0.1",
		"127.0.0.1",
		"169.254.169.254", // метадані хмари — класична ціль
		"172.16.0.1", "172.31.255.255",
		"192.0.0.1",
		"192.0.2.1",
		"192.88.99.1",
		"192.168.1.1",
		"198.18.0.1", "198.19.255.255",
		"198.51.100.1",
		"203.0.113.1",
		"224.0.0.1", "239.1.1.1",
		"240.0.0.1", "255.255.255.255",
		"::",
		"::1",
		"64:ff9b::a00:1", // NAT64 на 10.0.0.1
		"100::1",
		"2001:db8::1",
		"fc00::1", "fd00::1",
		"fe80::1",
		"ff02::1",
		"::ffff:10.0.0.1",  // IPv4-mapped приватна
		"::ffff:127.0.0.1", // IPv4-mapped loopback
	}
	for _, s := range denied {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", s, err)
		}
		if PublicAddr(a) {
			t.Errorf("PublicAddr(%s) = true, очікували відмову", s)
		}
	}

	allowed := []string{"1.1.1.1", "8.8.8.8", "104.21.0.1", "2606:4700::1111", "2a00:1450:4001::1"}
	for _, s := range allowed {
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("ParseAddr(%q): %v", s, err)
		}
		if !PublicAddr(a) {
			t.Errorf("PublicAddr(%s) = false, очікували дозвіл", s)
		}
	}

	if PublicAddr(netip.Addr{}) {
		t.Error("PublicAddr(нульова адреса) = true")
	}
}

func TestDialContextRejectsPrivateResolve(t *testing.T) {
	f := NewFetcher(nil)
	calls := 0
	f.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		calls++
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	_, err := f.dialContext(t.Context(), "tcp", "evil.test:443")
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
	if !strings.Contains(err.Error(), "непублічна адреса") {
		t.Errorf("err = %v, очікували згадку непублічної адреси", err)
	}
	if calls != 1 {
		t.Errorf("резолвер викликано %d разів, очікували 1", calls)
	}
}

func TestDialContextRejectsIPLiteralHost(t *testing.T) {
	f := NewFetcher(nil)
	f.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		t.Error("резолвер не мав викликатися для IP-літерала")
		return nil, nil
	}
	_, err := f.dialContext(t.Context(), "tcp", "93.184.216.34:443")
	if !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("err = %v, очікували ErrProvider", err)
	}
}

// Rebinding: перша відповідь публічна, друга приватна. Ми беремо першу
// перевірену адресу і НЕ резолвимо вдруге — саме це ламає атаку.
// Контекст скасований, тож реального з'єднання тест не відкриває.
func TestDialContextUsesFirstVettedAddressAndResolvesOnce(t *testing.T) {
	f := NewFetcher(nil)
	calls := 0
	f.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		calls++
		return []netip.Addr{
			netip.MustParseAddr("1.1.1.1"),
			netip.MustParseAddr("10.0.0.1"),
		}, nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := f.dialContext(ctx, "tcp", "cdn.test:443")
	if err == nil {
		t.Fatal("очікували помилку скасованого контексту")
	}
	if strings.Contains(err.Error(), "непублічна адреса") {
		t.Errorf("err = %v: перша адреса публічна, відмови бути не мало", err)
	}
	if calls != 1 {
		t.Errorf("резолвер викликано %d разів, очікували 1", calls)
	}
}

func TestDialContextClassifiesDNSFailureAsOffline(t *testing.T) {
	f := NewFetcher(nil)
	f.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	_, err := f.dialContext(t.Context(), "tcp", "cdn.test:443")
	if !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("err = %v, очікували ErrOffline", err)
	}
}

func TestDialContextRejectsEmptyResolve(t *testing.T) {
	f := NewFetcher(nil)
	f.resolve = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return nil, nil
	}
	_, err := f.dialContext(t.Context(), "tcp", "cdn.test:443")
	if !errors.Is(err, errs.ErrOffline) {
		t.Fatalf("err = %v, очікували ErrOffline", err)
	}
}
