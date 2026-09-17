package playback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// localRef — тайтл БЕЗ Name: саме так його бачить headless `play` (refFromID),
// і саме на цьому ref має спрацювати пошук по диску.
var localRef = provider.TitleRef{Provider: "stub", Slug: "1-frieren"}

const localTitleName = "Фрірен"

// saveFile кладе файл так, як це робить завантажувач: папка з marker-ом,
// медіафайл і (за withSidecar) паспорт поруч. Без sidecar-а файл бачить лише
// фолбек за іменем — це випадок «перенесли руками».
func saveFile(t *testing.T, dir string, p download.Parts, withSidecar bool) string {
	t.Helper()
	folder := filepath.Join(dir, download.FolderName(localTitleName))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := download.WriteMarker(folder, download.Marker{
		Provider: localRef.Provider, Slug: localRef.Slug, Name: localTitleName,
	}); err != nil {
		t.Fatalf("marker: %v", err)
	}
	path := download.Path(dir, folder, p)
	if err := os.WriteFile(path, []byte("медіа"), 0o644); err != nil {
		t.Fatalf("write media: %v", err)
	}
	if withSidecar {
		if err := download.WriteSidecar(path, download.Sidecar{
			Provider: localRef.Provider, Slug: localRef.Slug, Name: localTitleName,
			Episode: p.Episode, Studio: p.Studio, Kind: p.Kind, Height: p.Height,
		}); err != nil {
			t.Fatalf("sidecar: %v", err)
		}
	}
	return path
}

// hostileProvider падає на будь-якому мережевому виклику: локальний резолв
// зобов'язаний обійтися без провайдера (літак, лежачий сайт).
func hostileProvider(t *testing.T) providertest.Stub {
	t.Helper()
	return providertest.Stub{
		IDValue: "stub",
		SourcesFn: func(context.Context, provider.TitleRef, int) ([]provider.Source, error) {
			t.Fatal("локальний файл є, а резолв пішов у провайдера за джерелами")
			return nil, nil
		},
		EpisodesFn: func(context.Context, provider.TitleRef) ([]provider.Episode, error) {
			t.Fatal("локальний файл є, а резолв пішов у провайдера за серіями")
			return nil, nil
		},
	}
}

func TestResolveWithPlaysSavedEpisodeWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	path := saveFile(t, dir, download.Parts{
		Title: localTitleName, Episode: 5, Studio: "FanVoxUA",
		Kind: provider.KindDub, Height: 1080, Ext: ".ts",
	}, true)

	engine := &Engine{Provider: hostileProvider(t), Lib: &library.Library{}}
	h := Hints{DownloadDir: dir, StartSec: 120, Prefs: library.Prefs{}}

	res, err := engine.ResolveWith(t.Context(), localRef, 5, h, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if !res.Local || res.HostID != "local" {
		t.Fatalf("Resolved.Local = %v, HostID = %q", res.Local, res.HostID)
	}
	if res.Stream.URL != path || len(res.Stream.Headers) != 0 {
		t.Fatalf("Stream = %+v, очікував шлях %q без заголовків", res.Stream, path)
	}
	if len(res.Streams) != 1 || res.Streams[0].URL != res.Stream.URL {
		t.Fatalf("Streams = %+v, очікував рівно Stream", res.Streams)
	}
	if res.StartSec != 120 {
		t.Fatalf("StartSec = %v, очікував resume-позицію з підказок", res.StartSec)
	}
	// Name у ref порожній — його має дати sidecar, а не slug.
	if res.Name != localTitleName || res.MediaTitle != localTitleName+" · 5" {
		t.Fatalf("Name = %q, MediaTitle = %q", res.Name, res.MediaTitle)
	}
	if res.Ref != localRef || res.Episode != 5 {
		t.Fatalf("Ref/Episode = %+v/%d", res.Ref, res.Episode)
	}
	if res.Source.Studio != "FanVoxUA" || res.Source.Kind != provider.KindDub {
		t.Fatalf("Source = %+v", res.Source)
	}
	if res.Deviation != library.DeviationNone || res.Failed != nil {
		t.Fatalf("Deviation = %v, Failed = %+v", res.Deviation, res.Failed)
	}
	if len(res.Playable) != 1 || res.Playable[0].Studio != "FanVoxUA" {
		t.Fatalf("Playable = %+v", res.Playable)
	}
	if text, ok := res.Warning(); ok {
		t.Fatalf("Warning = %q, очікував тишу", text)
	}
}

