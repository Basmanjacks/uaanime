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
	TitleID     string        `json:"title_id"`
	State       State         `json:"state"`
	StudioPin   string        `json:"studio_pin,omitempty"`
	KindPin     provider.Kind `json:"kind_pin,omitempty"`
	LastEpisode int           `json:"last_episode,omitempty"`
}

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
	for _, e := range l.Entries {
		if e.TitleID == titleID {
			return e
		}
	}
	e := &Entry{TitleID: titleID, State: StateWatching}
	l.Entries = append(l.Entries, e)
	return e
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
