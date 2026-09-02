// Package anitube — провайдер для anitube.in.ua (DLE-двигун).
//
// Структура сайту, перевірена 2026-08-31:
//   - пошук: POST /index.php?do=search (form: do, subaction, story…), результати —
//     <article class="story"> з <h2 itemprop="name"><a href="/<newsID>-<slug>.html">;
//   - пагінація пошуку (перевірено 2026-08-31): перша сторінка передає
//     search_start=0/result_from=1, наступні — номер сторінки та зміщення по 10;
//   - каталог: окремих URL немає — обидві добірки лежать на головній GET /
//     (сезонний топ у div.box з h2 «Найкраще», новинки — у div.news_2);
//   - сторінка тайтлу містить JS-змінну dle_login_hash; id новини — це числовий
//     префікс слага (JS-змінна news_id є не всюди, data-news_id — fallback);
//   - плейлисти (перевірено 2026-09-01): GET /engine/ajax/playlists.php?news_id=N&xfield=playlist&user_hash=H →
//     JSON {success, response}, response — HTML зі <li>: навігаційні (лише data-id)
//     і серії (data-file + data-id);
//   - ієрархія data-id НЕ фіксована: буває тип→студія→плеєр (Фрірен),
//     студія→тип→плеєр (Судзуме) і плоска плеєр→серії без рівнів студії/типу
//     (OVA «Атаки титанів»). Рівень типу визначається за мітками
//     ДУБЛЯЖ/ОЗВУЧЕННЯ/СУБТИТРИ, а студія плоского плейлиста — з поля «Переклад»;
//   - підписи плеєрів («ПЛЕЄР ASHDI», «ПЛЄЕР MOON») містять одруківки — хост
//     визначається з домену data-file, не з підпису;
//   - фільми підписані «Фільм» без номера серії — трактуємо як серію 1.
package anitube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

const (
	providerID    = "anitube"
	providerName  = "AniTube"
	baseURL       = "https://anitube.in.ua"
	searchPerPage = 10
	catalogLimit  = 20
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
	return provider.Caps{Search: true, Catalog: true}
}

// titleURL — єдине місце, де народжується URL сторінки тайтлу. Він завжди
// будується зі слага і ніколи не береться з розмітки сайту чи з диска.
func titleURL(slug string) string { return baseURL + "/" + slug + ".html" }

// RefFromSlug відновлює TitleRef з самого слага (для headless-команд,
// де користувач передає title-id без повного URL). Слаг — недовірений вхід,
// тому валідується тим самим шаблоном, що й слаг зі сторінки.
func RefFromSlug(slug string) (provider.TitleRef, error) {
	if !provider.ValidSlug(slug) {
		return provider.TitleRef{}, fmt.Errorf("невалідний title-id %q: %w", slug, errs.ErrProvider)
	}
	return provider.TitleRef{
		Provider: providerID,
		Slug:     slug,
		URL:      titleURL(slug),
	}, nil
}

// reSlug звужений до реального формату DLE (id-слово): широкий `[^/]+` пропускав
// у слаг будь-що, включно з traversal-послідовностями і не-ascii.
var reSlug = regexp.MustCompile(`/(\d+-[A-Za-z0-9_-]+)\.html`)