// Правило 4 діє й на диску: 720p дубляж не програється як 1080p субтитри
// лише тому, що файл більший. Явний пін на студію субтитрів поважається.
func TestResolveWithLocalRespectsPickAndPin(t *testing.T) {
	dir := t.TempDir()
	dub := saveFile(t, dir, download.Parts{
		Title: localTitleName, Episode: 3, Studio: "FanVoxUA",
		Kind: provider.KindDub, Height: 720, Ext: ".ts",
	}, true)
	sub := saveFile(t, dir, download.Parts{
		Title: localTitleName, Episode: 3, Studio: "SubStudio",
		Kind: provider.KindSub, Height: 1080, Ext: ".ts",
	}, true)

	engine := &Engine{Provider: hostileProvider(t), Lib: &library.Library{}}

	res, err := engine.ResolveWith(t.Context(), localRef, 3, Hints{DownloadDir: dir}, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if res.Stream.URL != dub || res.Source.Kind != provider.KindDub {
		t.Fatalf("без піна грає %q (%+v), очікував дубляж %q", res.Stream.URL, res.Source, dub)
	}
	if len(res.Playable) != 2 {
		t.Fatalf("Playable = %+v, очікував обидва релізи", res.Playable)
	}

	pinned := Hints{DownloadDir: dir, StudioPin: "SubStudio", KindPin: provider.KindSub}
	res, err = engine.ResolveWith(t.Context(), localRef, 3, pinned, nil)
	if err != nil {
		t.Fatalf("ResolveWith з піном: %v", err)
	}
	if res.Stream.URL != sub || res.Source.Studio != "SubStudio" {
		t.Fatalf("з піном грає %q (%+v), очікував %q", res.Stream.URL, res.Source, sub)
	}
	if res.Deviation != library.DeviationNone || res.Pin != (library.Pin{Studio: "SubStudio", Kind: provider.KindSub}) {
		t.Fatalf("Deviation = %v, Pin = %+v — явний пін збігся з вибором", res.Deviation, res.Pin)
	}

	// Пін на студію, якої на диску немає: грає дубляж, що є, і про підміну
	// попереджає той самий Warning, що й для мережі.
	missing := Hints{DownloadDir: dir, StudioPin: "Missing"}
	res, err = engine.ResolveWith(t.Context(), localRef, 3, missing, nil)
	if err != nil {
		t.Fatalf("ResolveWith з чужим піном: %v", err)
	}
	if res.Stream.URL != dub || res.Deviation != library.DeviationStudio {
		t.Fatalf("Stream = %q, Deviation = %v", res.Stream.URL, res.Deviation)
	}
	if text, ok := res.Warning(); !ok || text != fmt.Sprintf(i18n.TuiStudioFallback, "Missing", "FanVoxUA") {
		t.Fatalf("Warning = (%q, %v)", text, ok)
	}
}

// Файли без sidecar (перенесені руками): реліз невідомий, тому без Pick —
// просто найвища якість і жодних тверджень про студію.
func TestResolveWithLocalNameParsedTakesHighestQuality(t *testing.T) {
	dir := t.TempDir()
	saveFile(t, dir, download.Parts{Title: localTitleName, Episode: 7, Height: 480, Ext: ".ts"}, false)
	best := saveFile(t, dir, download.Parts{Title: localTitleName, Episode: 7, Height: 1080, Ext: ".ts"}, false)

	engine := &Engine{Provider: hostileProvider(t), Lib: &library.Library{}}

	res, err := engine.ResolveWith(t.Context(), localRef, 7, Hints{DownloadDir: dir}, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if res.Stream.URL != best {
		t.Fatalf("Stream.URL = %q, очікував найвищу якість %q", res.Stream.URL, best)
	}
	if res.Source.Studio != "" || res.Source.Kind != "" || res.Deviation != library.DeviationNone {
		t.Fatalf("Source = %+v, Deviation = %v — реліз невідомий", res.Source, res.Deviation)
	}
	if len(res.Candidates) != 1 || len(res.Playable) != 1 {
		t.Fatalf("Candidates = %+v, Playable = %+v", res.Candidates, res.Playable)
	}
	// Name з marker-а: sidecar-ів немає, а slug у заголовку вікна — некрасиво.
	if res.Name != localTitleName {
		t.Fatalf("Name = %q", res.Name)
	}
}

// NoLocal — завантажувач: після збереженого 720p він має бачити мережеві
// потоки, інакше 1080p неможливо докачати.
func TestResolveWithNoLocalSkipsDisk(t *testing.T) {
	dir := t.TempDir()
	saveFile(t, dir, download.Parts{
		Title: localTitleName, Episode: 1, Studio: "FanVoxUA",
		Kind: provider.KindDub, Height: 720, Ext: ".ts",
	}, true)

	const streamURL = "https://video.invalid/stream.m3u8"
	engine := testEngine(
		[]provider.Source{{Studio: "FanVoxUA", Kind: provider.KindDub, Embed: "https://handled.invalid/embed", Episode: 1}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: streamURL, Quality: 1080}}}},
	)

	res, err := engine.ResolveWith(t.Context(), localRef, 1, Hints{DownloadDir: dir, NoLocal: true}, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if res.Local || res.Stream.URL != streamURL {
		t.Fatalf("Resolved = %+v, очікував мережевий потік", res)
	}
}

