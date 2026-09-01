// Фікстурний режим: канонічні сторінки сайту, збережені в testdata/.
// UAANIME_FIXTURES=1 підміняє мережу цими файлами; make record-fixtures їх переписує.
package anitube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Канонічні фікстури. Кожен запис — реальний стан сайту на момент запису;
// імена кажуть, яку форму сторінки покриває фікстура, а не дату.
var canonicalTitles = []struct {
	File string // базове ім'я: <File>.html + <File>-playlists.json
	Slug string
}{
	{"title-multi-studio", "4465-frren-scho-provodzhaye-v-ostannyu-put-1-sezon"}, // 7 студій, тип→студія→плеєр
	{"title-ongoing", "5663-frren-scho-provodzhaye-v-ostannyu-put-2-sezon"},      // онгоїнг, часткові релізи
	{"title-single-release", "5808-pokoyivka-scho-lishe-yist"},                   // одна студія, лише субтитри
	{"title-dub-layout", "4304-sudzume-zachinyaye-dver"},                         // фільм, студія→тип→плеєр, ДУБЛЯЖ
}

const (
	searchFixtureQuery = "фрірен"
	searchPagedQuery   = "аніме"
)

// CanonicalRef повертає TitleRef канонічної фікстури за її ім'ям файлу.
func CanonicalRef(file string) provider.TitleRef {
	for _, t := range canonicalTitles {
		if t.File == file {
			return RefFromSlug(t.Slug)
		}
	}
	return provider.TitleRef{}
}

// FixtureTransport відповідає на запити до anitube.in.ua вмістом testdata-каталогу.
// Чужі домени пропускає (httpx.ErrSkip).
func FixtureTransport(dir string) http.RoundTripper {
	return fixtureTransport{dir: dir}
}

type fixtureTransport struct{ dir string }

func (t fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != strings.TrimPrefix(baseURL, "https://") {
		return nil, httpx.ErrSkip
	}
	switch {
	case req.URL.Query().Get("do") == "search":
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("читання запиту пошуку: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, fmt.Errorf("розбір запиту пошуку: %w", err)
		}
		switch form.Get("story") {
		case searchFixtureQuery:
			return t.serve("search.html")
		case searchPagedQuery:
			switch form.Get("search_start") {
			case "0", "1":
				return t.serve("search-paged.html")
			case "2":
				return t.serve("search-paged-2.html")
			}
		}
	case strings.HasSuffix(req.URL.Path, "playlists.php"):
		newsID := req.URL.Query().Get("news_id")
		for _, ct := range canonicalTitles {
			if strings.HasPrefix(ct.Slug, newsID+"-") {
				return t.serve(ct.File + "-playlists.json")
			}
		}
	case req.URL.Path == "/":
		// Головна — джерело обох добірок каталогу (топ сезону і новинки).
		return t.serve("catalog-fresh.html")
	default:
		for _, ct := range canonicalTitles {
			if strings.Contains(req.URL.Path, ct.Slug) {
				return t.serve(ct.File + ".html")
			}
		}
	}
	return nil, fmt.Errorf("фікстури не містять %s", req.URL)
}

func (t fixtureTransport) serve(name string) (*http.Response, error) {
	b, err := os.ReadFile(filepath.Join(t.dir, name))
	if err != nil {
		return nil, fmt.Errorf("фікстура %s: %w", name, err)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header:     http.Header{},
	}, nil
}

// RecordFixtures переписує канонічні фікстури з живого сайту. Запускається
// вручну через make record-fixtures, ніколи в CI.
func RecordFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	c := New(httpClient)

	if err := recordSearchFixture(ctx, c, dir, "search.html", searchFixtureQuery, 0, 1); err != nil {
		return err
	}

	for _, ct := range canonicalTitles {
		ref := RefFromSlug(ct.Slug)
		page, err := c.get(ctx, ref.URL, ref.URL)
		if err != nil {
			return fmt.Errorf("запис %s: %w", ct.File, err)
		}
		if err := os.WriteFile(filepath.Join(dir, ct.File+".html"), page, 0o644); err != nil {
			return err
		}
		hashMatch := reLoginHash.FindSubmatch(page)
		id, okID := newsID(ct.Slug, page)
		if hashMatch == nil || !okID {
			return fmt.Errorf("запис %s: не знайдено hash/news_id", ct.File)
		}
		ajaxURL := fmt.Sprintf("%s/engine/ajax/playlists.php?news_id=%s&xfield=playlist&user_hash=%s",
			baseURL, id, hashMatch[1])
		pl, err := c.get(ctx, ajaxURL, ref.URL)
		if err != nil {
			return fmt.Errorf("запис %s-playlists: %w", ct.File, err)
		}
		if err := os.WriteFile(filepath.Join(dir, ct.File+"-playlists.json"), pl, 0o644); err != nil {
			return err
		}
	}
	return recordNewFixtures(ctx, c, dir)
}

// RecordNewFixtures записує лише фікстури пагінації пошуку та каталогів.
func RecordNewFixtures(ctx context.Context, httpClient *http.Client, dir string) error {
	return recordNewFixtures(ctx, New(httpClient), dir)
}

func recordNewFixtures(ctx context.Context, c *Client, dir string) error {
	if err := recordSearchFixture(ctx, c, dir, "search-paged.html", searchPagedQuery, 0, 1); err != nil {
		return err
	}
	if err := recordSearchFixture(ctx, c, dir, "search-paged-2.html", searchPagedQuery, 2, 11); err != nil {
		return err
	}
	return recordCatalogFixture(ctx, c, dir, "catalog-fresh.html", baseURL+"/")
}

func recordSearchFixture(
	ctx context.Context,
	c *Client,
	dir, file, story string,
	searchStart, resultFrom int,
) error {
	form := url.Values{
		"do":           {"search"},
		"subaction":    {"search"},
		"search_start": {strconv.Itoa(searchStart)},
		"full_search":  {"0"},
		"result_from":  {strconv.Itoa(resultFrom)},
		"story":        {story},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/index.php?do=search", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("запис %s: %w", file, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setCommonHeaders(req, baseURL+"/")
	body, err := c.do(req)
	if err != nil {
		return fmt.Errorf("запис %s: %w", file, err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), body, 0o644); err != nil {
		return fmt.Errorf("запис %s: %w", file, err)
	}
	return nil
}

func recordCatalogFixture(ctx context.Context, c *Client, dir, file, catalogURL string) error {
	body, err := c.get(ctx, catalogURL, baseURL+"/")
	if err != nil {
		return fmt.Errorf("запис %s з %s: %w", file, catalogURL, err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), body, 0o644); err != nil {
		return fmt.Errorf("запис %s з %s: %w", file, catalogURL, err)
	}
	// Головна має нести обидва блоки; порожній — сигнал, що розмітка змінилася.
	top, err := parseTopSeason(body)
	if err != nil {
		return fmt.Errorf("перевірка %s з %s: %w", file, catalogURL, err)
	}
	fresh, err := parseFresh(body)
	if err != nil {
		return fmt.Errorf("перевірка %s з %s: %w", file, catalogURL, err)
	}
	if len(top) == 0 || len(fresh) == 0 {
		return fmt.Errorf("перевірка %s з %s: топ сезону %d, новинки %d — сайт змінив розмітку головної: %w",
			file, catalogURL, len(top), len(fresh), errs.ErrProvider)
	}
	return nil
}
