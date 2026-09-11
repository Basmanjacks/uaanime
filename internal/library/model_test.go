package library

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

// eps — список серій 1..n для ResumeIn і StatusOf.
func eps(n int) []provider.Episode {
	out := make([]provider.Episode, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, provider.Episode{Number: i})
	}
	return out
}

func TestRecordPositionAndResume(t *testing.T) {
	lib := &Library{}
	ref := provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "X"}
	title := lib.EnsureTitle(ref, func() string { return "id-1" })
	if lib.EnsureTitle(ref, func() string { return "id-2" }) != title {
		t.Fatal("EnsureTitle створив дубль")
	}

	now := time.Now()
	// вийшли на 14:32 з 24 хв
	lib.RecordPosition(title.ID, 3, 872, 1440, now)
	ep, pos, ok := lib.ResumeIn(title.ID, eps(5))
	if !ok || ep != 3 || pos != 872 {
		t.Fatalf("ResumeIn = (%d, %v, %v)", ep, pos, ok)
	}

	// додивилися до 95% — завершено, resume пропонує наступну
	lib.RecordPosition(title.ID, 3, 1368, 1440, now.Add(time.Minute))
	p := lib.ProgressFor(title.ID, 3)
	if p == nil || !p.Completed {
		t.Fatalf("очікував completed на 95%%: %+v", p)
	}
	ep, pos, ok = lib.ResumeIn(title.ID, eps(5))
	if !ok || ep != 4 || pos != 0 {
		t.Fatalf("ResumeIn після завершення = (%d, %v, %v)", ep, pos, ok)
	}

	if e := lib.EntryFor(title.ID); e.LastEpisode != 3 {
		t.Fatalf("LastEpisode = %d", e.LastEpisode)
	}
}

func TestCompletionThresholdNotReached(t *testing.T) {
	lib := &Library{}
	lib.RecordPosition("t", 1, 100, 1440, time.Now()) // ~7%
	if lib.ProgressFor("t", 1).Completed {
		t.Fatal("7% не є завершенням")
	}
}

func TestEntryLookupDoesNotCreateEntry(t *testing.T) {
	lib := &Library{}
	if entry := lib.EntryLookup("missing"); entry != nil {
		t.Fatalf("EntryLookup = %+v, want nil", entry)
	}
	if len(lib.Entries) != 0 {
		t.Fatalf("EntryLookup created %d entries", len(lib.Entries))
	}

	want := lib.EntryFor("existing")
	if got := lib.EntryLookup("existing"); got != want {
		t.Fatalf("EntryLookup = %p, want %p", got, want)
	}
}

func TestToggleBookmark(t *testing.T) {
	t.Run("adds planned entry", func(t *testing.T) {
		lib := &Library{}

		if got := lib.ToggleBookmark("title", 12); got != BookmarkAdded {
			t.Fatalf("ToggleBookmark = %v, очікував BookmarkAdded", got)
		}
		entry := lib.EntryLookup("title")
		if entry == nil || entry.KnownEpisodes != 12 {
			t.Fatalf("запис закладки = %+v", entry)
		}
	})

	t.Run("removes entry without progress", func(t *testing.T) {
		lib := &Library{Entries: []*Entry{
			{TitleID: "title", KnownEpisodes: 12},
			{TitleID: "other"},
		}}

		if got := lib.ToggleBookmark("title", 20); got != BookmarkRemoved {
			t.Fatalf("ToggleBookmark = %v, очікував BookmarkRemoved", got)
		}
		if lib.EntryLookup("title") != nil {
			t.Fatal("запланований запис лишився після видалення закладки")
		}
		if len(lib.Entries) != 1 || lib.Entries[0].TitleID != "other" {
			t.Fatalf("Entries після видалення = %+v", lib.Entries)
		}
	})

	t.Run("hides and restores started entry", func(t *testing.T) {
		entry := &Entry{TitleID: "title", StudioPin: "Студія", LastEpisode: 4}
		lib := &Library{
			Entries:  []*Entry{entry},
			Progress: []*Progress{{TitleID: "title", Episode: 4, Completed: true}},
		}

		if got := lib.ToggleBookmark("title", 20); got != BookmarkRemoved {
			t.Fatalf("ToggleBookmark = %v, очікував BookmarkRemoved", got)
		}
		if len(lib.Entries) != 1 || lib.Entries[0] != entry || !entry.Hidden {
			t.Fatalf("запис перегляду не приховано: %+v", lib.Entries)
		}
		if got := lib.ToggleBookmark("title", 20); got != BookmarkAdded {
			t.Fatalf("ToggleBookmark вдруге = %v, очікував BookmarkAdded", got)
		}
		if entry.Hidden {
			t.Fatalf("запис перегляду лишився прихованим: %+v", entry)
		}
	})

	// Знята остання позначка лишає прихований запис зовсім без прогресу:
	// «немає прогресу» тут не має означати «нічого не було».
	t.Run("restores hidden entry without progress", func(t *testing.T) {
		entry := &Entry{TitleID: "title", StudioPin: "Студія", Hidden: true}
		lib := &Library{Entries: []*Entry{entry}}

		if got := lib.ToggleBookmark("title", 20); got != BookmarkAdded {
			t.Fatalf("ToggleBookmark = %v, очікував BookmarkAdded", got)
		}
		if len(lib.Entries) != 1 || entry.Hidden {
			t.Fatalf("прихований запис не повернувся: %+v", lib.Entries)
		}
	})
}

