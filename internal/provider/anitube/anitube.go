// Package anitube — провайдер для anitube.in.ua (DLE-двигун).
//
// Структура сайту, перевірена 2026-08-31:
//   - пошук: POST /index.php?do=search (form: do, subaction, story…), результати —
//     <article class="story"> з <h2 itemprop="name"><a href="/<newsID>-<slug>.html">;
//   - сторінка тайтлу містить JS-змінну dle_login_hash; id новини — це числовий
//     префікс слага (JS-змінна news_id є не всюди, data-news_id — fallback);
//   - плейлисти: GET /engine/ajax/playlists.php?news_id=N&xfield=playlist&user_hash=H →
//     JSON {success, response}, response — HTML зі <li>: навігаційні (лише data-id)
//     і серії (data-file + data-id);
//   - ієрархія data-id НЕ фіксована: буває тип→студія→плеєр (Фрірен) і
//     студія→тип→плеєр (Судзуме). Рівень типу визначається за мітками
//     ДУБЛЯЖ/ОЗВУЧЕННЯ/СУБТИТРИ, решта рівнів — студія і плеєр;
//   - підписи плеєрів («ПЛЕЄР ASHDI», «ПЛЄЕР MOON») містять одруківки — хост
//     визначається з домену data-file, не з підпису;
//   - фільми підписані «Фільм» без номера серії — трактуємо як серію 1.
package anitube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

const (
	providerID   = "anitube"
	providerName = "AniTube"
	baseURL      = "https://anitube.in.ua"
)

type Client struct {
	http *http.Client
}

func New(httpClient *http.Client) *Client {
	return &Client{http: httpClient}
}

func (c *Client) ID() string   { return providerID }
func (c *Client) Name() string { return providerName }
func (c *Client) Caps() provider.Caps {
	return provider.Caps{Search: true, Subtitles: true}
}

// RefFromSlug відновлює TitleRef з самого слага (для headless-команд,
// де користувач передає title-id без повного URL).
func RefFromSlug(slug string) provider.TitleRef {
	return provider.TitleRef{
		Provider: providerID,
		Slug:     slug,
		URL:      baseURL + "/" + slug + ".html",
	}
}

var reSlug = regexp.MustCompile(`/(\d+-[^/]+)\.html`)

func (c *Client) Search(ctx context.Context, q string) ([]provider.TitleRef, error) {
	form := url.Values{
		"do":           {"search"},
		"subaction":    {"search"},
		"search_start": {"0"},
		"full_search":  {"0"},
		"result_from":  {"1"},
		"story":        {q},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/index.php?do=search", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setCommonHeaders(req, baseURL+"/")
	body, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("пошук: %w", err)
	}
	return parseSearch(body)
}

// Розмітка результату: <article class="story"> → <h2 itemprop="name"><a>.
func parseSearch(body []byte) ([]provider.TitleRef, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("пошук: розбір HTML: %w", err)
	}
	var refs []provider.TitleRef
	doc.Find(`article.story h2[itemprop="name"] a[href]`).Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		m := reSlug.FindStringSubmatch(href)
		name := strings.TrimSpace(s.Text())
		if m == nil || name == "" {
			return
		}
		refs = append(refs, provider.TitleRef{
			Provider: providerID,
			Slug:     m[1],
			Name:     name,
			URL:      href,
		})
	})
	return refs, nil
}

func (c *Client) Episodes(ctx context.Context, ref provider.TitleRef) ([]provider.Episode, error) {
	sources, err := c.allSources(ctx, ref)
	if err != nil {
		return nil, err
	}
	byEp := map[int]map[provider.Release]bool{}
	for _, s := range sources {
		if byEp[s.Episode] == nil {
			byEp[s.Episode] = map[provider.Release]bool{}
		}
		byEp[s.Episode][provider.Release{Studio: s.Studio, Kind: s.Kind}] = true
	}
	eps := make([]provider.Episode, 0, len(byEp))
	for num, rels := range byEp {
		ep := provider.Episode{Number: num}
		for r := range rels {
			ep.Releases = append(ep.Releases, r)
		}
		sort.Slice(ep.Releases, func(i, j int) bool {
			a, b := ep.Releases[i], ep.Releases[j]
			if a.Studio != b.Studio {
				return a.Studio < b.Studio
			}
			return a.Kind < b.Kind
		})
		eps = append(eps, ep)
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].Number < eps[j].Number })
	return eps, nil
}

