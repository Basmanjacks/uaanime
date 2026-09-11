// Package playback — оркестрація відтворення: вибір релізу за перевагами,
// екстракція потоку з fallback між джерелами, сесія плеєра з журналом прогресу.
// Спільний код для headless-команд і TUI; сам нічого не друкує.
//
// Правило потокобезпечності: library.Library НЕ потокобезпечна. Методи Engine,
// які читають або пишуть Lib, викликаються ЛИШЕ з горутини Update (TUI) або
// послідовно (CLI); асинхронна частина (tea.Cmd, фонові горутини) працює лише
// з мережею, плеєром і файлом журналу. Кожен метод нижче позначений
// «sync: торкається Lib» або «async-safe» — за цим маркуванням і треба вибирати,
// що можна запускати з команди, а що ні. Live (веб-пульт) — окремий випадок:
// усі його методи async-safe і до Lib не торкаються взагалі.
package playback

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

type Engine struct {
	watchedUndoEpoch uint64
	Provider         provider.Provider
	Extractors       []extractor.Extractor
	Store            *store.Store
	Lib              *library.Library
	Prefs            library.Prefs
	Player           player.Player
	PlayerFallback   bool
	Autoplay         bool
	// JournalInterval — крок семплювання позиції під час Run; 0 означає
	// defaultJournalInterval. Поле, а не пакетна змінна, щоб тести інших
	// пакетів не чекали 5 с на перший запис журналу.
	JournalInterval time.Duration
	// Live — вікно в поточну сесію для веб-пульта; nil, коли пульт вимкнено.
	Live *Live

	// memo джерел серії; Engine усюди використовується як покажчик
	sourcesMu    sync.Mutex
	sourcesCache map[sourceKey]sourcesEntry
}

// sourceKey містить серію, бо Provider.Sources повертає джерела ЛИШЕ
// запитаної серії (anitube фільтрує список перед поверненням). Name і URL
// у ключ не входять: та сама серія приходить і зі свіжої картки, і з
// library.json, де ці поля різні.
type sourceKey struct {
	Provider string
	Slug     string
	Episode  int
}

type sourcesEntry struct {
	at      time.Time
	sources []provider.Source
}

// sourcesTTL — скільки живе memo джерел. Кешуються embed-URL, тобто сторінки
// плеєра, а не підписані потоки: правило «URL потоку ніколи не кешується»
// не порушується, потік дістає екстрактор на кожен запуск. Без memo кожен
// Enter і кожен крок автоплею повторно тягнули й розбирали 180 КБ сторінки.
const sourcesTTL = 15 * time.Minute

// defaultJournalInterval — крок семплювання позиції: kill -9 коштує ≤ 10 с.
const defaultJournalInterval = 5 * time.Second

func (e *Engine) journalInterval() time.Duration {
	if e.JournalInterval > 0 {
		return e.JournalInterval
	}
	return defaultJournalInterval
}

// Event — немовні сигнали для інтерфейсу (текст додає той, хто показує).
type Event int

const (
	EventTryingNext Event = iota // джерело мертве, пробуємо наступне
)

// Resolved — все, що треба для запуску плеєра.
type Resolved struct {
	Ref     provider.TitleRef
	Episode int
	Source  provider.Source
	// Pin і Prefs — знімок, з яким робився вибір. Статус після запуску
	// будується лише з них: Begin уже записав неявний пін, і бібліотека далі
	// не відрізняє «піна не було» від явного.
	Pin        library.Pin
	Prefs      library.Prefs
	Deviation  library.Deviation
	Stream     extractor.Stream
	HostID     string
	StartSec   float64 // resume-позиція, 0 = з початку
	Name       string  // назва тайтлу без номера серії (для пульта)
	MediaTitle string  // Name · Episode — заголовок вікна плеєра
	// Candidates непорожній, коли на переможному ярусі >1 студії і піна немає:
	// інтерфейс може спитати один раз і закріпити. Source при цьому вже
	// детермінований — headless-режим грає без питань.
	Candidates []provider.Source
	// Playable — усі пари (студія, тип) серії з відомим екстрактором;
	// Failed — ті з них, чиє джерело впало саме зараз (хост не відповів,
	// потік порожній). Пікер показує обидва класи по-різному: перший можна
	// закріпити, другий — теж, але з попередженням.
	Playable []provider.Source
	Failed   []provider.Source
}