func TestToggleBookmarkPreservesProgress(t *testing.T) {
	lib := &Library{}
	lib.RecordPosition("title", 3, 872, 1440, time.Now())

	if got := lib.ToggleBookmark("title", 12); got != BookmarkRemoved {
		t.Fatalf("ToggleBookmark = %v, очікував BookmarkRemoved", got)
	}
	entry := lib.EntryLookup("title")
	if entry == nil || !entry.Hidden {
		t.Fatalf("прихований запис = %+v", entry)
	}
	ep, pos, ok := lib.ResumeIn("title", eps(5))
	if !ok || ep != 3 || pos != 872 {
		t.Fatalf("ResumeIn після приховування = (%d, %v, %v)", ep, pos, ok)
	}

	if got := lib.ToggleBookmark("title", 12); got != BookmarkAdded {
		t.Fatalf("ToggleBookmark вдруге = %v, очікував BookmarkAdded", got)
	}
	if entry.Hidden {
		t.Fatalf("запис лишився прихованим: %+v", entry)
	}
}

func TestToggleBookmarkPreservesStudioPin(t *testing.T) {
	entry := &Entry{
		TitleID:   "title",
		StudioPin: "Студія",
		KindPin:   provider.KindDub,
	}
	lib := &Library{
		Entries:  []*Entry{entry},
		Progress: []*Progress{{TitleID: "title", Episode: 1, Completed: true}},
	}

	lib.ToggleBookmark("title", 12)
	lib.ToggleBookmark("title", 12)

	if entry.StudioPin != "Студія" || entry.KindPin != provider.KindDub {
		t.Fatalf("піни після hide/unhide = (%q, %q)", entry.StudioPin, entry.KindPin)
	}
}

func TestEntryForRevealsHiddenEntry(t *testing.T) {
	entry := &Entry{TitleID: "title", Hidden: true}
	lib := &Library{Entries: []*Entry{entry}}

	if got := lib.EntryFor("title"); got != entry || got.Hidden {
		t.Fatalf("EntryFor = %+v, очікував видимий існуючий запис", got)
	}
}

func TestMarkSeenIsMonotonic(t *testing.T) {
	lib := &Library{Entries: []*Entry{{TitleID: "title", KnownEpisodes: 3}}}

	lib.MarkSeen("title", 5)
	lib.MarkSeen("title", 2)
	if got := lib.EntryLookup("title").KnownEpisodes; got != 5 {
		t.Fatalf("KnownEpisodes = %d, очікував 5", got)
	}

	lib.MarkSeen("missing", 9)
	if len(lib.Entries) != 1 {
		t.Fatalf("MarkSeen створив запис: %+v", lib.Entries)
	}
}