func (c *Client) Sources(ctx context.Context, ref provider.TitleRef, episode int) ([]provider.Source, error) {
	all, err := c.allSources(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out []provider.Source
	for _, s := range all {
		if s.Episode == episode {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("серія %d: не знайдено жодного джерела", episode)
	}
	return out, nil
}

var (
	reLoginHash  = regexp.MustCompile(`dle_login_hash\s*=\s*'([0-9a-f]+)'`)
	reDataNewsID = regexp.MustCompile(`data-news_id="(\d+)"`)
	reSlugID     = regexp.MustCompile(`^(\d+)-`)
)

// newsID: у DLE слаг завжди починається з id новини («5808-pokoyivka…»);
// JS-змінної news_id на деяких сторінках немає, тому вона не використовується.
// Fallback — атрибут data-news_id блока плейлистів.
func newsID(slug string, page []byte) (string, bool) {
	if m := reSlugID.FindStringSubmatch(slug); m != nil {
		return m[1], true
	}
	if m := reDataNewsID.FindSubmatch(page); m != nil {
		return string(m[1]), true
	}
	return "", false
}

func (c *Client) allSources(ctx context.Context, ref provider.TitleRef) ([]provider.Source, error) {
	page, err := c.get(ctx, ref.URL, ref.URL)
	if err != nil {
		return nil, fmt.Errorf("сторінка тайтлу: %w", err)
	}
	hashMatch := reLoginHash.FindSubmatch(page)
	id, okID := newsID(ref.Slug, page)
	if hashMatch == nil || !okID {
		return nil, fmt.Errorf("сторінка тайтлу %s: не знайдено dle_login_hash/news_id (сайт змінив розмітку?)", ref.URL)
	}

	ajaxURL := fmt.Sprintf("%s/engine/ajax/playlists.php?news_id=%s&xfield=playlist&user_hash=%s",
		baseURL, id, hashMatch[1])
	body, err := c.get(ctx, ajaxURL, ref.URL)
	if err != nil {
		return nil, fmt.Errorf("плейлисти: %w", err)
	}

	var resp struct {
		Success  bool   `json:"success"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("плейлисти: не JSON: %w", err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("плейлисти: сайт відповів success=false")
	}
	return parsePlaylists(resp.Response, ref.URL)
}

func parsePlaylists(playlistHTML, titleURL string) ([]provider.Source, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(playlistHTML))
	if err != nil {
		return nil, fmt.Errorf("плейлисти: розбір HTML: %w", err)
	}

	labels := map[string]string{}
	doc.Find("li[data-id]").Each(func(_ int, s *goquery.Selection) {
		if _, hasFile := s.Attr("data-file"); hasFile {
			return
		}
		id, _ := s.Attr("data-id")
		labels[id] = strings.TrimSpace(s.Text())
	})

	var sources []provider.Source
	doc.Find("li[data-file]").Each(func(_ int, s *goquery.Selection) {
		file, _ := s.Attr("data-file")
		branch, _ := s.Attr("data-id")
		if file == "" {
			return
		}
		epNum, ok := episodeNumber(s.Text())
		if !ok {
			return
		}
		parts := strings.Split(branch, "_")
		if len(parts) < 3 {
			return
		}
		l1 := labels[strings.Join(parts[:2], "_")]
		l2 := labels[strings.Join(parts[:3], "_")]
		studio, kind := studioAndKind(l1, l2)
		if studio == "" {
			return // гілка поза відомою структурою — пропускаємо, не панікуємо
		}
		sources = append(sources, provider.Source{
			Studio:  studio,
			Kind:    kind,
			Embed:   file,
			Referer: titleURL,
			Episode: epNum,
		})
	})
	if len(sources) == 0 {
		return nil, fmt.Errorf("плейлисти: не знайдено жодної серії")
	}
	return sources, nil
}

// studioAndKind розкладає два рівні ієрархії на (студія, тип). Рівень типу
// впізнається за мітками ДУБЛЯЖ/ОЗВУЧЕННЯ/СУБТИТРИ; порядок рівнів на сайті плаває.
func studioAndKind(l1, l2 string) (string, provider.Kind) {
	k1, isType1 := kindFromLabel(l1)
	k2, isType2 := kindFromLabel(l2)
	switch {
	case isType1 && !isType2 && l2 != "":
		return l2, k1
	case isType2 && !isType1 && l1 != "":
		return l1, k2
	case !isType1 && l1 != "" && l2 == "":
		// студія без явного типу — доказів немає, sub не вгадуємо
		return l1, provider.KindMulti
	default:
		return "", provider.KindMulti
	}
}

func kindFromLabel(label string) (provider.Kind, bool) {
	up := strings.ToUpper(label)
	switch {
	case strings.Contains(up, "ДУБЛЯЖ"):
		return provider.KindDub, true
	case strings.Contains(up, "ОЗВУЧ"):
		return provider.KindVoiceover, true
	case strings.Contains(up, "СУБ"):
		return provider.KindSub, true
	}
	return provider.KindMulti, false
}

var reEpisode = regexp.MustCompile(`(\d+)\s*серія`)

// episodeNumber: «N серія» → N; підпис без номера («Фільм») → серія 1.
func episodeNumber(text string) (int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, false
	}
	m := reEpisode.FindStringSubmatch(text)
	if m == nil {
		return 1, true
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func setCommonHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", httpx.UserAgent)
	req.Header.Set("Referer", referer)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept-Language", "uk-UA,uk;q=0.9,en;q=0.5")
}

func (c *Client) get(ctx context.Context, rawURL, referer string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, referer)
	return c.do(req)
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", req.URL, res.StatusCode)
	}
	return io.ReadAll(res.Body)
}