// Warning — попередження про відхилення від наміру користувача, спільне для
// TUI і CLI. ok == false — грає рівно те, чого хотіли (або відхилення між
// озвученими типами, яке видно в рядку сесії і не варте попередження).
func (r *Resolved) Warning() (text string, ok bool) {
	wantsSub := library.WantsSub(r.Pin, r.Prefs)
	sub := r.Source.Kind == provider.KindSub
	switch {
	case r.Deviation == library.DeviationStudio && sub && !wantsSub:
		return fmt.Sprintf(i18n.TuiStudioFallbackSub, r.Pin.Studio, r.Source.Studio), true
	case r.Deviation == library.DeviationStudio && r.Pin.Kind == provider.KindSub && !sub:
		return fmt.Sprintf(i18n.TuiSubsMissingStudio, r.Pin.Studio, r.Source.Studio), true
	case r.Deviation == library.DeviationStudio && r.Pin.Kind == provider.KindSub:
		return fmt.Sprintf(i18n.TuiSubsFallbackStudio, r.Pin.Studio, r.Source.Studio), true
	case r.Deviation == library.DeviationStudio:
		return fmt.Sprintf(i18n.TuiStudioFallback, r.Pin.Studio, r.Source.Studio), true
	case sub && !wantsSub && r.Pin.Studio != "":
		return fmt.Sprintf(i18n.TuiKindNotOutYetLong, r.Pin.Studio), true
	case sub && !wantsSub:
		return i18n.TuiOnlySubsStatus, true
	case r.Pin.Kind == provider.KindSub && !sub:
		return fmt.Sprintf(i18n.TuiSubsMissing, r.Pin.Studio), true
	}
	return "", false
}

func (e *Engine) playable(sources []provider.Source) []provider.Source {
	playable := make([]provider.Source, 0, len(sources))
	for _, source := range sources {
		if _, ok := extractor.Find(e.Extractors, source.Embed); ok {
			playable = append(playable, source)
		}
	}
	return playable
}

// sources — memo списку джерел серії (async-safe: мережа плюс власний м'ютекс).
// Кеш віддається, лише якщо він свіжий і справді містить потрібну серію.
func (e *Engine) sources(ctx context.Context, ref provider.TitleRef, ep int) ([]provider.Source, error) {
	key := sourceKey{Provider: ref.Provider, Slug: ref.Slug, Episode: ep}

	e.sourcesMu.Lock()
	cached, ok := e.sourcesCache[key]
	e.sourcesMu.Unlock()
	if ok && time.Since(cached.at) < sourcesTTL && hasEpisode(cached.sources, ep) {
		return cached.sources, nil
	}

	sources, err := e.Provider.Sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	e.sourcesMu.Lock()
	if e.sourcesCache == nil {
		e.sourcesCache = make(map[sourceKey]sourcesEntry)
	}
	e.sourcesCache[key] = sourcesEntry{at: time.Now(), sources: sources}
	e.sourcesMu.Unlock()
	return sources, nil
}

func hasEpisode(sources []provider.Source, ep int) bool {
	for _, s := range sources {
		if s.Episode == ep {
			return true
		}
	}
	return false
}

// validStreams — єдина точка перевірки URL потоку в застосунку: недовірений
// embed міг підставити адресу в локальну мережу, і плеєр пішов би туди.
func validStreams(streams []extractor.Stream) []extractor.Stream {
	out := make([]extractor.Stream, 0, len(streams))
	for _, s := range streams {
		if extractor.ValidStreamURL(s.URL) {
			out = append(out, s)
		}
	}
	return out
}

// ReleaseChoices повертає відтворювані пари (студія, тип) серії. async-safe.
func (e *Engine) ReleaseChoices(ctx context.Context, ref provider.TitleRef, ep int) ([]provider.Source, error) {
	sources, err := e.sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	choices := library.ReleaseChoices(e.playable(sources))
	if len(choices) == 0 {
		return nil, fmt.Errorf("серія %d: немає відтворюваних студій: %w", ep, errs.ErrNoStream)
	}
	return choices, nil
}