func TestSetWatchedMarksAndUnmarks(t *testing.T) {
	lib := &Library{}
	now := time.Now()

	lib.SetWatched("title", 2, true, now)
	p := lib.ProgressFor("title", 2)
	if p == nil || !p.Completed || !p.WatchedAt.Equal(now) {
		t.Fatalf("прогрес після позначки: %+v", p)
	}
	if ep, pos, ok := lib.ResumeIn("title", eps(5)); !ok || ep != 3 || pos != 0 {
		t.Fatalf("ResumeIn = (%d, %v, %v), очікував серію 3", ep, pos, ok)
	}
	if got := lib.EntryFor("title").LastEpisode; got != 2 {
		t.Fatalf("LastEpisode = %d, очікував 2", got)
	}

	// раніша серія не понижує LastEpisode
	lib.SetWatched("title", 1, true, now.Add(time.Minute))
	if got := lib.EntryFor("title").LastEpisode; got != 2 {
		t.Fatalf("LastEpisode = %d після позначки серії 1", got)
	}

	lib.SetWatched("title", 2, false, now.Add(2*time.Minute))
	if p := lib.ProgressFor("title", 2); p != nil {
		t.Fatalf("прогрес серії 2 не зник: %+v", p)
	}
	if got := lib.EntryFor("title").LastEpisode; got != 1 {
		t.Fatalf("LastEpisode = %d, очікував падіння до 1", got)
	}
	if got := lib.ProgressFor("title", 1); got == nil {
		t.Fatal("зняття позначки прибрало чужий прогрес")
	}
}

func TestSetWatchedKeepsDurationAndKnownEpisodes(t *testing.T) {
	lib := &Library{Entries: []*Entry{{TitleID: "title", KnownEpisodes: 12}}}
	now := time.Now()
	lib.RecordPosition("title", 4, 100, 1440, now)

	lib.SetWatched("title", 4, true, now.Add(time.Minute))
	p := lib.ProgressFor("title", 4)
	if p.PositionSec != 1440 || p.DurationSec != 1440 {
		t.Fatalf("позиція не в кінці: %+v", p)
	}

	lib.SetWatched("title", 4, false, now.Add(2*time.Minute))
	if got := lib.EntryLookup("title").KnownEpisodes; got != 12 {
		t.Fatalf("KnownEpisodes = %d, очікував 12", got)
	}
}

func TestSetWatchedUnmarkMissingIsNoop(t *testing.T) {
	lib := &Library{Entries: []*Entry{{TitleID: "title", LastEpisode: 5}}}

	lib.SetWatched("title", 7, false, time.Now())
	if len(lib.Progress) != 0 {
		t.Fatalf("Progress = %+v", lib.Progress)
	}
	if got := lib.EntryLookup("title").LastEpisode; got != 5 {
		t.Fatalf("LastEpisode = %d, очікував 5", got)
	}

	lib.SetWatched("missing", 1, false, time.Now())
	if len(lib.Entries) != 1 {
		t.Fatalf("зняття позначки створило запис: %+v", lib.Entries)
	}
}

func TestReconcileKnown(t *testing.T) {
	t.Run("lowers matching provisional value", func(t *testing.T) {
		lib := &Library{Entries: []*Entry{{TitleID: "title", KnownEpisodes: 12}}}

		if changed := lib.ReconcileKnown("title", 12, 10); !changed {
			t.Fatal("ReconcileKnown не повідомив про зміну")
		}
		if got := lib.EntryLookup("title").KnownEpisodes; got != 10 {
			t.Fatalf("KnownEpisodes = %d, очікував 10", got)
		}
	})

	t.Run("refuses changed baseline", func(t *testing.T) {
		lib := &Library{Entries: []*Entry{{TitleID: "title", KnownEpisodes: 12}}}
		lib.MarkSeen("title", 13)

		if changed := lib.ReconcileKnown("title", 12, 10); changed {
			t.Fatal("ReconcileKnown перезаписав змінену базову лінію")
		}
		if got := lib.EntryLookup("title").KnownEpisodes; got != 13 {
			t.Fatalf("KnownEpisodes = %d, очікував 13", got)
		}
	})

	t.Run("refuses started entry", func(t *testing.T) {
		lib := &Library{
			Entries:  []*Entry{{TitleID: "title", KnownEpisodes: 12}},
			Progress: []*Progress{{TitleID: "title", Episode: 1}},
		}

		if changed := lib.ReconcileKnown("title", 12, 10); changed {
			t.Fatal("ReconcileKnown змінив запис перегляду")
		}
		if got := lib.EntryLookup("title").KnownEpisodes; got != 12 {
			t.Fatalf("KnownEpisodes = %d, очікував 12", got)
		}
	})
}

