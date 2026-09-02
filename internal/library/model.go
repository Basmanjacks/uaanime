// Package library — доменна логіка стану користувача: прогрес, завершення,
// закріплення студії. Нічого не знає ні про сайти, ні про UI, ні про диск.
package library

import (
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// LocalTitle — наш тайтл. Прогрес прив'язується до його ID, ніколи до слага
// провайдера: провайдери — змінні шляхи до того самого тайтлу.
type LocalTitle struct {
	ID      string              `json:"id"`
	Name    string              `json:"name"`
	Sources []provider.TitleRef `json:"sources"`
}

type State string

const (
	StateWatching  State = "watching"
	StatePlanned   State = "planned"
	StateCompleted State = "completed"
)

// Entry — запис списку перегляду.
type Entry struct {
	TitleID       string        `json:"title_id"`
	State         State         `json:"state"`
	StudioPin     string        `json:"studio_pin,omitempty"`
	KindPin       provider.Kind `json:"kind_pin,omitempty"`
	LastEpisode   int           `json:"last_episode,omitempty"`
	KnownEpisodes int           `json:"known_episodes,omitempty"`
	Hidden        bool          `json:"hidden,omitempty"`
}

type BookmarkResult int

const (
	BookmarkAdded BookmarkResult = iota
	BookmarkRemoved
)

type Progress struct {
	TitleID     string    `json:"title_id"`
	Episode     int       `json:"episode"`
	PositionSec float64   `json:"position_sec"`
	DurationSec float64   `json:"duration_sec"`
	Completed   bool      `json:"completed"`
	WatchedAt   time.Time `json:"watched_at"`
}

// Library — весь стан користувача. Серіалізується в library.json як є.
type Library struct {
	Titles   []*LocalTitle `json:"titles"`
	Entries  []*Entry      `json:"entries"`
	Progress []*Progress   `json:"progress"`
}

// Normalize приводить прочитану з диска бібліотеку до інваріантів, на які
// решта коду покладається без перевірок: TUI бере `t.Sources[0]` у шести
// місцях, а назви й піни йдуть у термінал як є. JSON `null` у масиві дає
// nil-елемент, тож структурні дірки чистяться тут же.
//
// clean передається ззовні, щоб домен не залежав від конкретного санітайзера
// (виклик передає provider.CleanText). Повертає кількість ВИДАЛЕНИХ записів —
// почищені рядки не рахуються: store.Import за цим числом відхиляє бекап цілком.
func (l *Library) Normalize(clean func(string) string) (dropped int) {
	known := make(map[string]bool, len(l.Titles))
	titles := l.Titles[:0]
	for _, t := range l.Titles {
		if t == nil {
			dropped++
			continue
		}
		t.Name = clean(t.Name)
		sources := t.Sources[:0]
		for _, s := range t.Sources {
			s.Name = clean(s.Name)
			// Збережений URL недовірений (міг прийти зі старого href або з
			// чужого бекапа); коректний уміє побудувати лише провайдер зі слага.
			s.URL = ""
			if !provider.ValidSlug(s.Slug) || s.Provider == "" {
				dropped++
				continue
			}
			sources = append(sources, s)
		}
		t.Sources = sources
		if t.ID == "" || len(t.Sources) == 0 {
			dropped++
			continue
		}
		known[t.ID] = true
		titles = append(titles, t)
	}
	l.Titles = titles

	seen := make(map[string]bool, len(l.Entries))
	entries := l.Entries[:0]
	for _, e := range l.Entries {
		if e == nil {
			dropped++
			continue
		}
		e.StudioPin = clean(e.StudioPin)
		// Порожній KindPin означає «пін не стоїть» — саме це й потрібно,
		// коли на диску опинилося щось невідоме: гадати тип заборонено.
		if e.KindPin != "" && !provider.ValidKind(e.KindPin) {
			e.KindPin = ""
		}
		switch e.State {
		case StateWatching, StatePlanned, StateCompleted, "":
		default:
			e.State = ""
		}
		if !known[e.TitleID] || seen[e.TitleID] {
			dropped++
			continue
		}
		seen[e.TitleID] = true
		entries = append(entries, e)
	}
	l.Entries = entries

	progress := l.Progress[:0]
	for _, p := range l.Progress {
		if p == nil || !known[p.TitleID] {
			dropped++
			continue
		}
		progress = append(progress, p)
	}
	l.Progress = progress
	return dropped
}

// TitleByRef знаходить локальний тайтл, що має вказане джерело.
func (l *Library) TitleByRef(ref provider.TitleRef) *LocalTitle {
	for _, t := range l.Titles {
		for _, s := range t.Sources {
			if s.Provider == ref.Provider && s.Slug == ref.Slug {
				return t
			}
		}
	}
	return nil
}

// EnsureTitle повертає локальний тайтл для джерела, створюючи його за потреби.
// newID передається ззовні: домен не генерує ідентифікатори сам.
func (l *Library) EnsureTitle(ref provider.TitleRef, newID func() string) *LocalTitle {
	if t := l.TitleByRef(ref); t != nil {
		if t.Name == "" && ref.Name != "" {
			t.Name = ref.Name
		}
		return t
	}
	t := &LocalTitle{ID: newID(), Name: ref.Name, Sources: []provider.TitleRef{ref}}
	l.Titles = append(l.Titles, t)
	return t
}

// EntryFor повертає запис списку перегляду тайтлу, створюючи його за потреби.
func (l *Library) EntryFor(titleID string) *Entry {
	if e := l.EntryLookup(titleID); e != nil {
		e.Hidden = false
		return e
	}
	e := &Entry{TitleID: titleID, State: StateWatching}
	l.Entries = append(l.Entries, e)
	return e
}

// EntryLookup повертає запис списку перегляду тайтлу, не змінюючи бібліотеку.
func (l *Library) EntryLookup(titleID string) *Entry {
	for _, e := range l.Entries {
		if e.TitleID == titleID {
			return e
		}
	}
	return nil
}

// ToggleBookmark змінює видиме членство тайтлу в бібліотеці. Прогрес живе
// окремо в Library.Progress, а піни — на Entry, тому розпочатий тайтл ховаємо,
// а не видаляємо разом із налаштуваннями користувача.
func (l *Library) ToggleBookmark(titleID string, knownEpisodes int) BookmarkResult {
	for i, entry := range l.Entries {
		if entry.TitleID != titleID {
			continue
		}
		if entry.State == StatePlanned {
			l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)
			return BookmarkRemoved
		}
		if entry.Hidden {
			entry.Hidden = false
			return BookmarkAdded
		}
		entry.Hidden = true
		return BookmarkRemoved
	}
	l.Entries = append(l.Entries, &Entry{
		TitleID:       titleID,
		State:         StatePlanned,
		KnownEpisodes: knownEpisodes,
	})
	return BookmarkAdded
}