// Hints — знімок бібліотеки для одного Resolve. ЛИШЕ значення: покажчик або
// слайс із Library означав би, що фонова горутина далі читає її пам'ять.
type Hints struct {
	HasEntry  bool
	StudioPin string
	KindPin   provider.Kind
	StartSec  float64 // resume-позиція, 0 = з початку
	Name      string
	// Prefs — знімок глобальних переваг на момент запиту. ResolveWith біжить у
	// фоні, а Engine.Prefs може змінити екран налаштувань на Update-горутині:
	// копія в підказках замість читання поля рушія — і гонки немає.
	Prefs library.Prefs
}

// ResolveHints знімає з бібліотеки все, що потрібно ResolveWith.
// sync: читає Lib.
func (e *Engine) ResolveHints(ref provider.TitleRef, ep int) Hints {
	h := Hints{Name: ref.Name, Prefs: e.Prefs}
	title := e.Lib.TitleByRef(ref)
	if title == nil {
		return h
	}
	if entry := e.Lib.EntryLookup(title.ID); entry != nil {
		h.HasEntry = true
		h.StudioPin = entry.StudioPin
		h.KindPin = entry.KindPin
	}
	if p := e.Lib.ProgressFor(title.ID, ep); p != nil && !p.Completed && p.PositionSec > 0 {
		h.StartSec = p.PositionSec
	}
	if title.Name != "" {
		h.Name = title.Name
	}
	return h
}

// Resolve обирає реліз за перевагами і дістає потік; мертві джерела
// пропускає (onEvent(EventTryingNext)). Нічого не відтворює.
// sync: читає Lib (через ResolveHints) — для headless-режиму.
func (e *Engine) Resolve(ctx context.Context, ref provider.TitleRef, ep int, onEvent func(Event)) (*Resolved, error) {
	return e.ResolveWith(ctx, ref, ep, e.ResolveHints(ref, ep), onEvent)
}

// ResolveWith — та сама вибірка, але з уже знятими підказками: мережа й
// екстрактори, жодного звертання до Lib. async-safe.
func (e *Engine) ResolveWith(ctx context.Context, ref provider.TitleRef, ep int, h Hints, onEvent func(Event)) (*Resolved, error) {
	sources, err := e.sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("серія %d: провайдер не повернув джерел: %w", ep, errs.ErrNoStream)
	}
	name := h.Name

	pin := library.Pin{Studio: h.StudioPin, Kind: h.KindPin}
	remaining := sources
	var failures []error
	var failed []provider.Source // джерела з екстрактором, які впали цього разу
	for len(remaining) > 0 {
		chosen, candidates := library.Pick(remaining, pin, h.Prefs)
		if chosen == nil {
			failures = append(failures, fmt.Errorf("неможливо обрати реліз: %w", errs.ErrProvider))
			break
		}
		ex, ok := extractor.Find(e.Extractors, chosen.Embed)
		if !ok {
			failures = append(failures, fmt.Errorf("embed %q: немає підтримуваного екстрактора: %w", chosen.Embed, errs.ErrNoStream))
			remaining = without(remaining, *chosen)
			continue
		}
		streams, err := ex.Extract(ctx, chosen.Embed, chosen.Referer)
		if err != nil {
			failures = append(failures, err)
			if onEvent != nil {
				onEvent(EventTryingNext)
			}
			failed = append(failed, *chosen)
			remaining = without(remaining, *chosen)
			continue
		}
		streams = validStreams(streams)
		if len(streams) == 0 {
			failures = append(failures, fmt.Errorf("екстрактор %s не повернув потоку: %w", ex.ID(), errs.ErrNoStream))
			if onEvent != nil {
				onEvent(EventTryingNext)
			}
			failed = append(failed, *chosen)
			remaining = without(remaining, *chosen)
			continue
		}
		if name == "" {
			name = ref.Slug
		}
		return &Resolved{
			Ref:        ref,
			Episode:    ep,
			Source:     *chosen,
			Pin:        pin,
			Prefs:      h.Prefs,
			Deviation:  library.DeviationOf(pin, *chosen),
			Stream:     streams[0],
			HostID:     ex.ID(),
			StartSec:   h.StartSec,
			Name:       name,
			MediaTitle: fmt.Sprintf("%s · %d", name, ep),
			Candidates: e.playable(candidates),
			Playable:   library.ReleaseChoices(e.playable(sources)),
			Failed:     library.ReleaseChoices(failed),
		}, nil
	}
	return nil, aggregateFailures(ep, failures)
}

