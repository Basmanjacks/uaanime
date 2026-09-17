package ui

import (
	"context"
	"sort"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// ---- асинхронні команди ----

// asyncCmd — фонова команда з власним дедлайном. Контекст не витікає за межі
// команди: далі за неї він нікому не потрібен, а cancel не можна забути.
func asyncCmd(timeout time.Duration, fn func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return fn(ctx)
	}
}

func (m *Model) searchCmd(q string, page, req int) tea.Cmd {
	eng := m.eng
	return asyncCmd(30*time.Second, func(ctx context.Context) tea.Msg {
		p, err := eng.Provider.Search(ctx, q, page)
		return searchDoneMsg{cards: p.Titles, hasMore: p.HasMore, page: page, err: err, req: req}
	})
}

// epsPurpose — навіщо просили список серій. Від цього залежить і джерело
// (кеш чи мережа), і те, як відповідь застосовується: навігація веде на екран
// серій, «Продовжити» лише запам'ятовує список, а оновлення — після перегляду
// чи по клавіші r — перемальовує список на місці й не має права ні закрити
// оверлей, ні скинути відкладений кадр.
type epsPurpose int

const (
	// epsResume — нульове значення навмисно: відповідь без призначення нікуди
	// не веде, як і колишній navigate=false.
	epsResume    epsPurpose = iota // «Продовжити»: список для екрана після перегляду
	epsOpen                        // відкрити тайтл
	epsAfterPlay                   // тиха перевірка після серії: «а наступна вже вийшла?»
	epsManual                      // клавіша r на екрані серій
)

// fresh — чи минати кеш: оновлення без мережі було б порожнім жестом.
func (p epsPurpose) fresh() bool { return p == epsAfterPlay || p == epsManual }

func (m *Model) episodesCmd(ref provider.TitleRef, req int, purpose epsPurpose) tea.Cmd {
	if m.deferEpisodeRequest(ref, req, purpose) {
		return nil
	}
	eng := m.eng
	gen := m.refreshGen
	return asyncCmd(30*time.Second, func(ctx context.Context) tea.Msg {
		var (
			eps     []provider.Episode
			offline bool
			err     error
		)
		if purpose.fresh() {
			eps, err = eng.EpisodesFresh(ctx, ref)
		} else {
			eps, offline, err = eng.EpisodesCached(ctx, ref)
		}
		return episodesDoneMsg{ref: ref, eps: eps, err: err, offline: offline, req: req, purpose: purpose, refreshGen: gen}
	})
}

func (m *Model) bookmarkBaselineCmd(titleID string, ref provider.TitleRef, provisional int) tea.Cmd {
	if m.bootstrapPending {
		m.deferredBookmark = &bookmarkRequest{titleID, ref, provisional}
		return nil
	}
	eng := m.eng
	return asyncCmd(30*time.Second, func(ctx context.Context) tea.Msg {
		eps, err := eng.EpisodesFresh(ctx, ref)
		return bookmarkBaselineMsg{
			titleID: titleID, ref: ref, provisional: provisional,
			maxEp: maxEpisodeNumber(eps), eps: eps, err: err,
		}
	})
}

// resolveCmd бере вже зняті підказки: читання бібліотеки лишається на
// горутині Update, у фон іде лише мережа.
func (m *Model) resolveCmd(ref provider.TitleRef, ep, req int, h playback.Hints) tea.Cmd {
	eng := m.eng
	return asyncCmd(45*time.Second, func(ctx context.Context) tea.Msg {
		res, err := eng.ResolveWith(ctx, ref, ep, h, nil)
		return resolvedMsg{res: res, err: err, req: req}
	})
}

func (m *Model) studiosCmd(ref provider.TitleRef, ep, req int) tea.Cmd {
	eng := m.eng
	return asyncCmd(45*time.Second, func(ctx context.Context) tea.Msg {
		choices, err := eng.ReleaseChoices(ctx, ref, ep)
		return studiosMsg{choices: choices, err: err, req: req}
	})
}

// catalogCmd оновлює один блок каталогу у фоні. Помилка мовчазна: домівка вже
// показана, і червоний рядок про недоступний топ сезону нічого не додає.
func (m *Model) catalogCmd(kind provider.CatalogKind) tea.Cmd {
	eng := m.eng
	return asyncCmd(10*time.Second, func(ctx context.Context) tea.Msg {
		cards, _, err := eng.CatalogCached(ctx, kind)
		if err != nil {
			return catalogMsg{kind: kind}
		}
		return catalogMsg{kind: kind, cards: cards}
	})
}