// MarkSeen рухає базову лінію відомих серій лише вперед.
func (l *Library) MarkSeen(titleID string, maxEp int) {
	entry := l.EntryLookup(titleID)
	if entry != nil && maxEp > entry.KnownEpisodes {
		entry.KnownEpisodes = maxEp
	}
}

// ReconcileKnown замінює лише незмінену попередню оцінку кількості серій.
func (l *Library) ReconcileKnown(titleID string, provisional, actual int) bool {
	entry := l.EntryLookup(titleID)
	if entry == nil || entry.State != StatePlanned || entry.KnownEpisodes != provisional || actual == provisional {
		return false
	}
	// EpAired на картці буває завищеним, тому підтверджене число може бути меншим.
	entry.KnownEpisodes = actual
	return true
}

// ProgressFor повертає прогрес серії, якщо він є.
func (l *Library) ProgressFor(titleID string, episode int) *Progress {
	for _, p := range l.Progress {
		if p.TitleID == titleID && p.Episode == episode {
			return p
		}
	}
	return nil
}

// CompletionThreshold — частка тривалості, після якої серія вважається переглянутою.
const CompletionThreshold = 0.9

// RecordPosition оновлює прогрес серії і повертає запис. Позначає серію
// завершеною на ≥90% тривалості; LastEpisode рухається лише вперед.
func (l *Library) RecordPosition(titleID string, episode int, posSec, durSec float64, at time.Time) *Progress {
	p := l.ProgressFor(titleID, episode)
	if p == nil {
		p = &Progress{TitleID: titleID, Episode: episode}
		l.Progress = append(l.Progress, p)
	}
	p.PositionSec = posSec
	p.DurationSec = durSec
	p.WatchedAt = at
	if durSec > 0 && posSec/durSec >= CompletionThreshold {
		p.Completed = true
	}
	e := l.EntryFor(titleID)
	if episode > e.LastEpisode {
		e.LastEpisode = episode
	}
	return p
}

// Resume — що запропонувати на «Продовжити»: незавершена серія з позицією
// або наступна після останньої завершеної.
func (l *Library) Resume(titleID string) (episode int, positionSec float64, ok bool) {
	var best *Progress
	for _, p := range l.Progress {
		if p.TitleID != titleID {
			continue
		}
		if best == nil || p.WatchedAt.After(best.WatchedAt) {
			best = p
		}
	}
	if best == nil {
		return 0, 0, false
	}
	if best.Completed {
		return best.Episode + 1, 0, true
	}
	return best.Episode, best.PositionSec, true
}