// searchRequest будує POST-форму пошуку. Її ж використовує запис фікстур —
// форма мусить бути однією, інакше фікстура перестає відповідати реальному запиту.
func searchRequest(ctx context.Context, story string, searchStart, resultFrom int) (*http.Request, error) {
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
		return nil, fmt.Errorf("пошук: створення запиту: %w: %w", errs.ErrProvider, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setCommonHeaders(req, baseURL+"/")
	return req, nil
}

// playlistsURL — AJAX-ендпоінт зі списком плейлистів тайтлу (спільний із записом фікстур).
func playlistsURL(newsID, loginHash string) string {
	return fmt.Sprintf("%s/engine/ajax/playlists.php?news_id=%s&xfield=playlist&user_hash=%s",
		baseURL, newsID, loginHash)
}

func (c *Client) Search(ctx context.Context, q string, page int) (provider.Page, error) {
	searchStart := 0
	resultFrom := 1
	if page >= 2 {
		searchStart = page
		resultFrom = (page-1)*searchPerPage + 1
	}
	req, err := searchRequest(ctx, q, searchStart, resultFrom)
	if err != nil {
		return provider.Page{}, err
	}
	body, err := httpx.Do(c.http, req)
	if err != nil {
		return provider.Page{}, fmt.Errorf("пошук: %w", err)
	}
	cards, err := parseCards(body)
	if err != nil {
		return provider.Page{}, err
	}
	return provider.Page{Titles: cards, HasMore: len(cards) == searchPerPage}, nil
}

// Catalog: обидві добірки живуть на одній сторінці — головній. Окремих URL для
// них сайт не має, тому вгадані фільтри (/f/year=…) прибрано: один GET "/" і два
// різні парсери блоків.
func (c *Client) Catalog(ctx context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error) {
	var parse func([]byte) ([]provider.TitleCard, error)
	switch kind {
	case provider.CatalogTopSeason:
		parse = parseTopSeason
	case provider.CatalogFresh:
		parse = parseFresh
	default:
		return nil, fmt.Errorf("невідомий каталог %q: %w", kind, errs.ErrProvider)
	}

	body, err := c.get(ctx, baseURL+"/", baseURL+"/")
	if err != nil {
		return nil, fmt.Errorf("каталог %q: %w", kind, err)
	}
	cards, err := parse(body)
	if err != nil {
		return nil, fmt.Errorf("каталог %q: %w", kind, err)
	}
	if len(cards) > catalogLimit {
		cards = cards[:catalogLimit]
	}
	return cards, nil
}

// Блоки головної не несуть ні року, ні рейтингу — лише назву й посилання.
// Решта полів TitleCard свідомо лишається нульовою: вигадувати метадані нема з чого.
//
// З href береться ЛИШЕ слаг, а URL будується наново: ворожа сторінка могла б
// підсунути href на http://127.0.0.1/… — і застосунок сам сходив би туди
// (allSources) та зберіг би це в library.json.
func cardFromAnchor(link *goquery.Selection) (provider.TitleCard, bool) {
	href, _ := link.Attr("href")
	m := reSlug.FindStringSubmatch(href)
	name := provider.CleanText(link.Text())
	if m == nil || name == "" {
		return provider.TitleCard{}, false
	}
	return provider.TitleCard{TitleRef: provider.TitleRef{
		Provider: providerID, Slug: m[1], Name: name, URL: titleURL(m[1]),
	}}, true
}

// Розмітку головної перевірено 2026-08-31. Сезонний топ:
//
//	<div class="box hidden"><h2>Найкраще<span> аніме літнього сезону</span>…</h2>
//	  <div class="example horizontal"><div class="carousel">…
//	    <ul class="portfolio_items"><li>…<div class="text_content"><a href="…">Назва</a>
//
// Заголовок сезону змінюється разом із сезоном, тому прив'язка — до слова
// «Найкраще», а не до повного тексту h2.
func parseTopSeason(body []byte) ([]provider.TitleCard, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("топ сезону: розбір HTML: %w: %w", errs.ErrProvider, err)
	}
	var cards []provider.TitleCard
	doc.Find("div.box").EachWithBreak(func(_ int, box *goquery.Selection) bool {
		if !strings.Contains(box.Find("h2").First().Text(), "Найкраще") {
			return true
		}
		box.Find("ul.portfolio_items li div.text_content a[href]").Each(func(_ int, link *goquery.Selection) {
			if card, ok := cardFromAnchor(link); ok {
				cards = append(cards, card)
			}
		})
		return false
	})
	return cards, nil
}

// Новинки (перевірено 2026-08-31): після <h2>Новинки…</h2> ідуть повторювані
//
//	<div class="news_2"><div class="title2" title="…"><a href="…">Назва</a></div>…
//
// Той самий тайтл трапляється і в карусельному топі, і в новинках, а всередині
// блоку — двічі (заголовок і постер), тому дедуплікуємо за слагом зі збереженням порядку.
func parseFresh(body []byte) ([]provider.TitleCard, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("новинки: розбір HTML: %w: %w", errs.ErrProvider, err)
	}
	seen := make(map[string]bool)
	var cards []provider.TitleCard
	doc.Find("div.news_2 div.title2 a[href]").Each(func(_ int, link *goquery.Selection) {
		card, ok := cardFromAnchor(link)
		if !ok || seen[card.Slug] {
			return
		}
		seen[card.Slug] = true
		cards = append(cards, card)
	})
	return cards, nil
}