// Candidate — один придатний до відтворення потік (headless `resolve`).
type Candidate struct {
	Studio string           `json:"studio"`
	Kind   provider.Kind    `json:"kind"`
	Host   string           `json:"host"`
	Stream extractor.Stream `json:"stream"`
}

// ResolveAll дістає потоки з УСІХ джерел серії, не застосовуючи переваг:
// це діагностична команда, а не вибір релізу. async-safe.
func (e *Engine) ResolveAll(ctx context.Context, ref provider.TitleRef, ep int) ([]Candidate, error) {
	sources, err := e.sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("серія %d: провайдер не повернув джерел: %w", ep, errs.ErrNoStream)
	}
	var out []Candidate
	var failures []error
	for _, src := range sources {
		ex, ok := extractor.Find(e.Extractors, src.Embed)
		if !ok {
			failures = append(failures, fmt.Errorf("embed %q: немає підтримуваного екстрактора: %w", src.Embed, errs.ErrNoStream))
			continue
		}
		streams, err := ex.Extract(ctx, src.Embed, src.Referer)
		if err != nil {
			failures = append(failures, err)
			continue // одне мертве джерело не має ховати решту
		}
		streams = validStreams(streams)
		if len(streams) == 0 {
			failures = append(failures, fmt.Errorf("екстрактор %s не повернув потоку: %w", ex.ID(), errs.ErrNoStream))
			continue
		}
		for _, st := range streams {
			out = append(out, Candidate{Studio: src.Studio, Kind: src.Kind, Host: ex.ID(), Stream: st})
		}
	}
	if len(out) == 0 {
		return nil, aggregateFailures(ep, failures)
	}
	return out, nil
}

func aggregateFailures(ep int, failures []error) error {
	offline, noStream := 0, 0
	for _, err := range failures {
		switch {
		case errs.Offline(err):
			offline++
		case errors.Is(err, errs.ErrNoStream):
			noStream++
		}
	}
	class := errs.ErrProvider
	if len(failures) > 0 && offline == len(failures) {
		class = errs.ErrOffline
	} else if len(failures) > 0 && noStream == len(failures) {
		class = errs.ErrNoStream
	}
	return fmt.Errorf("серія %d: жодне джерело не дало потоку: %w", ep, errs.WithClass(class, errors.Join(failures...)))
}

// EpisodesCached — серії з кешем метаданих: свіжий кеш (< TTL) віддається без
// мережі; при відмові мережі кеш будь-якої давності — офлайн-fallback.
// offline=true лише коли мережа впала і показано застарілий кеш. async-safe.
func (e *Engine) EpisodesCached(ctx context.Context, ref provider.TitleRef) (eps []provider.Episode, offline bool, err error) {
	if cached, fresh, found := e.Store.LoadEpisodes(ref); found && fresh {
		return cached, false, nil
	}
	eps, err = e.Provider.Episodes(ctx, ref)
	if err != nil {
		if errs.Offline(err) {
			if cached, _, found := e.Store.LoadEpisodes(ref); found {
				return cached, true, nil
			}
		}
		return nil, false, err
	}
	_ = e.Store.SaveEpisodes(ref, eps)
	return eps, false, nil
}

// EpisodesFresh завжди питає провайдера й оновлює кеш; потрібен там, де
// локальна оцінка має бути звірена з актуальним списком серій. async-safe.
func (e *Engine) EpisodesFresh(ctx context.Context, ref provider.TitleRef) ([]provider.Episode, error) {
	eps, err := e.Provider.Episodes(ctx, ref)
	if err != nil {
		return nil, err
	}
	_ = e.Store.SaveEpisodes(ref, eps)
	return eps, nil
}

// NextEpisodeNumber знаходить найближчу наявну серію після поточної, навіть
// коли нумерація має пропуски або список не відсортований.
func NextEpisodeNumber(eps []provider.Episode, after int) (int, bool) {
	return library.NextEpisodeAfter(eps, after)
}

