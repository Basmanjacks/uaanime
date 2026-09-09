package ui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

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

func (m *Model) episodesCmd(ref provider.TitleRef, req int, navigate bool) tea.Cmd {
	eng := m.eng
	return asyncCmd(30*time.Second, func(ctx context.Context) tea.Msg {
		eps, offline, err := eng.EpisodesCached(ctx, ref)
		return episodesDoneMsg{ref: ref, eps: eps, err: err, offline: offline, req: req, navigate: navigate}
	})
}

func (m *Model) bookmarkBaselineCmd(titleID string, ref provider.TitleRef, provisional int) tea.Cmd {
	eng := m.eng
	return asyncCmd(30*time.Second, func(ctx context.Context) tea.Msg {
		eps, err := eng.EpisodesFresh(ctx, ref)
		return bookmarkBaselineMsg{
			titleID: titleID, ref: ref, provisional: provisional,
			maxEp: maxEpisodeNumber(eps), err: err,
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
		choices, err := eng.StudioChoices(ctx, ref, ep)
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
	if m.eng == nil || m.eng.Provider == nil || m.eng.Lib == nil {
		return nil
	}
	var refs []provider.TitleRef
	for _, e := range m.eng.Lib.Entries {
		// Стан більше не фільтрує: переглянутий тайтл теж має дізнатися,
		// що вийшла нова серія.
		if e.Hidden {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		refs = append(refs, t.Sources[0])
		if len(refs) == maxBadgeProbes {
			break
		}
	}
	if len(refs) == 0 {
		return nil
	}

	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		var wg sync.WaitGroup
		jobs := make(chan provider.TitleRef)
		for range badgeWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for ref := range jobs {
					// Помилка нічого не ламає: лишається те, що вже в кеші.
					_, _, _ = eng.EpisodesCached(ctx, ref)
				}
			}()
		}
		for _, ref := range refs {
			jobs <- ref
		}
		close(jobs)
		wg.Wait()
		return libraryEpisodesMsg{}
	}
}

const (
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
