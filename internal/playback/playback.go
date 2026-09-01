// Package playback — оркестрація відтворення: вибір релізу за перевагами,
// екстракція потоку з fallback між джерелами, сесія mpv із журналом прогресу.
// Спільний код для headless-команд і TUI; сам нічого не друкує.
package playback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

type Engine struct {
	Provider       provider.Provider
	Extractors     []extractor.Extractor
	Store          *store.Store
	Lib            *library.Library
	Prefs          library.Prefs
	Player         player.Player
	PlayerFallback bool
	Autoplay       bool
}

// Event — немовні сигнали для інтерфейсу (текст додає той, хто показує).
type Event int

const (
	EventTryingNext Event = iota // джерело мертве, пробуємо наступне
)

// Resolved — все, що треба для запуску плеєра.
type Resolved struct {
	Ref        provider.TitleRef
	Episode    int
	Source     provider.Source
	Stream     extractor.Stream
	HostID     string
	StartSec   float64 // resume-позиція, 0 = з початку
	MediaTitle string
	// Candidates непорожній, коли на переможному ярусі >1 студії і піна немає:
	// інтерфейс може спитати один раз і закріпити. Source при цьому вже
	// детермінований — headless-режим грає без питань.
	Candidates []provider.Source
}

// Resolve обирає реліз за перевагами і дістає потік; мертві джерела
// пропускає (onEvent(EventTryingNext)). Нічого не відтворює.
func (e *Engine) Resolve(ctx context.Context, ref provider.TitleRef, ep int, onEvent func(Event)) (*Resolved, error) {
	sources, err := e.Provider.Sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("серія %d: провайдер не повернув джерел: %w", ep, errs.ErrNoStream)
	}
	var entry *library.Entry
	var startSec float64
	name := ref.Name
	if title := e.Lib.TitleByRef(ref); title != nil {
		entry = e.Lib.EntryLookup(title.ID)
		if p := e.Lib.ProgressFor(title.ID, ep); p != nil && !p.Completed && p.PositionSec > 0 {
			startSec = p.PositionSec
		}
		if title.Name != "" {
			name = title.Name
		}
	}

	remaining := sources
	var failures []error
	for len(remaining) > 0 {
		chosen, candidates := library.Pick(remaining, entry, e.Prefs)
		if chosen == nil {
			failures = append(failures, fmt.Errorf("неможливо обрати реліз: %w", errs.ErrProvider))
			break
		}
		ex, ok := extractor.Find(e.Extractors, chosen.Embed)
		if !ok {
			failures = append(failures, fmt.Errorf("embed %s: немає підтримуваного екстрактора: %w", chosen.Embed, errs.ErrNoStream))
			remaining = without(remaining, *chosen)
			continue
		}
		streams, err := ex.Extract(ctx, chosen.Embed, chosen.Referer)
		if err != nil {
			failures = append(failures, err)
			if onEvent != nil {
				onEvent(EventTryingNext)
			}
			remaining = without(remaining, *chosen)
			continue
		}
		if len(streams) == 0 {
			failures = append(failures, fmt.Errorf("екстрактор %s не повернув потоку: %w", ex.ID(), errs.ErrNoStream))
			if onEvent != nil {
				onEvent(EventTryingNext)
			}
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
			Stream:     streams[0],
			HostID:     ex.ID(),
			StartSec:   startSec,
			MediaTitle: fmt.Sprintf("%s · %d", name, ep),
			Candidates: candidates,
		}, nil
	}
	return nil, aggregateResolveFailures(ep, failures)
}