var (
	reEpisodes = regexp.MustCompile(`(\d+)(?:\s*з\s*(\d+))?`)
	reRating   = regexp.MustCompile(`(\d+[.,]?\d*)\s*/\s*10\D*?(\d+)`)
	reStudio   = regexp.MustCompile(`\(([^)]+)\)`)
)

// Структуру сторінки пошуку перевірено 2026-08-31: кожна картка — окремий
// article.story з обов'язковим h2[itemprop="name"] a[href]; метадані лежать у
// сусідніх story_infa, story_c_rate, dubsub і story_link усередині тієї ж картки.
func parseCards(body []byte) ([]provider.TitleCard, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("пошук: розбір HTML: %w: %w", errs.ErrProvider, err)
	}
	var cards []provider.TitleCard
	doc.Find(`article.story`).Each(func(_ int, article *goquery.Selection) {
		card, ok := cardFromAnchor(article.Find(`h2[itemprop="name"] a[href]`).First())
		if !ok {
			return
		}
		info := article.Find(`div.story_infa`).First()
		card.Year, _ = strconv.Atoi(fieldAfterLabel(info, "Рік виходу аніме:"))
		card.Episodes, card.EpAired, card.EpTotal = parseEpisodes(fieldAfterLabel(info, "Серій:"))
		card.Genres = splitTrimmed(fieldAfterLabel(info, "Категорія:"))

		rateText := strings.ReplaceAll(article.Find(`div.story_c_rate`).First().Text(), "\u00a0", " ")
		if rating := reRating.FindStringSubmatch(rateText); rating != nil {
			card.Rating, _ = strconv.ParseFloat(strings.ReplaceAll(rating[1], ",", "."), 64)
			card.Votes, _ = strconv.Atoi(rating[2])
		}
		dubsub := article.Find(`div.dubsub`).First().Text()
		card.HasDub = strings.Contains(dubsub, "D")
		card.HasSub = strings.Contains(dubsub, "S")
		card.Studios = parseStudios(article.Find(`span.story_link`).First().Text())
		cards = append(cards, card)
	})
	return cards, nil
}

// У story_infa, перевіреному 2026-08-31, значення є сусідніми вузлами після
// dt і закінчуються перед hr; обхід вузлів зберігає також текст поза тегами.
func fieldAfterLabel(info *goquery.Selection, label string) string {
	var value string
	info.Find("dt").EachWithBreak(func(_ int, dt *goquery.Selection) bool {
		if provider.CleanText(dt.Text()) != label || len(dt.Nodes) == 0 {
			return true
		}
		// empty — порожня вибірка того ж документа: AddNodes на ній дає текст
		// одного вузла без NewDocumentFromNode, який будував цілий документ
		// на КОЖНОГО сусіда (а їх у картці десятки).
		empty := dt.Slice(0, 0)
		var parts []string
		for node := dt.Nodes[0].NextSibling; node != nil && node.Data != "hr" && node.Data != "dt"; node = node.NextSibling {
			text := provider.CleanText(empty.AddNodes(node).Text())
			if text != "" {
				parts = append(parts, text)
			}
		}
		value = strings.Join(parts, " ")
		return false
	})
	return value
}

func parseEpisodes(text string) (string, int, int) {
	match := reEpisodes.FindStringSubmatch(text)
	if match == nil {
		return "", 0, 0
	}
	aired, _ := strconv.Atoi(match[1])
	if match[2] == "" {
		return match[1], aired, aired
	}
	total, _ := strconv.Atoi(match[2])
	return match[1] + " з " + match[2], aired, total
}