func TestKnownEpisodesJSONCompatibility(t *testing.T) {
	want := &Library{Entries: []*Entry{{TitleID: "title", KnownEpisodes: 12}}}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got Library
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if entry := got.EntryLookup("title"); entry == nil || entry.KnownEpisodes != 12 {
		t.Fatalf("KnownEpisodes не пережив JSON: %+v", entry)
	}

	var old Library
	if err := json.Unmarshal([]byte(`{"titles":[],"entries":[{"title_id":"old","state":"planned"}],"progress":[]}`), &old); err != nil {
		t.Fatalf("Unmarshal старої бібліотеки: %v", err)
	}
	if entry := old.EntryLookup("old"); entry == nil || entry.KnownEpisodes != 0 {
		t.Fatalf("KnownEpisodes старої бібліотеки = %+v, очікував 0", entry)
	}
}

func TestNormalizeDropsNilElements(t *testing.T) {
	// JSON `null` у масиві дає саме nil-елемент — так виглядає бібліотека,
	// яку хтось редагував руками.
	const raw = `{"titles":[null,{"id":"t1","name":"X","sources":[{"provider":"anitube","slug":"1-x"}]}],
	              "entries":[null,{"title_id":"t1","state":"watching"}],
	              "progress":[null,{"title_id":"t1","episode":1}]}`
	var lib Library
	if err := json.Unmarshal([]byte(raw), &lib); err != nil {
		t.Fatal(err)
	}

	if dropped := lib.Normalize(provider.CleanText); dropped != 3 {
		t.Fatalf("dropped = %d, очікував 3", dropped)
	}
	if len(lib.Titles) != 1 || len(lib.Entries) != 1 || len(lib.Progress) != 1 {
		t.Fatalf("після Normalize: titles=%d entries=%d progress=%d",
			len(lib.Titles), len(lib.Entries), len(lib.Progress))
	}
	for i, tt := range lib.Titles {
		if tt == nil {
			t.Fatalf("titles[%d] лишився nil", i)
		}
	}
}

func TestNormalizeCleansControlChars(t *testing.T) {
	lib := &Library{
		Titles: []*LocalTitle{{
			ID:      "t1",
			Name:    "Фрірен\x1b[2J",
			Sources: []provider.TitleRef{{Provider: "anitube", Slug: "1-x", Name: "Фрірен\u009b2J", URL: "https://evil.invalid/x"}},
		}},
		Entries: []*Entry{{TitleID: "t1", StudioPin: "FanVox\x1b[2J", KindPin: provider.Kind("\x1b")}},
	}

	if dropped := lib.Normalize(provider.CleanText); dropped != 0 {
		t.Fatalf("dropped = %d, чистка рядків не має нічого видаляти", dropped)
	}
	if got := lib.Titles[0].Name; got != "Фрірен" {
		t.Errorf("Name = %q", got)
	}
	if got := lib.Titles[0].Sources[0].Name; got != "Фрірен" {
		t.Errorf("Sources[0].Name = %q", got)
	}
	if got := lib.Titles[0].Sources[0].URL; got != "" {
		t.Errorf("Sources[0].URL = %q, збережений URL має обнулятися", got)
	}
	if got := lib.Entries[0].StudioPin; got != "FanVox" {
		t.Errorf("StudioPin = %q", got)
	}
	if got := lib.Entries[0].KindPin; got != "" {
		t.Errorf("KindPin = %q, невалідний пін має зникати", got)
	}
}

func TestNormalizeDropsTitleWithBadSlug(t *testing.T) {
	lib := &Library{
		Titles: []*LocalTitle{{
			ID:      "bad",
			Name:    "Погана",
			Sources: []provider.TitleRef{{Provider: "anitube", Slug: "../x"}},
		}},
		Entries:  []*Entry{{TitleID: "bad"}},
		Progress: []*Progress{{TitleID: "bad", Episode: 1}},
	}

	// джерело + тайтл + запис списку + прогрес
	if dropped := lib.Normalize(provider.CleanText); dropped != 4 {
		t.Fatalf("dropped = %d, очікував 4", dropped)
	}
	if len(lib.Titles) != 0 || len(lib.Entries) != 0 || len(lib.Progress) != 0 {
		t.Fatalf("тайтл без валідних джерел мав зникнути разом зі своїми записами: %+v", lib)
	}
}

