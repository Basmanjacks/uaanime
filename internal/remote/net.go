package remote

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// Listen відкриває слухача на всіх інтерфейсах: телефон приходить не на
// loopback. Збережений порт важливий тому, що посилання живе в закладці —
// тому падаємо на ефемерний лише коли він реально зайнятий, і кажемо про це
// прапорцем, щоб UI попередив: закладка спрацює після перезапуску.
func Listen(port int) (ln net.Listener, ephemeral bool, err error) {
	if port > 0 {
		ln, err = net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			return ln, false, nil
		}
	}
	ln, err = net.Listen("tcp", ":0")
	if err != nil {
		return nil, false, err
	}
	return ln, port > 0, nil
}

// Port — фактичний порт слухача (після ":0" він відомий лише тут).
func Port(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// URL — адреса для закладки. Ім'я хоста + ".local" переживає зміну IP після
// перепідключення до Wi-Fi, а IP — ні; тому mDNS-ім'я основне, а IP запасне.
// Порожній токен — відкритий режим: адреса без "/r/<токен>", просто корінь.
func URL(port int, token string) string {
	return buildURL(hostLabel(), port, token)
}

// AltURL — запасна адреса за IP, для мереж без mDNS. Порожньо, якщо IP немає
// або він і так уже став основною адресою.
func AltURL(port int, token string) string {
	ip := lanIPv4()
	if ip == "" {
		return ""
	}
	alt := buildURL(ip, port, token)
	if alt == URL(port, token) {
		return ""
	}
	return alt
}

// buildURL винесено окремо від визначення хоста, щоб складання адреси можна
// було перевірити без залежності від машини, на якій біжать тести.
func buildURL(host string, port int, token string) string {
	if net.ParseIP(host) == nil {
		host += ".local"
	}
	base := "http://" + host + ":" + strconv.Itoa(port)
	if token == "" {
		return base + "/"
	}
	return base + pathPrefix + token
}

// hostLabel — мітка машини для mDNS-імені; якщо з імені хоста нічого путнього
// не лишилося, краще чесний IP, ніж адреса, яка не резолвиться.
func hostLabel() string {
	label := labelFromHostname(hostname())
	if label != "" {
		return label
	}
	if ip := lanIPv4(); ip != "" {
		return ip
	}
	return "127.0.0.1"
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// labelFromHostname зводить ім'я хоста до мітки, яку приймає mDNS: "Vitaliis-
// MacBook-Pro.local" → "vitaliis-macbook-pro", "My Mac" → "my-mac". Пробіли й
// підкреслення стають дефісами (саме так їх перетворює й сама Bonjour), решта
// небезпечних символів просто зникає.
func labelFromHostname(h string) string {
	if i := strings.IndexByte(h, '.'); i >= 0 {
		h = h[:i]
	}
	h = strings.ToLower(h)
	var b strings.Builder
	b.Grow(len(h))
	for _, r := range h {
		switch {
		case r == ' ' || r == '_':
			b.WriteByte('-')
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// lanIPv4 — перша IPv4-адреса живого не-loopback інтерфейсу. Link-local
// (169.254/16) відкидаємо: це адреса «DHCP не відповів», по ній ніхто не прийде.
func lanIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	const up = net.FlagUp | net.FlagRunning
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&up != up {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLinkLocalUnicast() {
				continue
			}
			return v4.String()
		}
	}
	return ""
}