// CatalogCached — блок каталогу з кешем метаданих: свіжий кеш (< TTL)
// віддається без мережі; при відмові мережі кеш будь-якої давності —
// офлайн-fallback. offline=true лише коли мережа впала і показано застарілий кеш.
// async-safe.
func (e *Engine) CatalogCached(ctx context.Context, kind provider.CatalogKind) (cards []provider.TitleCard, offline bool, err error) {
	id := e.Provider.ID()
	if cached, fresh, found := e.Store.LoadCatalog(id, kind); found && fresh {
		return cached, false, nil
	}
	cards, err = e.Provider.Catalog(ctx, kind)
	if err != nil {
		if errs.Offline(err) {
			if cached, _, found := e.Store.LoadCatalog(id, kind); found {
				return cached, true, nil
			}
		}
		return nil, false, err
	}
	_ = e.Store.SaveCatalog(id, kind, cards)
	return cards, false, nil
}

// CatalogFresh завжди питає провайдера й оновлює кеш — для явного «оновити
// зараз», де свіжий кеш означає «нічого не робити», а людина чекає на дію.
// async-safe.
func (e *Engine) CatalogFresh(ctx context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error) {
	cards, err := e.Provider.Catalog(ctx, kind)
	if err != nil {
		return nil, err
	}
	_ = e.Store.SaveCatalog(e.Provider.ID(), kind, cards)
	return cards, nil
}

// PinStudio закріплює студію за тайтлом (відповідь на одноразове питання).
// sync: пише Lib.
func (e *Engine) PinStudio(ref provider.TitleRef, studio string, kind provider.Kind) error {
	title := e.Lib.EnsureTitle(ref, store.NewID)
	entry := e.Lib.EntryFor(title.ID)
	entry.StudioPin = studio
	entry.KindPin = kind
	return e.Store.SaveLibrary(e.Lib)
}

// KnownStudios — студії, які вже траплялися: піни бібліотеки та релізи серій
// із кешу на диску. Джерело для вибору улюбленої студії списком, без вводу тексту.
// sync: читає Lib.
func (e *Engine) KnownStudios() []string {
	seen := map[string]bool{}
	for _, entry := range e.Lib.Entries {
		if entry.StudioPin != "" {
			seen[entry.StudioPin] = true
		}
	}
	if e.Store != nil {
		for _, t := range e.Lib.Titles {
			if len(t.Sources) == 0 {
				continue
			}
			eps, _, found := e.Store.LoadEpisodes(t.Sources[0])
			if !found {
				continue
			}
			for _, ep := range eps {
				for _, r := range ep.Releases {
					if r.Studio != "" {
						seen[r.Studio] = true
					}
				}
			}
		}
	}
	studios := make([]string, 0, len(seen))
	for s := range seen {
		studios = append(studios, s)
	}
	sort.Strings(studios)
	return studios
}

// Bookmark перемикає тайтл у списку запланованого. sync: пише Lib.
func (e *Engine) Bookmark(ref provider.TitleRef, epCount int) (library.BookmarkResult, error) {
	prepared := e.Lib.Clone()
	title := prepared.EnsureTitle(ref, store.NewID)
	result := prepared.ToggleBookmark(title.ID, epCount)
	if episodes, _, found := e.Store.LoadEpisodes(ref); found {
		prepared.SeedReleaseBaseline(title.ID, episodes)
	}
	return result, e.publishLibrary(prepared)
}

// MarkSeen оновлює базову лінію лише для вже відомого локального тайтлу.
// sync: пише Lib.
func (e *Engine) MarkSeen(ref provider.TitleRef, maxEp int) error {
	title := e.Lib.TitleByRef(ref)
	if title == nil {
		return nil
	}
	e.Lib.MarkSeen(title.ID, maxEp)
	return e.Store.SaveLibrary(e.Lib)
}

// SetWatched ставить або знімає ручну позначку «переглянуто». sync: пише Lib.
//
// Не за зразком MarkSeen: той мовчки нічого не робить без локального тайтлу, а
// екран серій відкривається з пошуку чи каталогу — тобто ще до того, як тайтл
// з'явився в бібліотеці. Позначка має працювати з першого разу, тому тайтл
// створюється так само, як у Begin і Bookmark. Зняття позначки нічого не
// створює: знімати нема з чого.
func (e *Engine) SetWatched(ref provider.TitleRef, ep int, watched bool) error {
	prepared := e.Lib.Clone()
	var title *library.LocalTitle
	if watched {
		title = prepared.EnsureTitle(ref, store.NewID)
	} else if title = prepared.TitleByRef(ref); title == nil {
		return nil
	}
	prepared.SetWatched(title.ID, ep, watched, time.Now())
	if watched {
		e.acknowledgeCachedEpisode(prepared, ref, title.ID, ep)
	}
	return e.publishLibrary(prepared)
}

