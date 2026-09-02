package ui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Basmanjacks/uaanime/internal/library"
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

// badgesCmd рахує нові серії для тайтлів у перегляді й запланованих. Один спільний
// дедлайн на всі перевірки й обмежений паралелізм: двадцять послідовних
// запитів тривали б довше, ніж людина дивиться на домівку, а двадцять
// одночасних виглядали б для сайту як атака.
func (m *Model) badgesCmd() tea.Cmd {
	if m.eng == nil || m.eng.Provider == nil || m.eng.Lib == nil {
		return nil
	}
	type probe struct {
		id       string
		ref      provider.TitleRef
		baseline int
	}
	var probes []probe
	for _, e := range m.eng.Lib.Entries {
		if e.Hidden || (e.State != library.StateWatching && e.State != library.StatePlanned) {
			continue
		}
		t := m.titleByID(e.TitleID)
		if t == nil || len(t.Sources) == 0 {
			continue
		}
		probes = append(probes, probe{
			id:       t.ID,
			ref:      t.Sources[0],
			baseline: max(e.LastEpisode, e.KnownEpisodes),
		})
		if len(probes) == maxBadgeProbes {
			break
		}
	}
	if len(probes) == 0 {
		return nil
	}

	eng := m.eng
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		counts := make(map[string]int, len(probes))
		var mu sync.Mutex
		var wg sync.WaitGroup
		jobs := make(chan probe)
		for range badgeWorkers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for p := range jobs {
					eps, _, err := eng.EpisodesCached(ctx, p.ref)
					if err != nil {
						continue // недоступний тайтл не ховає бейджі решти
					}
					mu.Lock()
					counts[p.id] = newEpisodeCount(eps, p.baseline)
					mu.Unlock()
				}
			}()
		}
		for _, p := range probes {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		return badgesMsg{counts: counts}
	}
}

func (m *Model) playCmd(res *playback.Resolved, titleID string) (tea.Cmd, context.CancelFunc) {
	eng := m.eng
	ctx, cancel := context.WithCancel(context.Background())
	return func() tea.Msg {
		reason, err := eng.Run(ctx, res, titleID)
		return playDoneMsg{reason: reason, err: err}
	}, cancel
}
