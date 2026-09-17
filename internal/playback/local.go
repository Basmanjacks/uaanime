package playback

import (
	"fmt"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// localHostID — «хост» збереженої серії. Не порожній рядок, щоб у журналі й
// діагностиці було видно, що потік узяли з диска, а не з мережі.
const localHostID = "local"

// localResolved — серія з диска або nil, якщо збереженого файла немає.
//
// Пошук іде за provider+slug (див. download.Index), тому headless `play`, де
// ref приходить без Name, знаходить файл так само, як TUI.
//
// Вибір релізу робить той самий library.Pick, що й для мережевих джерел:
// правило 4 діє й на диску — дубляж 720p у папці не має тихо програтися
// субтитрами 1080p лише тому, що вони більші. Мережу ця функція не чіпає
// взагалі, тож async-safe і працює офлайн.
func localResolved(ref provider.TitleRef, ep int, h Hints) *Resolved {
	title := download.Index(h.DownloadDir)[download.RefKey(ref)]
	files := title.Files[ep]
	if len(files) == 0 {
		return nil
	}

	// Ім'я потрібне лише для заголовка вікна плеєра й пульта: підказка з
	// бібліотеки найсвіжіша, sidecar зберіг те, що було на момент
	// завантаження, slug — остання лінія, аби заголовок не був порожнім.
	name := h.Name
	if name == "" {
		name = title.Ref.Name
	}
	if name == "" {
		name = ref.Slug
	}

	pin := library.Pin{Studio: h.StudioPin, Kind: h.KindPin}
	res := &Resolved{
		Ref:        ref,
		Episode:    ep,
		Local:      true,
		Pin:        pin,
		Prefs:      h.Prefs,
		HostID:     localHostID,
		StartSec:   h.StartSec,
		Name:       name,
		MediaTitle: fmt.Sprintf("%s · %d", name, ep),
	}

	sources := localSources(files, ep)
	chosen, candidates := library.Pick(sources, pin, h.Prefs)
	if chosen == nil {
		// Жодного файла з відомим релізом (усі перенесені руками й упізнані
		// лише за іменем — або sidecar-и без типу, на яких Pick пасує):
		// вибирати нема з чого, тому без Pick — найвища якість, порожній
		// Source і нульове відхилення. Казати «інша студія», не знаючи
		// студії, було б брехнею.
		best := bestFile(files, "", "")
		res.Source = provider.Source{Episode: ep}
		res.Stream, res.Streams = localStream(best)
		res.Candidates = []provider.Source{res.Source}
		res.Playable = res.Candidates
		return res
	}
	res.Source = *chosen
	res.Deviation = library.DeviationOf(pin, *chosen)
	res.Stream, res.Streams = localStream(bestFile(files, chosen.Studio, chosen.Kind))
	// Локальні джерела не мають embed, тому e.playable їх би відсіяв: фільтр
	// «є екстрактор» для файла на диску безглуздий, лишається лише дедуплікація.
	res.Candidates = library.ReleaseChoices(candidates)
	res.Playable = library.ReleaseChoices(sources)
	return res
}

// localSources — файли серії як джерела для Pick. Файли без релізу в sidecar
// пропускаються: Pick на порожньому Kind повертає nil (див. preference.go),
// і одна безіменна знахідка сховала б справжні релізи поруч.
func localSources(files []download.SavedFile, ep int) []provider.Source {
	out := make([]provider.Source, 0, len(files))
	for _, f := range files {
		if f.Studio == "" && f.Kind == "" {
			continue
		}
		out = append(out, provider.Source{Episode: ep, Studio: f.Studio, Kind: f.Kind})
	}
	return out
}

// bestFile — найбільша якість серед файлів обраного релізу. Порядок з Index
// уже такий, але покладатися на нього означало б зв'язати два пакети мовчазною
// угодою про сортування.
func bestFile(files []download.SavedFile, studio string, kind provider.Kind) download.SavedFile {
	var best download.SavedFile
	found := false
	for _, f := range files {
		if studio != "" || kind != "" {
			if f.Studio != studio || f.Kind != kind {
				continue
			}
		}
		if !found || f.Height > best.Height {
			best, found = f, true
		}
	}
	return best
}

// localStream — шлях до файла замість URL і жодних заголовків: плеєр відкриває
// його з диска, посилати Referer нікуди.
func localStream(f download.SavedFile) (extractor.Stream, []extractor.Stream) {
	s := extractor.Stream{URL: f.Path, Quality: f.Height}
	return s, []extractor.Stream{s}
}
