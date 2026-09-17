package download

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
)

// Ліміти на ворожий вхід. Плейлист приходить із недовіреного джерела, тож
// розбір має впертися в стелю ДО того, як виділить пам'ять під мільйон
// сегментів. Числа взяті з запасом до живих значень (варіантів 3–5,
// сегментів 160–312, EXTINF 0.9–10 с): усе, що вище, — не серія аніме.
const (
	maxVariants   = 64
	maxSegments   = 20000
	maxSegmentSec = 3600.0
)

// Variant — один рядок якості з майстер-плейлиста.
type Variant struct {
	URL       string
	Bandwidth int
	Width     int
	Height    int
}

// Segment — один шматок медіаплейлиста і його тривалість із #EXTINF.
type Segment struct {
	URL string
	Sec float64
}

// Media — розібраний медіаплейлист: сегменти в порядку відтворення.
type Media struct {
	Segments []Segment
	TotalSec float64
}

// IsMaster відрізняє майстер від медіаплейлиста. Розрізняти доводиться за
// вмістом, а не за назвою файлу: хости віддають обидва як «index.m3u8».
func IsMaster(body []byte) bool {
	for _, ln := range lines(body) {
		if strings.HasPrefix(ln, "#EXT-X-STREAM-INF:") {
			return true
		}
	}
	return false
}

// ParseMaster повертає варіанти якості. Варіант із непридатним URL
// відкидається, а не валить розбір: решта якостей лишається придатною до
// завантаження, і краще дати людині 720p, ніж нічого.
func ParseMaster(base *url.URL, body []byte) ([]Variant, error) {
	ls, err := playlist(body)
	if err != nil {
		return nil, err
	}
	var out []Variant
	var pending Variant
	havePending := false
	seen, dropped := 0, 0
	for _, ln := range ls {
		switch {
		case ln == "":
			continue
		case strings.HasPrefix(ln, "#EXT-X-STREAM-INF:"):
			seen++
			if seen > maxVariants {
				return nil, fmt.Errorf("у майстер-плейлисті більше за %d варіантів: %w", maxVariants, errs.ErrProvider)
			}
			attrs := parseAttrs(strings.TrimPrefix(ln, "#EXT-X-STREAM-INF:"))
			w, h := parseResolution(attrs["RESOLUTION"])
			// Помилку Atoi ігноруємо свідомо: BANDWIDTH потрібен лише як
			// підказка для сортування, і його відсутність не робить варіант
			// непридатним.
			bw, _ := strconv.Atoi(attrs["BANDWIDTH"])
			pending = Variant{Bandwidth: bw, Width: w, Height: h}
			havePending = true
		case strings.HasPrefix(ln, "#"):
			// Решта тегів майстра (#EXT-X-MEDIA, #EXT-X-I-FRAME-STREAM-INF,
			// #EXT-X-INDEPENDENT-SEGMENTS, #EXT-X-VERSION) на склейку не
			// впливають: ми качаємо один варіант відео як є.
			continue
		default:
			if !havePending {
				continue // URI без свого тега — не варіант
			}
			havePending = false
			abs, err := resolveURL(base, ln)
			if err != nil || !extractor.ValidStreamURL(abs) {
				dropped++
				continue
			}
			pending.URL = abs
			out = append(out, pending)
		}
	}
	if len(out) == 0 {
		if dropped > 0 {
			return nil, fmt.Errorf("усі %d варіантів мають непридатні URL: %w", dropped, errs.ErrProvider)
		}
		return nil, fmt.Errorf("майстер-плейлист без жодного варіанта: %w", errs.ErrProvider)
	}
	return out, nil
}