func aggregateResolveFailures(ep int, failures []error) error {
	offline, noStream := 0, 0
	for _, err := range failures {
		switch {
		case errors.Is(err, errs.ErrOffline) || errs.Offline(err):
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
	return fmt.Errorf("серія %d: жодне джерело не дало потоку: %w: %w", ep, class, errors.Join(failures...))
}

// EpisodesCached — серії з кешем метаданих: свіжий кеш (< TTL) віддається без
// мережі; при відмові мережі кеш будь-якої давності — офлайн-fallback.
// offline=true лише коли мережа впала і показано застарілий кеш.
func (e *Engine) EpisodesCached(ctx context.Context, ref provider.TitleRef) (eps []provider.Episode, offline bool, err error) {
	if cached, fresh, found := e.Store.LoadEpisodes(ref); found && fresh {
		return cached, false, nil
	}
	eps, err = e.Provider.Episodes(ctx, ref)
	if err != nil {
		if errors.Is(err, errs.ErrOffline) || errs.Offline(err) {
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
// локальна оцінка має бути звірена з актуальним списком серій.
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
	next := 0
	for _, ep := range eps {
		if ep.Number > after && (next == 0 || ep.Number < next) {
			next = ep.Number
		}
	}
	return next, next != 0
}

// CatalogCached — блок каталогу з кешем метаданих: свіжий кеш (< TTL)
// віддається без мережі; при відмові мережі кеш будь-якої давності —
// офлайн-fallback. offline=true лише коли мережа впала і показано застарілий кеш.
func (e *Engine) CatalogCached(ctx context.Context, kind provider.CatalogKind) (cards []provider.TitleCard, offline bool, err error) {
	id := e.Provider.ID()
	if cached, fresh, found := e.Store.LoadCatalog(id, kind); found && fresh {
		return cached, false, nil
	}
	cards, err = e.Provider.Catalog(ctx, kind)
	if err != nil {
		if errors.Is(err, errs.ErrOffline) || errs.Offline(err) {
			if cached, _, found := e.Store.LoadCatalog(id, kind); found {
				return cached, true, nil
			}
		}
		return nil, false, err
	}
	_ = e.Store.SaveCatalog(id, kind, cards)
	return cards, false, nil
}

// PinStudio закріплює студію за тайтлом (відповідь на одноразове питання).
func (e *Engine) PinStudio(ref provider.TitleRef, studio string) error {
	title := e.Lib.EnsureTitle(ref, store.NewID)
	e.Lib.EntryFor(title.ID).StudioPin = studio
	return e.Store.SaveLibrary(e.Lib)
}

// Bookmark перемикає тайтл у списку запланованого.
func (e *Engine) Bookmark(ref provider.TitleRef, epCount int) (library.BookmarkResult, error) {
	title := e.Lib.EnsureTitle(ref, store.NewID)
	result := e.Lib.ToggleBookmark(title.ID, epCount)
	return result, e.Store.SaveLibrary(e.Lib)
}

// MarkSeen оновлює базову лінію лише для вже відомого локального тайтлу.
func (e *Engine) MarkSeen(ref provider.TitleRef, maxEp int) error {
	title := e.Lib.TitleByRef(ref)
	if title == nil {
		return nil
	}
	e.Lib.MarkSeen(title.ID, maxEp)
	return e.Store.SaveLibrary(e.Lib)
}

// ReconcileKnown зберігає уточнення, лише коли очікувана базова лінія не змінилась.
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
}

// Play веде сесію mpv: журнал кожні 5 с, злиття в бібліотеку наприкінці.
// Скасування ctx закриває плеєр і теж зливає журнал.
func (e *Engine) Play(ctx context.Context, res *Resolved) (*Result, error) {
	if e.Player == nil {
		return nil, errs.ErrNoPlayer
	}
	title := e.Lib.EnsureTitle(res.Ref, store.NewID)
	entry := e.Lib.EntryFor(title.ID)

	out := &Result{}
	// студія запам'ятовується після першого перегляду: наступна серія
	// піде тією самою озвучкою без питань
	if entry.StudioPin == "" {
		entry.StudioPin = res.Source.Studio
		out.PinnedStudio = res.Source.Studio
	}
	entry.State = library.StateWatching
	if err := e.Store.SaveLibrary(e.Lib); err != nil {
		return nil, err
	}

	sess, err := e.Player.Start(res.Stream.URL, res.MediaTitle, res.Stream.Headers, res.StartSec)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	go func() { _ = sess.Wait() }()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			pos, err := sess.TimePos()
			if err != nil {
				continue // буферизація чи пауза — не привід падати
			}
			dur, _ := sess.Duration()
			_ = e.Store.WriteJournal(&store.Journal{
				TitleID: title.ID, Episode: res.Episode,
				PositionSec: pos, DurationSec: dur, UpdatedAt: time.Now(),
			})
		case reason := <-sess.End():
			out.Reason = reason
			return out, e.finish(out, title.ID, res.Episode)
		case <-ctx.Done():
			sess.Close()
			out.Reason = player.EndQuit
			return out, e.finish(out, title.ID, res.Episode)
		}
	}
}

func (e *Engine) finish(out *Result, titleID string, ep int) error {
	if _, err := e.Store.RecoverJournal(e.Lib); err != nil {
		return err
	}
	p := e.Lib.ProgressFor(titleID, ep)
	if p == nil {
		return nil
	}
	// eof — серію додивилися, навіть якщо журнал відстав від порогу 90%
	if out.Reason == player.EndEOF && !p.Completed {
		p.Completed = true
		if err := e.Store.SaveLibrary(e.Lib); err != nil {
			return err
		}
	}
	out.Completed = p.Completed
	out.PositionSec = p.PositionSec
	return nil
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