// libraryEpisodesCmd прогріває кеш списків серій для бібліотеки. Числа рядків
// рахуються з того самого кешу на диску, тож команда нічого не повертає, крім
// «дані оновилися» — жодного другого джерела правди, яке могло б застаріти.
//
// Один спільний дедлайн на всі перевірки й обмежений паралелізм: двадцять
// послідовних запитів тривали б довше, ніж людина дивиться на домівку, а
// двадцять одночасних виглядали б для сайту як атака.
func (m *Model) libraryEpisodesCmd() tea.Cmd {
	if m.eng == nil || m.eng.Provider == nil || m.eng.Store == nil || m.badgeScheduled.Load() || m.bootstrapPending {
		return nil
	}
	refs := m.visibleRefs()
	if len(refs) == 0 {
		return nil
	}
	if !m.badgeScheduled.CompareAndSwap(false, true) {
		return nil
	}
	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cursor := eng.Store.LoadBadgeCursor()
		sort.Slice(refs, func(i, j int) bool { return refKey(refs[i]) < refKey(refs[j]) })
		start := sort.Search(len(refs), func(i int) bool { return refKey(refs[i]) > cursor })
		ordered := append(append([]provider.TitleRef{}, refs[start:]...), refs[:start]...)
		jobsList := make([]provider.TitleRef, 0, maxBadgeProbes)
		for _, ref := range ordered {
			_, fresh, found := eng.Store.LoadEpisodes(ref)
			if found && fresh {
				continue
			}
			jobsList = append(jobsList, ref)
			if len(jobsList) == maxBadgeProbes {
				break
			}
		}
		var mu sync.Mutex
		var seeds []playback.ReleaseSeed
		var wg sync.WaitGroup
		jobs := make(chan provider.TitleRef)
		for range min(badgeWorkers, len(jobsList)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ref := range jobs {
					eps, _, err := eng.EpisodesCached(ctx, ref)
					if err == nil {
						mu.Lock()
						seeds = append(seeds, playback.ReleaseSeed{Ref: ref, Episodes: eps})
						mu.Unlock()
					}
				}
			}()
		}
		last := ""
	queue:
		for _, ref := range jobsList {
			select {
			case jobs <- ref:
				last = refKey(ref)
			case <-ctx.Done():
				break queue
			}
		}
		close(jobs)
		wg.Wait()
		if last != "" {
			_ = eng.Store.SaveBadgeCursor(last)
		}
		return libraryEpisodesMsg{seeds: seeds}
	}
}

const (
	// refreshInterval — крок фонового циклу оновлення бібліотеки й каталогу.
	// Мережа задіюється лише для записів, чий TTL сплив (година для серій), тож
	// тік у десять хвилин коштує читання диска, а нова серія доходить до
	// бейджів домівки протягом години без перезапуску застосунку.
	refreshInterval = 10 * time.Minute
	// refreshAllTimeout — дедлайн ручного «оновити зараз»: уся бібліотека без
	// ліміту на кількість тайтлів, тому щедріший за 15 с фонового пробігу.
	refreshAllTimeout = 60 * time.Second

	// liveTickInterval — крок оновлення рядка «закінчиться о». Це вже третій
	// споживач RC-каналу VLC поруч із журналом і пультом, а хвилина на екрані
	// змінюється рідше, ніж раз на п'ять секунд.
	liveTickInterval = 5 * time.Second
	// liveStartRetry / liveStartTries — Live.set стається в Engine.Run уже
	// ПІСЛЯ Player.Start, тому перший знімок може застати idle. Перепитуємо,
	// але скінченну кількість разів: плеєр міг і не піднятися.
	liveStartRetry = time.Second
	liveStartTries = 10
)

// refreshTickCmd — наступний крок фонового циклу; refreshEvery == 0 вимикає
// цикл (тести виконують команди синхронно, і тік заблокував би їх назавжди).
func (m *Model) refreshTickCmd() tea.Cmd {
	if m.refreshEvery <= 0 || m.eng == nil || m.eng.Provider == nil || m.eng.Store == nil {
		return nil
	}
	return tea.Tick(m.refreshEvery, func(time.Time) tea.Msg { return refreshTickMsg{} })
}

// refreshAllCmd — ручне «оновити зараз» з домівки: усі видимі тайтли
// бібліотеки повз TTL і без ліміту на кількість, плюс обидва блоки каталогу.
// Одна операція з однією відповіддю: статус «Оновлено» має значити, що
// оновилося все, а не лише бібліотека. Лише Store і Provider — правило 10.
func (m *Model) refreshAllCmd(gen int) tea.Cmd {
	eng := m.eng
	refs := m.visibleRefs()
	var kinds []provider.CatalogKind
	if m.catalogEnabled() {
		kinds = catalogKinds
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), refreshAllTimeout)
		defer cancel()
		var (
			mu       sync.Mutex
			seeds    []playback.ReleaseSeed
			firstErr error
			wg       sync.WaitGroup
		)
		note := func(err error) {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
		jobs := make(chan provider.TitleRef)
		for range min(badgeWorkers, len(refs)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ref := range jobs {
					eps, err := eng.EpisodesFresh(ctx, ref)
					if err != nil {
						note(err)
						continue
					}
					mu.Lock()
					seeds = append(seeds, playback.ReleaseSeed{Ref: ref, Episodes: eps})
					mu.Unlock()
				}
			}()
		}
	queue:
		for _, ref := range refs {
			select {
			case jobs <- ref:
			case <-ctx.Done():
				note(ctx.Err())
				break queue
			}
		}
		close(jobs)
		wg.Wait()
		catalog := map[provider.CatalogKind][]provider.TitleCard{}
		for _, kind := range kinds {
			cards, err := eng.CatalogFresh(ctx, kind)
			if err != nil {
				note(err)
				continue
			}
			catalog[kind] = cards
		}
		return refreshDoneMsg{seeds: seeds, catalog: catalog, err: firstErr, refreshGen: gen}
	}
}

