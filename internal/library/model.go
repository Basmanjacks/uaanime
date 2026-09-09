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

// Entry — запис списку перегляду. Стану тут навмисно немає: «у планах» /
// «переглядаєш» / «переглянуто» рахуються з журналу прогресу (див. StatusOf),
// бо збережений стан розходився з ним при кожній ручній позначці.
type Entry struct {
	TitleID       string        `json:"title_id"`
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
	e := &Entry{TitleID: titleID}
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
		// Прихований перевіряється ПЕРШИМ: зняття всіх позначок лишає запис
		// без прогресу, і без цього порядку повернення в закладки читалося б
		// як «нічого не почато» і видаляло б запис замість розховати його.
		if entry.Hidden {
			entry.Hidden = false
			return BookmarkAdded
		}
		if !l.started(titleID) {
			l.Entries = append(l.Entries[:i], l.Entries[i+1:]...)
			return BookmarkRemoved
		}
		entry.Hidden = true
		return BookmarkRemoved
	}
	l.Entries = append(l.Entries, &Entry{
		TitleID:       titleID,
		KnownEpisodes: knownEpisodes,
	})
	return BookmarkAdded
}

// started — чи є в журналі бодай один запис про цей тайтл. Це й є межа між
// «у планах» і рештою станів.
func (l *Library) started(titleID string) bool {
	for _, p := range l.Progress {
		if p != nil && p.TitleID == titleID {
			return true
		}
	}
	return false
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
	if entry == nil || l.started(titleID) || entry.KnownEpisodes != provisional || actual == provisional {
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

// SetWatched — ручна позначка серії, без плеєра. Позначена серія виглядає так
// само, як додивлена: перемотана в кінець і завершена, тож Resume пропонує
// наступну.
//
// Зняття позначки видаляє прогрес серії, і це ЄДИНЕ місце, де LastEpisode
// рухається назад: користувач явно сказав, що серії він не бачив, і залишений
// попереду LastEpisode збрехав би на домівці. KnownEpisodes не чіпаємо — це
// база «що вийшло», а не «що переглянуто».
func (l *Library) SetWatched(titleID string, episode int, watched bool, at time.Time) {
	if watched {
		p := l.ProgressFor(titleID, episode)
		if p == nil {
			p = &Progress{TitleID: titleID, Episode: episode}
			l.Progress = append(l.Progress, p)
		}
		p.PositionSec = p.DurationSec // невідома тривалість лишається 0
		p.Completed = true
		p.WatchedAt = at
		e := l.EntryFor(titleID)
		if episode > e.LastEpisode {
			e.LastEpisode = episode
		}
		return
	}

	if l.ProgressFor(titleID, episode) == nil {
		return
	}
	progress := l.Progress[:0]
	last := 0
	for _, p := range l.Progress {
		if p.TitleID == titleID && p.Episode == episode {
			continue
		}
		if p.TitleID == titleID && p.Episode > last {
			last = p.Episode
		}
		progress = append(progress, p)
	}
	l.Progress = progress
	// EntryLookup, не EntryFor: зняття позначки не має ні створювати запис
	// списку, ні розховувати прихований.
	if e := l.EntryLookup(titleID); e != nil {
		e.LastEpisode = last
	}
}

// ResumeIn — що запропонувати на «Продовжити» для тайтла зі списком серій
// episodes. Список обов'язковий саме тому, що без нього попередня версія
// пропонувала «остання завершена + 1» — серію, якої на сайті ще немає.
//
// Ручні позначки лишають дірки (завершена 10-та при непереглянутій 3-й), тож
// порядок такий: незавершена серія з позицією → перша після останньої
// завершеної → найменша непереглянута → нема чого дивитися.
func (l *Library) ResumeIn(titleID string, episodes []provider.Episode) (episode int, positionSec float64, ok bool) {
	if len(episodes) == 0 {
		// Списку немає (перший запуск, порожній кеш): звірити номер нема з чим,
		// тож пропонуємо лише те, що людина точно вже відкривала.
		if best := l.latestProgress(titleID, nil); best != nil && !best.Completed {
			return best.Episode, best.PositionSec, true
		}
		return 0, 0, false
	}

	exists := make(map[int]bool, len(episodes))
	completed := make(map[int]bool, len(episodes))
	for _, ep := range episodes {
		exists[ep.Number] = true
	}
	for _, p := range l.Progress {
		if p != nil && p.TitleID == titleID && p.Completed && exists[p.Episode] {
			completed[p.Episode] = true
		}
	}

	if best := l.latestProgress(titleID, exists); best != nil {
		if !best.Completed {
			return best.Episode, best.PositionSec, true
		}
		if next, found := NextEpisodeAfter(episodes, best.Episode); found && !completed[next] {
			return next, 0, true
		}
	}

	first, found := 0, false
	for _, ep := range episodes {
		if completed[ep.Number] {
			continue
		}
		if !found || ep.Number < first {
			first, found = ep.Number, true
		}
	}
	return first, 0, found
}

// latestProgress — найсвіжіший запис журналу для тайтла; among != nil звужує
// вибір до серій, які досі є в списку.
func (l *Library) latestProgress(titleID string, among map[int]bool) *Progress {
	var best *Progress
	for _, p := range l.Progress {
		if p == nil || p.TitleID != titleID {
			continue
		}
		if among != nil && !among[p.Episode] {
			continue
		}
		if best == nil || p.WatchedAt.After(best.WatchedAt) {
			best = p
		}
	}
	return best
}
