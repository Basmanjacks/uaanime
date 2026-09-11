package library

import "github.com/Basmanjacks/uaanime/internal/provider"

// StatusKind — стан тайтла з погляду людини. Не зберігається на диску:
// збережений стан завжди розходився з журналом (запис лишався «у планах»
// після восьми ручних позначок), тож єдине джерело правди — сам журнал.
type StatusKind int

const (
	StatusPlanned  StatusKind = iota // жодної серії не почато
	StatusWatching                   // є непереглянуті або список серій невідомий
	StatusDone                       // усе наявне переглянуто
)

// Status — стан тайтла, порахований, а не збережений. Total == 0 означає, що
// списку серій ще немає: числа невідомі, лишається сам Kind.
type Status struct {
	Kind      StatusKind
	Total     int // серій у списку
	Remaining int // непереглянутих серій у списку
	Fresh     int // непереглянутих із номером понад базову лінію «що вже вийшло»
	// FreshSubOnly — скільки з Fresh мають лише субтитри (є релізи, і всі sub).
	// Серія без метаданих релізів — невідома, а не «лише саби».
	FreshSubOnly int
}

// subOnly — у серії є релізи, і жоден із них не озвучення (multi рахується
// озвученням: за kindRank він стоїть перед sub).
func subOnly(releases []provider.Release) bool {
	if len(releases) == 0 {
		return false
	}
	for _, r := range releases {
		if r.Kind != provider.KindSub {
			return false
		}
	}
	return true
}

// StatusOf рахує стан тайтла з журналу прогресу і списку серій. Порожній
// episodes — список ще не кешований: Kind виводиться лише з журналу.
// Невідомий (у т.ч. порожній) titleID — тайтл, якого ще немає в бібліотеці:
// прогресу немає, тож Planned із повним списком серій.
//
// Номери серій рахуються множиною: провайдер уміє віддати той самий номер
// двічі, і бейдж на це вже колись ловився.
func (l *Library) StatusOf(titleID string, episodes []provider.Episode) Status {
	completed := map[int]bool{}
	started := false
	if titleID != "" {
		for _, p := range l.Progress {
			if p == nil || p.TitleID != titleID {
				continue
			}
			started = true
			if p.Completed {
				completed[p.Episode] = true
			}
		}
	}

	// Базова лінія «що вже вийшло на момент, коли ми востаннє дивилися»:
	// понад неї серія вважається новою.
	baseline := 0
	if e := l.EntryLookup(titleID); e != nil {
		baseline = max(e.LastEpisode, e.KnownEpisodes)
	}

	// Провайдер може віддати один номер двічі: релізи всіх дублів зливаються,
	// щоб «лише саби» рахувалося по всьому, що є в серії (як у PreferredFresh).
	releases := map[int][]provider.Release{}
	for _, ep := range episodes {
		releases[ep.Number] = append(releases[ep.Number], provider.CleanEpisode(ep).Releases...)
	}

	var st Status
	seen := map[int]bool{}
	for _, ep := range episodes {
		if seen[ep.Number] {
			continue
		}
		seen[ep.Number] = true
		st.Total++
		if completed[ep.Number] {
			continue
		}
		st.Remaining++
		if ep.Number > baseline {
			st.Fresh++
			if subOnly(releases[ep.Number]) {
				st.FreshSubOnly++
			}
		}
	}

	switch {
	case !started:
		st.Kind = StatusPlanned
	case len(episodes) == 0 || st.Remaining > 0:
		st.Kind = StatusWatching
	default:
		st.Kind = StatusDone
	}
	return st
}

// NextEpisodeAfter — найменший наявний номер, більший за after. found окремо
// від номера: «0 серія» легальна (див. provider.CleanEpisodes), тож нуль тут
// не може бути ознакою «не знайдено».
func NextEpisodeAfter(episodes []provider.Episode, after int) (num int, found bool) {
	for _, ep := range episodes {
		if ep.Number <= after {
			continue
		}
		if !found || ep.Number < num {
			num, found = ep.Number, true
		}
	}
	return num, found
}
