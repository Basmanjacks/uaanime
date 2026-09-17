package download

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// Фікстури змодельовані на живих формах трьох хостів (див. doc.go), але з
// вигаданими іменами: правило 1 тримає назви сайтів у internal/provider і
// internal/extractor. Хости саме іменні — ValidStreamURL відкидає IP-літерали
// й localhost, тож «https://127.0.0.1/…» перевіряв би не те, що треба.
const baseRaw = "https://play.example.invalid/stream/abc/index.m3u8"

func mustBase(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse(baseRaw)
	if err != nil {
		t.Fatalf("розбір бази: %v", err)
	}
	return u
}

func TestParseAttrs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{
			name: "кома всередині лапок лишається у значенні",
			in:   `BANDWIDTH=2128000,CODECS="avc1.64001f,mp4a.40.2",RESOLUTION=1920x1080`,
			want: map[string]string{"BANDWIDTH": "2128000", "CODECS": "avc1.64001f,mp4a.40.2", "RESOLUTION": "1920x1080"},
		},
		{
			name: "RESOLUTION перший",
			in:   `RESOLUTION=1920x1080,BANDWIDTH=2128000`,
			want: map[string]string{"RESOLUTION": "1920x1080", "BANDWIDTH": "2128000"},
		},
		{
			name: "BANDWIDTH перший",
			in:   `BANDWIDTH=1080000,RESOLUTION=1920x1080`,
			want: map[string]string{"BANDWIDTH": "1080000", "RESOLUTION": "1920x1080"},
		},
		{
			name: "лапки в кінці рядка",
			in:   `BANDWIDTH=800000,NAME="720p"`,
			want: map[string]string{"BANDWIDTH": "800000", "NAME": "720p"},
		},
		{
			name: "нижній регістр ключа канонізується",
			in:   `bandwidth=100, resolution=640x360`,
			want: map[string]string{"BANDWIDTH": "100", "RESOLUTION": "640x360"},
		},
		{
			name: "незакрита лапка — решта рядка як значення",
			in:   `METHOD=AES-128,URI="https://k.example.invalid/key`,
			want: map[string]string{"METHOD": "AES-128", "URI": "https://k.example.invalid/key"},
		},
		{
			name: "порожній вхід",
			in:   "",
			want: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAttrs(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseAttrs(%q) = %v, очікував %v", tt.in, got, tt.want)
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Fatalf("parseAttrs(%q)[%s] = %q, очікував %q", tt.in, k, got[k], want)
				}
			}
		})
	}
}

func TestIsMaster(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "майстер",
			body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n",
			want: true,
		},
		{
			name: "медіаплейлист",
			body: "#EXTM3U\n#EXTINF:9.009,\nseg1.ts\n#EXT-X-ENDLIST\n",
			want: false,
		},
		{
			name: "лише I-FRAME не робить майстром для нас",
			body: "#EXTM3U\n#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=1,URI=\"i.m3u8\"\n",
			want: false,
		},
		{
			name: "BOM і CRLF не заважають",
			body: "\ufeff#EXTM3U\r\n#EXT-X-STREAM-INF:BANDWIDTH=1\r\nv.m3u8\r\n",
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMaster([]byte(tt.body)); got != tt.want {
				t.Fatalf("IsMaster = %v, очікував %v", got, tt.want)
			}
		})
	}
}