// ParseMedia повертає сегменти в порядку відтворення.
func ParseMedia(base *url.URL, body []byte) (Media, error) {
	ls, err := playlist(body)
	if err != nil {
		return Media{}, err
	}
	var m Media
	var pendingSec float64
	havePending, endlist, event := false, false, false
	seen := 0
	for _, ln := range ls {
		switch {
		case ln == "":
			continue
		case strings.HasPrefix(ln, "#EXTINF:"):
			sec, err := parseExtinf(strings.TrimPrefix(ln, "#EXTINF:"))
			if err != nil {
				return Media{}, err
			}
			seen++
			if seen > maxSegments {
				return Media{}, fmt.Errorf("у плейлисті більше за %d сегментів: %w", maxSegments, errs.ErrProvider)
			}
			pendingSec, havePending = sec, true
		case strings.HasPrefix(ln, "#EXT-X-KEY:"):
			// Правило 8: шифрування не обходимо. METHOD=NONE — легальний
			// спосіб скасувати попередній ключ, він нічого не шифрує.
			if method := strings.ToUpper(parseAttrs(strings.TrimPrefix(ln, "#EXT-X-KEY:"))["METHOD"]); method != "NONE" {
				return Media{}, fmt.Errorf("сегменти зашифровано (METHOD=%s): %w", method, errs.ErrEncryptedStream)
			}
		case strings.HasPrefix(ln, "#EXT-X-MAP"):
			return Media{}, fmt.Errorf("fMP4 з окремою ініціалізацією (#EXT-X-MAP): %w", errs.ErrUnsupportedStream)
		case strings.HasPrefix(ln, "#EXT-X-BYTERANGE"):
			return Media{}, fmt.Errorf("сегменти діапазонами одного файлу (#EXT-X-BYTERANGE): %w", errs.ErrUnsupportedStream)
		case strings.HasPrefix(ln, "#EXT-X-PLAYLIST-TYPE:"):
			event = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(ln, "#EXT-X-PLAYLIST-TYPE:")), "EVENT")
		case ln == "#EXT-X-ENDLIST":
			endlist = true
		case strings.HasPrefix(ln, "#"):
			// #EXT-X-DISCONTINUITY дозволено й проігноровано: на перевірених
			// хостах його не траплялося, а якщо трапиться — проста
			// конкатенація сегментів усе одно програється, просто з можливим
			// стрибком таймкоду. Це не привід відмовляти людині у файлі.
			continue
		default:
			if !havePending {
				continue
			}
			havePending = false
			abs, err := resolveURL(base, ln)
			if err != nil {
				return Media{}, fmt.Errorf("сегмент %q: %w", ln, errs.ErrProvider)
			}
			// На відміну від майстра, непридатний сегмент валить увесь розбір:
			// дірка посеред склеєного файлу гірша за відсутність файлу — її
			// видно лише під час перегляду, коли завантаження вже «успішне».
			if !extractor.ValidStreamURL(abs) {
				return Media{}, fmt.Errorf("сегмент %q не пройшов перевірку URL: %w", abs, errs.ErrProvider)
			}
			m.Segments = append(m.Segments, Segment{URL: abs, Sec: pendingSec})
			m.TotalSec += pendingSec
		}
	}
	if event || !endlist {
		return Media{}, fmt.Errorf("плейлист без #EXT-X-ENDLIST — трансляція триває: %w", errs.ErrUnsupportedStream)
	}
	if len(m.Segments) == 0 {
		return Media{}, fmt.Errorf("медіаплейлист без сегментів: %w", errs.ErrProvider)
	}
	return m, nil
}

// parseAttrs розбирає список атрибутів тега з повагою до лапок: у
// CODECS="avc1.64001f,mp4a.40.2" кома належить значенню, а не розділяє
// атрибути. Порядок атрибутів довільний — живі хости дають і
// RESOLUTION-перший, і BANDWIDTH-перший.
func parseAttrs(s string) map[string]string {
	attrs := make(map[string]string)
	for i := 0; i < len(s); {
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := strings.ToUpper(strings.TrimSpace(s[i : i+eq]))
		i += eq + 1
		var val string
		if i < len(s) && s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				// Незакрита лапка: решта рядка — значення. Виправляти чужий
				// синтаксис ми не беремося, але й падати через нього не варто.
				if key != "" {
					attrs[key] = s[i+1:]
				}
				break
			}
			val = s[i+1 : i+1+end]
			i += end + 2
			if c := strings.IndexByte(s[i:], ','); c >= 0 {
				i += c + 1
			} else {
				i = len(s)
			}
		} else if c := strings.IndexByte(s[i:], ','); c >= 0 {
			val = s[i : i+c]
			i += c + 1
		} else {
			val, i = s[i:], len(s)
		}
		if key != "" {
			attrs[key] = strings.TrimSpace(val)
		}
	}
	return attrs
}

// parseResolution розбирає «1920x1080». Відсутній або кривий атрибут — це 0x0,
// а не помилка: варіант без RESOLUTION усе одно можна завантажити.
func parseResolution(s string) (int, int) {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "x")
	if !ok {
		return 0, 0
	}
	wi, err := strconv.Atoi(strings.TrimSpace(w))
	if err != nil {
		return 0, 0
	}
	hi, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil {
		return 0, 0
	}
	return wi, hi
}

// parseExtinf читає «<секунди>,<назва>» — назва необов'язкова.
func parseExtinf(s string) (float64, error) {
	num, _, _ := strings.Cut(s, ",")
	sec, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("#EXTINF:%s не число: %w", s, errs.ErrProvider)
	}
	if sec <= 0 || sec > maxSegmentSec {
		return 0, fmt.Errorf("тривалість сегмента %v поза межами (0, %v]: %w", sec, maxSegmentSec, errs.ErrProvider)
	}
	return sec, nil
}

// resolveURL доводить відносний URI до абсолютного відносно плейлиста.
func resolveURL(base *url.URL, raw string) (string, error) {
	if base == nil {
		return "", fmt.Errorf("немає базового URL плейлиста: %w", errs.ErrProvider)
	}
	u, err := base.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("URI %q: %w", raw, errs.ErrProvider)
	}
	return u.String(), nil
}

// playlist ріже тіло на рядки й перевіряє підпис формату.
func playlist(body []byte) ([]string, error) {
	ls := lines(body)
	for _, ln := range ls {
		if ln == "" {
			continue // порожні рядки перед підписом трапляються після конкатенацій
		}
		if ln != "#EXTM3U" {
			break
		}
		return ls, nil
	}
	return nil, fmt.Errorf("не схоже на m3u8: %w", errs.ErrProvider)
}

// lines знімає BOM і CRLF: обидва зустрічаються в живих відповідях, а
// невидимий BOM інакше псує перший же рядок із підписом.
func lines(body []byte) []string {
	s := strings.TrimPrefix(string(body), "\ufeff")
	out := strings.Split(s, "\n")
	for i, ln := range out {
		out[i] = strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
	}
	return out
}
