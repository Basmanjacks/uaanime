package httpx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// denyPrefixes — явна таблиця special-use префіксів, куди завантажувач не ходить.
// Таблиця явна, а не net.IP.IsGlobalUnicast()/IsPrivate(): стандартні предикати
// пропускають 100.64/10 (CGNAT), 198.18/15 (бенчмарки), 192.0.0/24 та NAT64
// 64:ff9b::/96 — саме ті діапазони, якими зручно дістатися до роутера чи до
// внутрішньої мережі з недовіреного плейлиста.
var denyPrefixes = []netip.Prefix{
	// IPv4
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// IPv6
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// PublicAddr каже, чи можна відкривати з'єднання до цієї адреси. Адреса спершу
// Unmap()-иться: ::ffff:10.0.0.1 — це 10.0.0.1, і без Unmap жоден IPv4-префікс
// його б не впіймав (netip.Prefix.Contains не зіставляє різні родини адрес).
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() {
		return false
	}
	for _, p := range denyPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// dialTimeout — стеля на одне TCP-з'єднання. Не плутати з відсутнім
// Client.Timeout: обмежуємо встановлення з'єднання, а не тривалість качання.
const dialTimeout = 10 * time.Second

func defaultResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// dialContext — анти-SSRF шар під Transport. Текстового extractor.ValidStreamURL
// мало: ім'я хоста з недовіреного плейлиста цілком може резолвитись у 127.0.0.1
// або в адресу роутера. Тому ім'я резолвиться тут один раз, кожна адреса
// звіряється з denyPrefixes, і dial іде В ПЕРЕВІРЕНУ IP — повторного DNS немає,
// тож класичний DNS rebinding (перша відповідь публічна, друга приватна) не
// має куди вклинитися.
//
// SNI і перевірка сертифіката лишаються за іменем хоста: http.Transport робить
// TLS-handshake ПОНАД DialContext і бере ім'я з URL, а не з адреси, у яку ми
// з'єдналися. Тобто підміна IP не послаблює TLS.
func (f *Fetcher) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("httpx: адреса %q: %w: %w", addr, errs.ErrProvider, err)
	}
	// IP-літерал у хості відкидаємо: легального відеохоста без імені не буває,
	// а ValidStreamURL такий URL теж не пропустив би.
	if _, err := netip.ParseAddr(host); err == nil {
		return nil, fmt.Errorf("httpx: хост-літерал %s заборонено: %w", host, errs.ErrProvider)
	}

	resolve := f.resolve
	if resolve == nil {
		resolve = defaultResolve
	}
	addrs, err := resolve(ctx, host)
	if err != nil {
		return nil, Classify(host, "резолв імені", err)
	}

	vetted := make([]netip.Addr, 0, len(addrs))
	var denied netip.Addr
	for _, a := range addrs {
		a = a.Unmap()
		if PublicAddr(a) {
			vetted = append(vetted, a)
			continue
		}
		if !denied.IsValid() {
			denied = a
		}
	}
	if len(vetted) == 0 {
		if denied.IsValid() {
			return nil, fmt.Errorf("httpx: %s: непублічна адреса %s: %w", host, denied, errs.ErrProvider)
		}
		return nil, fmt.Errorf("httpx: %s: імені не резолвлено: %w", host, errs.ErrOffline)
	}

	d := net.Dialer{Timeout: dialTimeout}
	var lastErr error
	for _, a := range vetted {
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
