package remote

import (
	"errors"
	"strings"
	"testing"
)

func TestLabelFromHostname(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Vitaliis-MacBook-Pro.local", "vitaliis-macbook-pro"},
		{"My Mac", "my-mac"},
		{"", ""},
		{"MacBook_Pro", "macbook-pro"},
		{"host.lan.example", "host"},
		{"-weird-.local", "weird"},
		{"Ноутбук", ""}, // не-ASCII не вцілів — далі спрацює запасний IP
		{"box42", "box42"},
	}
	for _, tc := range cases {
		if got := labelFromHostname(tc.in); got != tc.want {
			t.Errorf("labelFromHostname(%q) = %q, очікував %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildURL(t *testing.T) {
	const port = 51234
	if got, want := buildURL("vitaliis-macbook-pro", port, testToken),
		"http://vitaliis-macbook-pro.local:51234/r/"+testToken; got != want {
		t.Errorf("buildURL(ім'я) = %q, очікував %q", got, want)
	}
	// IP резолвити не треба, тому ".local" до нього не клеїмо.
	if got, want := buildURL("192.168.1.2", port, testToken),
		"http://192.168.1.2:51234/r/"+testToken; got != want {
		t.Errorf("buildURL(IP) = %q, очікував %q", got, want)
	}
	if strings.Contains(buildURL("192.168.1.2", port, testToken), ".local") {
		t.Error("до IP додався .local")
	}
	// відкритий режим: без токена — корінь
	if got, want := buildURL("vitaliis-macbook-pro", port, ""),
		"http://vitaliis-macbook-pro.local:51234/"; got != want {
		t.Errorf("buildURL(open) = %q, очікував %q", got, want)
	}
}

func TestNewHandlerRejectsBadToken(t *testing.T) {
	for _, tok := range []string{"abc", "", strings.Repeat("z", 32), testToken + "0"} {
		if _, err := NewHandler(tok, playingCtl()); !errors.Is(err, ErrBadToken) {
			t.Errorf("NewHandler(%q) err = %v, очікував ErrBadToken", tok, err)
		}
	}
	if _, err := NewHandler(testToken, playingCtl()); err != nil {
		t.Errorf("NewHandler(валідний токен): %v", err)
	}
}