func TestNormalizeDropsOrphansAndDuplicates(t *testing.T) {
	lib := &Library{
		Titles: []*LocalTitle{{
			ID:      "t1",
			Name:    "X",
			Sources: []provider.TitleRef{{Provider: "anitube", Slug: "1-x"}},
		}},
		Entries: []*Entry{
			{TitleID: "t1", LastEpisode: 5},
			{TitleID: "t1"},
			{TitleID: "ghost"},
		},
		Progress: []*Progress{{TitleID: "t1", Episode: 1}, {TitleID: "ghost", Episode: 1}},
	}

	// дубль + сирота серед Entries, сирота серед Progress
	if dropped := lib.Normalize(provider.CleanText); dropped != 3 {
		t.Fatalf("dropped = %d, очікував 3", dropped)
	}
	if len(lib.Entries) != 1 || lib.Entries[0].LastEpisode != 5 {
		t.Fatalf("з дублів має лишатися перший: %+v", lib.Entries)
	}
	if len(lib.Progress) != 1 || lib.Progress[0].TitleID != "t1" {
		t.Fatalf("прогрес без тайтлу мав зникнути: %+v", lib.Progress)
	}
}

// Стан більше не зберігається, але старі library.json несуть ключ "state" —
// читання не має ні падати, ні воскрешати поле.
func TestNormalizeIgnoresLegacyState(t *testing.T) {
	const raw = `{"titles":[{"id":"t1","name":"X","sources":[{"provider":"anitube","slug":"1-x"}]}],
	              "entries":[{"title_id":"t1","state":"dropped-by-user","known_episodes":3}],
	              "progress":[]}`
	var lib Library
	if err := json.Unmarshal([]byte(raw), &lib); err != nil {
		t.Fatal(err)
	}
	if dropped := lib.Normalize(provider.CleanText); dropped != 0 {
		t.Fatalf("dropped = %d, старий ключ не привід викидати запис", dropped)
	}
	entry := lib.EntryLookup("t1")
	if entry == nil || entry.KnownEpisodes != 3 {
		t.Fatalf("запис зі старим ключем = %+v", entry)
	}
	data, err := json.Marshal(&lib)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(data), `"state"`) {
		t.Fatalf("збережена бібліотека досі несе state: %s", data)
	}
}

func TestNormalizeLeavesValidLibraryIntact(t *testing.T) {
	build := func() *Library {
		return &Library{
			Titles: []*LocalTitle{{
				ID:      "t1",
				Name:    "Фрірен",
				Sources: []provider.TitleRef{{Provider: "anitube", Slug: "4465-frren", Name: "Фрірен"}},
			}},
			Entries:  []*Entry{{TitleID: "t1", StudioPin: "FanVox", KindPin: provider.KindDub}},
			Progress: []*Progress{{TitleID: "t1", Episode: 2, PositionSec: 60, DurationSec: 1440}},
		}
	}
	lib, want := build(), build()
	want.Version = LibraryVersion // єдина легальна зміна: штамп схеми

	if dropped := lib.Normalize(provider.CleanText); dropped != 0 {
		t.Fatalf("dropped = %d, чиста бібліотека має лишатися цілою", dropped)
	}
	if !reflect.DeepEqual(lib, want) {
		t.Fatalf("Normalize змінив чисту бібліотеку:\n got %+v\nwant %+v", lib, want)
	}
}

func TestNormalizeMigratesLegacySubPin(t *testing.T) {
	// До v2 kind_pin: sub писався неявно (Begin) — стає wildcard; явний sub-пін
	// у файлі v2 лишається.
	legacy := &Library{
		Titles:  []*LocalTitle{{ID: "t", Sources: []provider.TitleRef{{Provider: "anitube", Slug: "4465-frren"}}}},
		Entries: []*Entry{{TitleID: "t", StudioPin: "A", KindPin: provider.KindSub}},
	}
	if dropped := legacy.Normalize(provider.CleanText); dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	if legacy.Entries[0].KindPin != "" || legacy.Entries[0].StudioPin != "A" {
		t.Fatalf("legacy pin = %q/%q, want A/wildcard", legacy.Entries[0].StudioPin, legacy.Entries[0].KindPin)
	}
	if legacy.Version != LibraryVersion {
		t.Fatalf("Version = %d, want %d", legacy.Version, LibraryVersion)
	}

	current := &Library{
		Version: LibraryVersion,
		Titles:  []*LocalTitle{{ID: "t", Sources: []provider.TitleRef{{Provider: "anitube", Slug: "4465-frren"}}}},
		Entries: []*Entry{{TitleID: "t", StudioPin: "A", KindPin: provider.KindSub}},
	}
	current.Normalize(provider.CleanText)
	if current.Entries[0].KindPin != provider.KindSub {
		t.Fatalf("явний sub-пін v2 має зберегтися, отримав %q", current.Entries[0].KindPin)
	}
}