// Порожня папка завантажень нічого не змінює: звичайний мережевий шлях.
func TestResolveWithoutSavedFileGoesToNetwork(t *testing.T) {
	const streamURL = "https://video.invalid/stream.m3u8"
	engine := testEngine(
		[]provider.Source{{Studio: "FanVoxUA", Kind: provider.KindDub, Embed: "https://handled.invalid/embed", Episode: 2}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{{URL: streamURL}}}},
	)

	res, err := engine.ResolveWith(t.Context(), localRef, 2, Hints{DownloadDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if res.Local || res.Stream.URL != streamURL || res.HostID != "stub" {
		t.Fatalf("Resolved = %+v, очікував мережевий потік", res)
	}
}

// Файл на диску не вчить бібліотеку перевазі: завантаження її не торкається.
func TestBeginDoesNotPinStudioForLocalFile(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	engine := &Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{}}
	res := &Resolved{
		Ref:     localRef,
		Episode: 1,
		Local:   true,
		Source:  provider.Source{Studio: "FanVoxUA", Kind: provider.KindDub},
	}

	titleID, pinned, err := engine.Begin(res)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if pinned != "" {
		t.Fatalf("Begin закріпив студію %q за файлом із диска", pinned)
	}
	entry := engine.Lib.EntryLookup(titleID)
	if entry == nil {
		t.Fatalf("Begin не створив запис бібліотеки")
	}
	if entry.StudioPin != "" || entry.KindPin != "" {
		t.Fatalf("пін = (%q, %q), очікував порожній", entry.StudioPin, entry.KindPin)
	}
	if engine.Lib.TitleByRef(localRef) == nil {
		t.Fatalf("Begin не створив тайтл")
	}
}

// Мережевий шлях: усі якості релізу лишаються в Streams, у плеєр іде найкраща.
func TestResolveKeepsAllStreamsSortedByQuality(t *testing.T) {
	engine := testEngine(
		[]provider.Source{{Studio: "FanVoxUA", Kind: provider.KindDub, Embed: "https://handled.invalid/embed", Episode: 1}},
		[]extractor.Extractor{stubExtractor{streams: []extractor.Stream{
			{URL: "https://video.invalid/720.webm", Quality: 720},
			{URL: "https://video.invalid/1080.webm", Quality: 1080},
		}}},
	)

	res, err := engine.ResolveWith(t.Context(), localRef, 1, Hints{}, nil)
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if len(res.Streams) != 2 {
		t.Fatalf("Streams = %+v, очікував обидві якості", res.Streams)
	}
	if res.Streams[0].Quality != 1080 || res.Streams[1].Quality != 720 {
		t.Fatalf("Streams не за спаданням якості: %+v", res.Streams)
	}
	if res.Stream.URL != res.Streams[0].URL {
		t.Fatalf("Stream = %+v, очікував Streams[0]", res.Stream)
	}
}