// liveCmd — знімок сесії для екрана «Грає». Snapshot async-safe і Lib не
// торкається (правило 10). gen несе покоління сесії: відповідь, що приїхала
// вже після зміни серії, Update відкидає цілком.
func (m *Model) liveCmd(gen int, delay time.Duration, periodic bool) tea.Cmd {
	eng := m.eng
	// Без вікна в сесію знімок завжди буде порожній: не запускаємо ні цикл,
	// ні повтори — просто немає рядка.
	if eng == nil || eng.Live == nil {
		return nil
	}
	take := func() tea.Msg {
		snap, err := eng.Live.Snapshot()
		return liveMsg{periodic: periodic, gen: gen, snap: snap, err: err}
	}
	if delay == 0 {
		return take
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return take() })
}

// liveSnapshotCmd — знімок без затримки: старт сесії й кожна клавіша керування
// мусять оновити рядок одразу, а не за п'ять секунд.
func (m *Model) liveSnapshotCmd(gen int) tea.Cmd { return m.liveCmd(gen, 0, false) }

// liveTickCmd — черговий крок періодичного циклу; переозброюється лише з
// відповіді на попередній тік, тому паралельних циклів не буває.
func (m *Model) liveTickCmd(gen int) tea.Cmd { return m.liveCmd(gen, liveTickInterval, true) }

// liveRetryCmd — повтор для сесії, якої ще немає (див. liveStartRetry).
func (m *Model) liveRetryCmd(gen int) tea.Cmd { return m.liveCmd(gen, liveStartRetry, false) }

// remoteRequestCmd чекає на адресний запит пульта, що прийшов у простої. Команда
// блокується на каналі, як і всякий підписник у bubbletea, і переозброюється
// після кожного remotePlayMsg — так одночасно живе рівно один читач скриньки.
func (m *Model) remoteRequestCmd() tea.Cmd {
	if m.eng == nil || m.eng.Live == nil {
		return nil
	}
	requests := m.eng.Live.Requests()
	return func() tea.Msg { return remotePlayMsg{req: <-requests} }
}

func (m *Model) playCmd(res *playback.Resolved, titleID string) (tea.Cmd, context.CancelFunc) {
	eng := m.eng
	ctx, cancel := context.WithCancel(context.Background())
	// Latest state is enough: the observer must never wait for terminal input
	// or mutate the UI. Closing also releases a waiter after a failed start.
	events := make(chan error, 1)
	m.journalEvents = events
	observe := func(err error) {
		select {
		case events <- err:
		default:
			select {
			case <-events:
			default:
			}
			events <- err
		}
	}
	return func() tea.Msg {
		defer close(events)
		reason, err := eng.RunWithObserver(ctx, res, titleID, observe)
		return playDoneMsg{reason: reason, err: err}
	}, cancel
}

func (m *Model) journalCmd() tea.Cmd {
	events, gen := m.journalEvents, m.playGen
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		err, open := <-events
		return journalMsg{gen: gen, err: err, open: open}
	}
}

// downloadEventsCmd підписується на сигнал менеджера завантажень. Команда
// блокується на каналі, як remoteRequestCmd, і переозброюється з обробника
// downloadMsg — так одночасно живе рівно один читач. Закритий канал (Close)
// більше не переозброюється: далі змінюватись нічому.
func (m *Model) downloadEventsCmd() tea.Cmd {
	if m.dl == nil {
		return nil
	}
	events := m.dl.Events()
	return func() tea.Msg {
		_, ok := <-events
		return downloadMsg{ok: ok}
	}
}

// downloadPlanCmd — підготовка завантаження: той самий резолв, що й у
// відтворення (а отже той самий Pick і те саме правило 4), але з NoLocal —
// інакше після збереженого 720p неможливо було б докачати 1080p. Зондування
// потоків і розбір плейлистів робить BuildPlan; у фон іде лише мережа.
func (m *Model) downloadPlanCmd(ref provider.TitleRef, ep, req int, h playback.Hints) tea.Cmd {
	eng, f := m.eng, m.fetcher
	h.NoLocal = true
	return asyncCmd(45*time.Second, func(ctx context.Context) tea.Msg {
		res, err := eng.ResolveWith(ctx, ref, ep, h, nil)
		if err != nil {
			return downloadPlanMsg{req: req, err: err}
		}
		plan, err := download.BuildPlan(ctx, f, res.Streams)
		return downloadPlanMsg{req: req, res: res, plan: plan, err: err}
	})
}