// ReconcileKnown зберігає уточнення, лише коли очікувана базова лінія не змінилась.
// sync: пише Lib.
func (e *Engine) ReconcileKnown(ref provider.TitleRef, provisional, actual int) error {
	title := e.Lib.TitleByRef(ref)
	if title == nil || !e.Lib.ReconcileKnown(title.ID, provisional, actual) {
		return nil
	}
	return e.Store.SaveLibrary(e.Lib)
}

// Result — підсумок сесії перегляду.
type Result struct {
	Reason       player.EndReason
	Completed    bool
	PositionSec  float64
	PinnedStudio string // студія, закріплена цим переглядом ("" — вже була)
	Intent       Intent // чого попросив користувач: none | next | stop | play
	// Requested заповнене лише при IntentPlay: яку саме серію просять.
	Requested PlayRequest
	// StopAfter — «досидіти й зупинитись» було увімкнене на цій серії, тож
	// ланцюжок автоплею далі не йде.
	StopAfter    bool
	SessionLimit SessionLimit
}

// Begin — синхронна половина запуску: тайтл і пін студії.
// Відсутність плеєра перевіряється ПЕРШИМ рядком, щоб бібліотека не мутувала
// заради сесії, якої не буде. sync: пише Lib.
func (e *Engine) Begin(res *Resolved) (titleID, pinnedStudio string, err error) {
	if e.Player == nil {
		return "", "", errs.ErrNoPlayer
	}
	e.InvalidateWatchedUndo()
	title := e.Lib.EnsureTitle(res.Ref, store.NewID)
	entry := e.Lib.EntryFor(title.ID)

	// студія запам'ятовується після першого перегляду: наступна серія
	// піде тією самою озвучкою без питань. Тип субтитрів неявно не
	// закріплюється: wildcard означає «озвучення цієї студії, щойно буде».
	if entry.StudioPin == "" {
		entry.StudioPin = res.Source.Studio
		entry.KindPin = res.Source.Kind
		if entry.KindPin == provider.KindSub {
			entry.KindPin = ""
		}
		pinnedStudio = res.Source.Studio
	}
	if err := e.Store.SaveLibrary(e.Lib); err != nil {
		return "", "", err
	}
	return title.ID, pinnedStudio, nil
}

// Run веде сесію плеєра: позиція семплюється раз на journalInterval і пишеться
// в журнал, лише коли вона змінилася або попередній запис не вдався.
// Скасування ctx закриває плеєр. async-safe: плеєр і файл журналу, без Lib.
func (e *Engine) Run(ctx context.Context, res *Resolved, titleID string) (player.EndReason, error) {
	return e.RunWithObserver(ctx, res, titleID, nil)
}

// RunWithObserver повідомляє про перший збій журналу та відновлення (nil),
// не перериваючи перегляд. Observer викликається синхронно в горутині Run:
// він не повинен блокувати або торкатися Lib. Помилка, яка не відновилась
// до завершення сесії, повертається викликачу. async-safe: без Lib.
func (e *Engine) RunWithObserver(ctx context.Context, res *Resolved, titleID string, observer func(error)) (player.EndReason, error) {
	sess, err := e.Player.Start(ctx, res.Stream.URL, res.MediaTitle, res.Stream.Headers, res.StartSec)
	if err != nil {
		return player.EndError, fmt.Errorf("%w: %w", errs.ErrPlayerStart, err)
	}
	defer sess.Close()
	e.Live.set(sess, res.Ref, res.Name, res.Episode, res.Source.Studio, res.Source.Kind)
	defer e.Live.clear()

	ticker := time.NewTicker(e.journalInterval())
	defer ticker.Stop()
	lastPos, sampled := 0.0, false
	var journalErr error
	for {
		select {
		case <-ticker.C:
			pos, err := sess.TimePos()
			if err != nil {
				continue // буферизація чи пауза — не привід падати
			}
			if sampled && pos == lastPos && journalErr == nil {
				continue
			}
			dur, _ := sess.Duration()
			err = e.Store.WriteJournal(&store.Journal{
				TitleID: titleID, Episode: res.Episode,
				PositionSec: pos, DurationSec: dur, UpdatedAt: time.Now(),
			})
			if err != nil {
				if journalErr == nil && observer != nil {
					observer(err)
				}
				journalErr = err
				continue
			}
			lastPos, sampled = pos, true
			if journalErr != nil {
				journalErr = nil
				if observer != nil {
					observer(nil)
				}
			}
		case reason := <-sess.End():
			if reason == player.EndError {
				return reason, errors.Join(errs.ErrPlayer, journalErr)
			}
			return reason, journalErr
		case <-ctx.Done():
			sess.Close()
			return player.EndQuit, journalErr
		}
	}
}