func TestParseMaster(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []Variant
	}{
		{
			name: "обидва порядки атрибутів, кома в лапках, абсолютний і відносний URI",
			body: "#EXTM3U\n" +
				"#EXT-X-INDEPENDENT-SEGMENTS\n" +
				"#EXT-X-VERSION:3\n" +
				"#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"ukr\",DEFAULT=YES\n" +
				"#EXT-X-STREAM-INF:RESOLUTION=1920x1080,BANDWIDTH=2128000,CODECS=\"avc1.64001f,mp4a.40.2\"\n" +
				"https://jk19.cdn.example.invalid/hls/1080/index.m3u8\n" +
				"#EXT-X-STREAM-INF:BANDWIDTH=1080000,RESOLUTION=1280x720\n" +
				"720/index.m3u8\n" +
				"#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=90000,URI=\"iframe.m3u8\"\n",
			want: []Variant{
				{URL: "https://jk19.cdn.example.invalid/hls/1080/index.m3u8", Bandwidth: 2128000, Width: 1920, Height: 1080},
				{URL: "https://play.example.invalid/stream/abc/720/index.m3u8", Bandwidth: 1080000, Width: 1280, Height: 720},
			},
		},
		{
			name: "BOM, CRLF і відсутній RESOLUTION",
			body: "\ufeff#EXTM3U\r\n#EXT-X-STREAM-INF:BANDWIDTH=500000\r\n../480/index.m3u8\r\n",
			want: []Variant{
				{URL: "https://play.example.invalid/stream/480/index.m3u8", Bandwidth: 500000},
			},
		},
		{
			name: "відсутній BANDWIDTH — 0, але варіант живий",
			body: "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=640x360\nlow.m3u8\n",
			want: []Variant{
				{URL: "https://play.example.invalid/stream/abc/low.m3u8", Width: 640, Height: 360},
			},
		},
		{
			name: "непридатний варіант відкинуто, решта живе",
			body: "#EXTM3U\n" +
				"#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=1920x1080\n" +
				"http://plain.example.invalid/1080.m3u8\n" +
				"#EXT-X-STREAM-INF:BANDWIDTH=2,RESOLUTION=1280x720\n" +
				"https://203.0.113.9/720.m3u8\n" +
				"#EXT-X-STREAM-INF:BANDWIDTH=3,RESOLUTION=854x480\n" +
				"480/index.m3u8\n",
			want: []Variant{
				{URL: "https://play.example.invalid/stream/abc/480/index.m3u8", Bandwidth: 3, Width: 854, Height: 480},
			},
		},
		{
			name: "теґ без URI в кінці файлу ігнорується",
			body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nok.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=2\n",
			want: []Variant{
				{URL: "https://play.example.invalid/stream/abc/ok.m3u8", Bandwidth: 1},
			},
		},
		{
			name: "рівно межа у 64 варіанти",
			body: manyVariants(maxVariants),
			want: nil, // перевіряємо лише кількість, див. нижче
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMaster(mustBase(t), []byte(tt.body))
			if err != nil {
				t.Fatalf("ParseMaster: %v", err)
			}
			if tt.want == nil {
				if len(got) != maxVariants {
					t.Fatalf("варіантів %d, очікував %d", len(got), maxVariants)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("варіантів %d (%v), очікував %d (%v)", len(got), got, len(tt.want), tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("варіант %d = %+v, очікував %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseMasterErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{
			name: "не m3u8",
			body: "<html><body>404</body></html>",
			want: errs.ErrProvider,
		},
		{
			name: "порожнє тіло",
			body: "",
			want: errs.ErrProvider,
		},
		{
			name: "усі варіанти непридатні",
			body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttp://plain.example.invalid/a.m3u8\n" +
				"#EXT-X-STREAM-INF:BANDWIDTH=2\nhttps://localhost/b.m3u8\n",
			want: errs.ErrProvider,
		},
		{
			name: "жодного варіанта",
			body: "#EXTM3U\n#EXT-X-INDEPENDENT-SEGMENTS\n",
			want: errs.ErrProvider,
		},
		{
			name: "більше за ліміт варіантів",
			body: manyVariants(maxVariants + 1),
			want: errs.ErrProvider,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMaster(mustBase(t), []byte(tt.body))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseMaster = %v, очікував %v", err, tt.want)
			}
		})
	}
}

func TestParseMedia(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantURLs []string
		wantSec  float64
	}{
		{
			name: "абсолютні сегменти на іншому піддомені",
			body: "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n" +
				"#EXT-X-PLAYLIST-TYPE:VOD\n" +
				"#EXTINF:9.009,\nhttps://jk19.cdn.example.invalid/hls/1080/seg1.ts\n" +
				"#EXTINF:0.991,\nhttps://jk19.cdn.example.invalid/hls/1080/seg2.ts\n" +
				"#EXT-X-ENDLIST\n",
			wantURLs: []string{
				"https://jk19.cdn.example.invalid/hls/1080/seg1.ts",
				"https://jk19.cdn.example.invalid/hls/1080/seg2.ts",
			},
			wantSec: 10,
		},
		{
			name: "відносні сегменти з ALLOW-CACHE і CRLF",
			body: "#EXTM3U\r\n#EXT-X-VERSION:3\r\n#EXT-X-ALLOW-CACHE:YES\r\n#EXT-X-TARGETDURATION:11\r\n" +
				"#EXTINF:10.000,\r\nsegment1.ts\r\n#EXTINF:5.500,\r\nsegment2.ts\r\n#EXT-X-ENDLIST\r\n",
			wantURLs: []string{
				"https://play.example.invalid/stream/abc/segment1.ts",
				"https://play.example.invalid/stream/abc/segment2.ts",
			},
			wantSec: 15.5,
		},
		{
			name: "підписані query-рядки зберігаються",
			body: "\ufeff#EXTM3U\n#EXTINF:6,\n" +
				"https://s2.cdn.example.invalid/v/1/seg1.ts?expires=1789000000&sig=abc123\n" +
				"#EXTINF:6,назва сегмента\nseg2.ts?expires=1789000000&sig=def456\n#EXT-X-ENDLIST\n",
			wantURLs: []string{
				"https://s2.cdn.example.invalid/v/1/seg1.ts?expires=1789000000&sig=abc123",
				"https://play.example.invalid/stream/abc/seg2.ts?expires=1789000000&sig=def456",
			},
			wantSec: 12,
		},
		{
			name: "невідомі теги і DISCONTINUITY пропускаються",
			body: "#EXTM3U\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-KEY:METHOD=NONE\n" +
				"#EXTINF:4,\na.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:4,\nb.ts\n#EXT-X-ENDLIST\n",
			wantURLs: []string{
				"https://play.example.invalid/stream/abc/a.ts",
				"https://play.example.invalid/stream/abc/b.ts",
			},
			wantSec: 8,
		},
		{
			name: "EXTINF без URI в кінці файлу ігнорується",
			body: "#EXTM3U\n#EXTINF:4,\na.ts\n#EXTINF:4,\n#EXT-X-ENDLIST\n",
			wantURLs: []string{
				"https://play.example.invalid/stream/abc/a.ts",
			},
			wantSec: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMedia(mustBase(t), []byte(tt.body))
			if err != nil {
				t.Fatalf("ParseMedia: %v", err)
			}
			if len(got.Segments) != len(tt.wantURLs) {
				t.Fatalf("сегментів %d (%v), очікував %d", len(got.Segments), got.Segments, len(tt.wantURLs))
			}
			for i, want := range tt.wantURLs {
				if got.Segments[i].URL != want {
					t.Fatalf("сегмент %d = %q, очікував %q", i, got.Segments[i].URL, want)
				}
			}
			if diff := got.TotalSec - tt.wantSec; diff > 1e-9 || diff < -1e-9 {
				t.Fatalf("TotalSec = %v, очікував %v", got.TotalSec, tt.wantSec)
			}
		})
	}
}

func TestParseMediaErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want error
	}{
		{
			name: "не m3u8",
			body: "just text\n#EXTM3U\n",
			want: errs.ErrProvider,
		},
		{
			name: "зашифровані сегменти",
			body: "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"https://k.example.invalid/k\",IV=0x0\n" +
				"#EXTINF:4,\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrEncryptedStream,
		},
		{
			name: "fMP4 з EXT-X-MAP",
			body: "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4,\na.m4s\n#EXT-X-ENDLIST\n",
			want: errs.ErrUnsupportedStream,
		},
		{
			name: "BYTERANGE",
			body: "#EXTM3U\n#EXTINF:4,\n#EXT-X-BYTERANGE:75232@0\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrUnsupportedStream,
		},
		{
			name: "live без ENDLIST",
			body: "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:120\n#EXTINF:4,\na.ts\n",
			want: errs.ErrUnsupportedStream,
		},
		{
			name: "PLAYLIST-TYPE:EVENT",
			body: "#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXTINF:4,\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrUnsupportedStream,
		},
		{
			name: "жодного сегмента",
			body: "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "непридатний сегмент валить увесь плейлист",
			body: "#EXTM3U\n#EXTINF:4,\na.ts\n#EXTINF:4,\nhttp://plain.example.invalid/b.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "сегмент на IP-літералі",
			body: "#EXTM3U\n#EXTINF:4,\nhttps://192.168.0.1/b.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "EXTINF не число",
			body: "#EXTM3U\n#EXTINF:abc,\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "нульова тривалість сегмента",
			body: "#EXTM3U\n#EXTINF:0,\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "тривалість сегмента понад годину",
			body: "#EXTM3U\n#EXTINF:3600.5,\na.ts\n#EXT-X-ENDLIST\n",
			want: errs.ErrProvider,
		},
		{
			name: "більше за ліміт сегментів",
			body: manySegments(maxSegments + 1),
			want: errs.ErrProvider,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMedia(mustBase(t), []byte(tt.body))
			if !errors.Is(err, tt.want) {
				t.Fatalf("ParseMedia = %v, очікував %v", err, tt.want)
			}
		})
	}
}

func TestParseMediaAtSegmentLimit(t *testing.T) {
	got, err := ParseMedia(mustBase(t), []byte(manySegments(maxSegments)))
	if err != nil {
		t.Fatalf("ParseMedia: %v", err)
	}
	if len(got.Segments) != maxSegments {
		t.Fatalf("сегментів %d, очікував %d", len(got.Segments), maxSegments)
	}
}

func TestResolveURLWithoutBase(t *testing.T) {
	if _, err := ParseMedia(nil, []byte("#EXTM3U\n#EXTINF:4,\na.ts\n#EXT-X-ENDLIST\n")); !errors.Is(err, errs.ErrProvider) {
		t.Fatalf("ParseMedia(nil base) = %v, очікував ErrProvider", err)
	}
}

func manyVariants(n int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := range n {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=640x360\nv%d.m3u8\n", 100000+i, i)
	}
	return b.String()
}

func manySegments(n int) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := range n {
		fmt.Fprintf(&b, "#EXTINF:4,\nseg%d.ts\n", i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}