func splitTrimmed(text string) []string {
	var values []string
	for _, value := range strings.Split(text, ",") {
		if value = provider.CleanText(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func parseStudios(text string) []string {
	seen := make(map[string]bool)
	var studios []string
	for _, match := range reStudio.FindAllStringSubmatch(text, -1) {
		studio := provider.CleanText(match[1])
		if studio != "" && !seen[studio] {
			seen[studio] = true
			studios = append(studios, studio)
		}
	}
	return studios
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
		return nil, fmt.Errorf("серія %d: не знайдено жодного джерела: %w", episode, errs.ErrNoStream)
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

// allSources свідомо ігнорує ref.URL: він приходить із library.json або з кешу,
// тобто з диска, і може бути порожнім чи вказувати на чужий хост. Єдине надійне
// поле ідентичності — слаг, тому адреса будується з нього.
func (c *Client) allSources(ctx context.Context, ref provider.TitleRef) ([]provider.Source, error) {
	if !provider.ValidSlug(ref.Slug) {
		return nil, fmt.Errorf("невалідний слаг %q: %w", ref.Slug, errs.ErrProvider)
	}
	pageURL := titleURL(ref.Slug)
	page, err := c.get(ctx, pageURL, pageURL)
	if err != nil {
		return nil, fmt.Errorf("сторінка тайтлу: %w", err)
	}
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page))
	if err != nil {
		return nil, fmt.Errorf("сторінка тайтлу: розбір HTML: %w: %w", errs.ErrProvider, err)
	}
	fallbackStudio := provider.CleanText(doc.Find(`a[href*="/xfsearch/translation/"]`).First().Text())
	hashMatch := reLoginHash.FindSubmatch(page)
	id, okID := newsID(ref.Slug, page)
	if hashMatch == nil || !okID {
		return nil, fmt.Errorf("сторінка тайтлу %s: не знайдено dle_login_hash/news_id (сайт змінив розмітку?): %w", pageURL, errs.ErrProvider)
	}

	body, err := c.get(ctx, playlistsURL(id, string(hashMatch[1])), pageURL)
	if err != nil {
		return nil, fmt.Errorf("плейлисти: %w", err)
	}

	var resp struct {
		Success  bool   `json:"success"`
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("плейлисти: не JSON: %w: %w", errs.ErrProvider, err)
	}
	if !resp.Success {
		return nil, fmt.Errorf("плейлисти: сайт відповів success=false: %w", errs.ErrProvider)
	}
	return parsePlaylists(resp.Response, pageURL, fallbackStudio)
}

func parsePlaylists(playlistHTML, titleURL, fallbackStudio string) ([]provider.Source, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(playlistHTML))
	if err != nil {
		return nil, fmt.Errorf("плейлисти: розбір HTML: %w: %w", errs.ErrProvider, err)
	}

	// Назва студії з будь-якої гілки йде просто в список вибору озвучки, тому
	// чистяться обидва її джерела — мітки плейлиста і резервна студія зі сторінки.
	fallbackStudio = provider.CleanText(fallbackStudio)
	labels := map[string]string{}
	doc.Find("li[data-id]").Each(func(_ int, s *goquery.Selection) {
		if _, hasFile := s.Attr("data-file"); hasFile {
			return
		}
		id, _ := s.Attr("data-id")
		labels[id] = provider.CleanText(s.Text())
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
		if len(parts) < 2 {
			return
		}

		var navLabels []string
		for depth := 2; depth < len(parts); depth++ {
			navLabels = append(navLabels, labels[strings.Join(parts[:depth], "_")])
		}

		studio := ""
		kind := provider.KindMulti
		switch len(navLabels) {
		case 0:
			studio = fallbackStudio
		case 1:
			if labelKind, isType := kindFromLabel(navLabels[0]); isType {
				studio, kind = fallbackStudio, labelKind
			} else {
				studio = navLabels[0]
			}
		default:
			studio, kind = studioAndKind(navLabels[0], navLabels[1])
		}
		if studio == "" {
			studio = labels[branch]
		}
		if studio == "" {
			return
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
		return nil, fmt.Errorf("плейлисти: не знайдено жодної серії: %w", errs.ErrProvider)
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
		return nil, fmt.Errorf("створення запиту %s: %w: %w", rawURL, errs.ErrProvider, err)
	}
	setCommonHeaders(req, referer)
	return httpx.Do(c.http, req)
}