// Finish зливає журнал у бібліотеку й підсумовує сесію. PinnedStudio заповнює
// викликач із того, що повернув Begin. sync: читає й пише Lib.
func (e *Engine) Finish(reason player.EndReason, titleID string, ep int) (*Result, error) {
	return e.FinishRun(reason, titleID, ep, nil)
}

// FinishRun still recovers an older journal after startup failure, but only a
// started session may acknowledge newly available release metadata. sync: Lib.
func (e *Engine) FinishRun(reason player.EndReason, titleID string, ep int, runErr error) (*Result, error) {
	// намір і прапорець зупинки забираються до будь-якого раннього виходу —
	// рівно один раз, інакше вони протекли б у наступну серію
	intent, requested, limit := e.Live.finishSession(reason)
	out := &Result{Reason: reason, Intent: intent, Requested: requested, SessionLimit: limit, StopAfter: limit.Enabled && limit.Remaining == 0}
	if _, err := e.Store.RecoverJournal(e.Lib); err != nil {
		return out, err
	}
	prepared := e.Lib.Clone()
	p := prepared.ProgressFor(titleID, ep)
	changed := false
	if p == nil && reason == player.EndEOF {
		p = prepared.RecordPosition(titleID, ep, 0, 0, time.Now())
		changed = true
	}
	if p == nil {
		return out, nil
	}
	if reason == player.EndEOF && !p.Completed {
		p.Completed = true
		changed = true
	}
	if !errors.Is(runErr, errs.ErrPlayerStart) && (p.PositionSec > 0 || p.Completed) {
		for _, title := range prepared.Titles {
			if title.ID == titleID && len(title.Sources) > 0 {
				changed = e.acknowledgeCachedEpisode(prepared, title.Sources[0], titleID, ep) || changed
				break
			}
		}
	}
	if changed {
		if err := e.publishLibrary(prepared); err != nil {
			return out, err
		}
	}
	out.Completed, out.PositionSec = p.Completed, p.PositionSec
	return out, nil
}

// Play — послідовний сценарій для headless-команди: Begin → Run → Finish.
// sync: через Begin і Finish торкається Lib.
func (e *Engine) Play(ctx context.Context, res *Resolved) (*Result, error) {
	return e.PlayWithObserver(ctx, res, nil)
}

// PlayWithObserver is the sequential CLI path with journal state feedback.
// The observer follows RunWithObserver's rules; Begin and Finish remain sync.
func (e *Engine) PlayWithObserver(ctx context.Context, res *Resolved, observer func(error)) (*Result, error) {
	titleID, pinned, err := e.Begin(res)
	if err != nil {
		return nil, err
	}
	reason, runErr := e.RunWithObserver(ctx, res, titleID, observer)
	out, finishErr := e.FinishRun(reason, titleID, res.Episode, runErr)
	out.PinnedStudio = pinned
	return out, errors.Join(runErr, finishErr)
}

func without(sources []provider.Source, drop provider.Source) []provider.Source {
	out := make([]provider.Source, 0, len(sources))
	for _, s := range sources {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// Metadata is local and read-only here: finish must never wait on the provider.
func (e *Engine) acknowledgeCachedEpisode(lib *library.Library, ref provider.TitleRef, id string, number int) bool {
	episodes, _, found := e.Store.LoadEpisodes(ref)
	if !found {
		return false
	}
	seeded := lib.SeedReleaseBaseline(id, episodes)
	var watched []provider.Episode
	for _, ep := range episodes {
		if ep.Number == number {
			watched = append(watched, ep)
		}
	}
	return lib.AcknowledgeReleases(id, watched) || seeded
}
