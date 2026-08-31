package ashdi

import (
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/httpx"
)

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
