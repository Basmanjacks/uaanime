package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/provider/anitube"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// Наскрізні сценарії: справжні провайдер, екстрактори, сховище, бібліотека,
// i18n і CLI-вивід на фікстурах; фейковий — лише плеєр. Кожен сценарій
// відповідає критерію приймання з docs/build-brief.md.

// fault — один інжектований збій: offline (помилка мережі) або порожня
// відповідь 200 (хост живий, але потоку в ній немає).
type fault int

const (
	faultOffline fault = iota + 1
	faultEmptyBody
)

// faultTransport підміняє відповіді для точних URL (пріоритет) або цілих
// доменів; решту делегує фікстурам. Мапи захищені м'ютексом: тест змінює
// їх між викликами, а рушій ходить у транспорт із фонових горутин.
type faultTransport struct {
	mu    sync.Mutex
	urls  map[string]fault
	hosts map[string]fault
	next  http.RoundTripper
}

func newFaultTransport() *faultTransport {
	return &faultTransport{urls: map[string]fault{}, hosts: map[string]fault{}, next: fixtureTransport()}
}

func (t *faultTransport) failURL(u string, f fault)  { t.mu.Lock(); t.urls[u] = f; t.mu.Unlock() }
func (t *faultTransport) failHost(h string, f fault) { t.mu.Lock(); t.hosts[h] = f; t.mu.Unlock() }
func (t *faultTransport) reset()                     { t.mu.Lock(); clear(t.urls); clear(t.hosts); t.mu.Unlock() }
func (t *faultTransport) lookup(req *http.Request) fault {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.urls[req.URL.String()]; ok {
		return f
	}
	return t.hosts[req.URL.Host]
}

func (t *faultTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch t.lookup(req) {
	case faultOffline:
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("інжектований збій мережі")}
	case faultEmptyBody:
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil)), Header: http.Header{}}, nil
	}
	return t.next.RoundTrip(req)
}

// journeyEnv — cliEnv плюс шви: фейковий плеєр, миттєвий журнал, транспорт
// зі збоями. Повертає каталог даних, транспорт і плеєр.
func journeyEnv(t *testing.T, sessions ...*playertest.Session) (dir string, ft *faultTransport, fp *playertest.Player) {
	t.Helper()
	dir = cliEnv(t)
	ft = newFaultTransport()
	fp = &playertest.Player{IDValue: "vlc", Sessions: sessions}

	savedT, savedD, savedJ := newTransport, detectPlayer, journalInterval
	newTransport = func() http.RoundTripper { return ft }
	detectPlayer = func(string) (player.Player, bool, error) { return fp, false, nil }
	journalInterval = time.Millisecond
	t.Cleanup(func() { newTransport, detectPlayer, journalInterval = savedT, savedD, savedJ })
	return dir, ft, fp
}

// journeyEngine — рушій так, як його збирає застосунок, але з фейковим плеєром.
func journeyEngine(t *testing.T, ft *faultTransport, fp *playertest.Player) *playback.Engine {
	t.Helper()
	a, err := newAppWith(ft, false)
	if err != nil {
		t.Fatalf("newAppWith: %v", err)
	}
	eng := a.engineWithoutPlayer()
	eng.Player = fp
	eng.JournalInterval = time.Millisecond
	return eng
}

func fixtureRef(t *testing.T) provider.TitleRef {
	t.Helper()
	ref, err := anitube.RefFromSlug(strings.TrimPrefix(fixtureTitleID, "anitube:"))
	if err != nil {
		t.Fatalf("RefFromSlug: %v", err)
	}
	return ref
}

func loadLibrary(t *testing.T, dir string) *library.Library {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	return lib
}

func mustExit(t *testing.T, want, got int, out, errOut string) {
	t.Helper()
	if got != want {
		t.Fatalf("код виходу = %d, want %d\nstdout: %s\nstderr: %s", got, want, out, errOut)
	}
}

// J1 — критерій Phase 2: «вихід на 14:32 → наступний запуск пропонує
// продовжити з 14:32». Реальний шлях cmdPlay: Resolve → Begin → Run → Finish.
func TestJourneyResumeAfterQuit(t *testing.T) {
	dir, _, fp := journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{100, 500, 872}, []float64{1440}))

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgProgressSaved, 14, 32))
	mustContain(t, "stdout", out, strings.SplitN(i18n.MsgStudioPinned, "%", 2)[0])
	if strings.Contains(out, strings.SplitN(i18n.MsgResume, "%", 2)[0]) {
		t.Errorf("перший перегляд не має пропонувати resume:\n%s", out)
	}

	starts := fp.Starts()
	if len(starts) != 1 {
		t.Fatalf("плеєр запущено %d разів, want 1", len(starts))
	}
	if starts[0].StartSec != 0 || !strings.HasPrefix(starts[0].URL, "https://") || !strings.HasSuffix(starts[0].MediaTitle, " · 1") {
		t.Errorf("перший Start = %+v", starts[0])
	}

	lib := loadLibrary(t, dir)
	title := lib.TitleByRef(fixtureRef(t))
	if title == nil {
		t.Fatal("тайтл не з'явився у бібліотеці")
	}
	entry := lib.EntryLookup(title.ID)
	if entry == nil || entry.State != library.StateWatching || entry.StudioPin == "" {
		t.Fatalf("entry = %+v, want watching з піном студії", entry)
	}
	if p := lib.ProgressFor(title.ID, 1); p == nil || p.PositionSec != 872 || p.DurationSec != 1440 || p.Completed {
		t.Fatalf("progress = %+v, want 872/1440, не завершено", p)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "current.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("журнал має бути злитий і видалений після Finish: %v", err)
	}

	// наступний запуск: resume з тієї ж позиції і та сама студія без питань
	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgResume, 14, 32))
	if got := regexpAll(out, i18n.MsgPickedSource); len(got) != 1 || got[0] != entry.StudioPin {
		t.Errorf("студія при resume = %v, want пін %q", got, entry.StudioPin)
	}
	mustContain(t, "argv", lastLine(out), "--start-time=872.0")
}

// J2 — DoD «kill -9 під час гри → втрачено ≤ 10 с»: процес зникає після
// Run без Finish; наступний старт застосунку зливає журнал у бібліотеку.
func TestJourneyJournalSurvivesCrash(t *testing.T) {
	dir, ft, fp := journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{60, 125}, []float64{1440}))
	eng := journeyEngine(t, ft, fp)
	ref := fixtureRef(t)

	res, err := eng.Resolve(t.Context(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	titleID, _, err := eng.Begin(res)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if reason, err := eng.Run(t.Context(), res, titleID); err != nil || reason != player.EndQuit {
		t.Fatalf("Run = %v, %v", reason, err)
	}
	// Finish навмисно не викликається — «kill -9»

	journalPath := filepath.Join(dir, "state", "current.json")
	var j store.Journal
	b, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("журнал має існувати після Run: %v", err)
	}
	if err := json.Unmarshal(b, &j); err != nil || j.PositionSec != 125 || j.Episode != 1 || j.TitleID != titleID {
		t.Fatalf("журнал = %+v (%v), want позиція 125 серії 1", j, err)
	}
	if lib := loadLibrary(t, dir); lib.ProgressFor(titleID, 1) != nil {
		t.Fatal("бібліотека не має знати про прогрес до злиття журналу")
	}

	// новий запуск застосунку
	a, err := newAppWith(ft, false)
	if err != nil {
		t.Fatalf("newAppWith: %v", err)
	}
	if _, err := os.Stat(journalPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("журнал має бути видалений після злиття: %v", err)
	}
	if p := a.lib.ProgressFor(titleID, 1); p == nil || p.PositionSec != 125 {
		t.Fatalf("progress після відновлення = %+v, want 125", p)
	}
	if h := a.engineWithoutPlayer().ResolveHints(ref, 1); h.StartSec != 125 || h.StudioPin == "" {
		t.Fatalf("ResolveHints = %+v, want StartSec 125 і пін", h)
	}
}

// J3 — autoplay: EOF першої серії тягне другу тією самою озвучкою без питань.
func TestJourneyAutoplayAfterEOF(t *testing.T) {
	dir, _, fp := journeyEnv(t,
		playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}),
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"autoplay":"always"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgEpisodeDone, 1))
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgResolving, 2))
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgProgressSaved, 0, 30))

	starts := fp.Starts()
	if len(starts) != 2 {
		t.Fatalf("плеєр запущено %d разів, want 2\n%s", len(starts), out)
	}
	if !strings.HasSuffix(starts[0].MediaTitle, " · 1") || !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Errorf("MediaTitle = %q, %q", starts[0].MediaTitle, starts[1].MediaTitle)
	}
	if starts[1].StartSec != 0 {
		t.Errorf("друга серія має йти з початку, StartSec = %v", starts[1].StartSec)
	}
	if strings.Count(out, strings.SplitN(i18n.MsgStudioPinned, "%", 2)[0]) != 1 {
		t.Errorf("студія закріплюється рівно один раз:\n%s", out)
	}
	// обидва запуски — та сама студія (пін після першого перегляду)
	picked := regexpAll(out, i18n.MsgPickedSource)
	if len(picked) != 2 || picked[0] != picked[1] {
		t.Errorf("студії двох серій = %v, want однакові", picked)
	}

	lib := loadLibrary(t, dir)
	title := lib.TitleByRef(fixtureRef(t))
	if p := lib.ProgressFor(title.ID, 1); p == nil || !p.Completed {
		t.Errorf("серія 1 має бути завершена: %+v", p)
	}
	if p := lib.ProgressFor(title.ID, 2); p == nil || p.Completed || p.PositionSec != 30 {
		t.Errorf("серія 2 = %+v, want 30 с, не завершена", p)
	}
}

// regexpAll витягує студію з кожного рядка «Обрано: <студія> (…)».
func regexpAll(out, format string) []string {
	prefix := strings.SplitN(format, "%", 2)[0]
	var studios []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, prefix); ok {
			studios = append(studios, strings.SplitN(rest, " (", 2)[0])
		}
	}
	return studios
}

// hostsOf групує джерела серії за студією → підтримувані хости.
func hostsOf(eng *playback.Engine, sources []provider.Source) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, s := range sources {
		if _, ok := extractor.Find(eng.Extractors, s.Embed); !ok {
			continue
		}
		u, err := url.Parse(s.Embed)
		if err != nil {
			continue
		}
		if out[s.Studio] == nil {
			out[s.Studio] = map[string]bool{}
		}
		out[s.Studio][u.Host] = true
	}
	return out
}

// J4a — мертвий хост: та сама студія, інший хост, мовчазний fallback.
func TestJourneyDeadHostFallsBackWithinStudio(t *testing.T) {
	_, ft, fp := journeyEnv(t)
	eng := journeyEngine(t, ft, fp)
	ref := fixtureRef(t)

	sources, err := eng.Provider.Sources(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	var studio string
	for s, hosts := range hostsOf(eng, sources) {
		if len(hosts) >= 2 && (studio == "" || s < studio) {
			studio = s
		}
	}
	if studio == "" {
		t.Fatal("фікстура не має студії з ≥2 підтримуваними хостами")
	}
	if err := eng.PinStudio(ref, studio); err != nil {
		t.Fatalf("PinStudio: %v", err)
	}

	first, err := eng.Resolve(t.Context(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.Source.Studio != studio || first.PinFallback {
		t.Fatalf("перший Resolve = %s/%s fallback=%v, want пін %s", first.Source.Studio, first.HostID, first.PinFallback, studio)
	}
	deadHost := mustHost(t, first.Source.Embed)
	ft.failHost(deadHost, faultOffline)

	events := 0
	second, err := journeyEngine(t, ft, fp).Resolve(t.Context(), ref, 1, func(playback.Event) { events++ })
	if err != nil {
		t.Fatalf("Resolve після збою хоста: %v", err)
	}
	if events == 0 {
		t.Error("очікував EventTryingNext")
	}
	if second.Source.Studio != studio || second.PinFallback {
		t.Errorf("студія після збою хоста = %s (fallback=%v), want %s", second.Source.Studio, second.PinFallback, studio)
	}
	if second.HostID == first.HostID || mustHost(t, second.Source.Embed) == deadHost {
		t.Errorf("хост не змінився: %s → %s", first.HostID, second.HostID)
	}
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("embed %q: %v", raw, err)
	}
	return u.Host
}

// J4b — мертва студія: усі її embed-и не відповідають; грає інша озвучка,
// ніколи не субтитри (правило 4), а людині про це кажуть.
func TestJourneyDeadStudioFallsBackToOtherDub(t *testing.T) {
	_, ft, fp := journeyEnv(t)
	eng := journeyEngine(t, ft, fp)
	ref := fixtureRef(t)

	sources, err := eng.Provider.Sources(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("Sources: %v", err)
	}
	pinned, _ := library.Pick(sources, library.Pin{}, eng.Prefs)
	if pinned == nil {
		t.Fatal("Pick не обрав джерело")
	}
	studio := pinned.Studio
	if err := eng.PinStudio(ref, studio); err != nil {
		t.Fatalf("PinStudio: %v", err)
	}
	for _, s := range sources {
		if s.Studio == studio {
			ft.failURL(s.Embed, faultOffline)
		}
	}

	res, err := journeyEngine(t, ft, fp).Resolve(t.Context(), ref, 1, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Source.Studio == studio || !res.PinFallback {
		t.Fatalf("Resolve = %s fallback=%v, want іншу студію з PinFallback", res.Source.Studio, res.PinFallback)
	}
	if res.Source.Kind == provider.KindSub {
		t.Errorf("є інші озвучення, а обрано субтитри: %+v", res.Source)
	}

	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stderr", errOut, fmt.Sprintf(i18n.TuiStudioFallback, studio, res.Source.Studio))
}

// J5 — офлайн: три різні поведінки залежно від кешу.
func TestJourneyOffline(t *testing.T) {
	dir, ft, _ := journeyEnv(t)
	allHosts := []string{"anitube.in.ua", "ashdi.vip", "tortuga.tw", "moonanime.art"}
	goOffline := func() {
		for _, h := range allHosts {
			ft.failHost(h, faultOffline)
		}
	}

	t.Run("без кешу", func(t *testing.T) {
		goOffline()
		defer ft.reset()
		for _, args := range [][]string{
			{"episodes", fixtureTitleID},
			{"search", "фрірен"},
			{"resolve", fixtureTitleID, "1"},
		} {
			code, out, errOut := runCLI(t, args...)
			mustExit(t, 1, code, out, errOut)
			if got := strings.TrimSpace(errOut); got != i18n.MsgOffline {
				t.Errorf("%v: stderr = %q, want %q", args, got, i18n.MsgOffline)
			}
		}
	})

	// прогріваємо кеш серій онлайн
	code, out, errOut := runCLI(t, "episodes", fixtureTitleID)
	mustExit(t, 0, code, out, errOut)
	online := out
	cacheFiles, _ := filepath.Glob(filepath.Join(dir, "cache", "episodes-*.json"))
	if len(cacheFiles) != 1 {
		t.Fatalf("кеш серій: %v", cacheFiles)
	}

	t.Run("свіжий кеш", func(t *testing.T) {
		goOffline()
		defer ft.reset()
		code, out, errOut := runCLI(t, "episodes", fixtureTitleID)
		mustExit(t, 0, code, out, errOut)
		if out != online || strings.TrimSpace(errOut) != "" {
			t.Errorf("свіжий кеш має віддаватися без попереджень; stderr=%q", errOut)
		}
	})

	t.Run("застарілий кеш", func(t *testing.T) {
		ageCache(t, cacheFiles[0], 7*time.Hour)
		goOffline()
		defer ft.reset()
		code, out, errOut := runCLI(t, "episodes", fixtureTitleID)
		mustExit(t, 0, code, out, errOut)
		if out != online {
			t.Errorf("застарілий кеш має віддати ті самі серії")
		}
		if got := strings.TrimSpace(errOut); got != i18n.MsgOfflineCache {
			t.Errorf("stderr = %q, want %q", got, i18n.MsgOfflineCache)
		}
	})
}

// ageCache переписує fetched_at, щоб кеш вийшов за TTL.
func ageCache(t *testing.T, path string, age time.Duration) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c map[string]json.RawMessage
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	stamp, _ := json.Marshal(time.Now().Add(-age))
	c["fetched_at"] = stamp
	b, _ = json.Marshal(c)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// J6 — хости живі, але потоку не віддають: третій клас помилки, не офлайн.
func TestJourneyNoStreamIsNotOffline(t *testing.T) {
	_, ft, _ := journeyEnv(t)
	for _, h := range []string{"ashdi.vip", "tortuga.tw", "moonanime.art"} {
		ft.failHost(h, faultEmptyBody)
	}
	code, out, errOut := runCLI(t, "resolve", fixtureTitleID, "1")
	mustExit(t, 1, code, out, errOut)
	want := i18n.ErrorText(errs.ErrNoStream)
	if got := strings.TrimSpace(errOut); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if strings.Contains(errOut, i18n.MsgOffline) {
		t.Error("порожня відповідь хоста — не офлайн")
	}
}

// J7 — бекап переносить прогрес і пін: у новому каталозі resume той самий.
func TestJourneyExportImportKeepsResume(t *testing.T) {
	journeyEnv(t, playertest.NewSession(player.EndQuit, []float64{872}, []float64{1440}))
	code, out, errOut := runCLI(t, "play", fixtureTitleID, "1")
	mustExit(t, 0, code, out, errOut)
	studio := regexpAll(out, i18n.MsgPickedSource)
	if len(studio) != 1 {
		t.Fatalf("студія: %v", studio)
	}

	code, backup, errOut := runCLI(t, "export")
	mustExit(t, 0, code, backup, errOut)
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := os.WriteFile(path, []byte(backup), 0o600); err != nil {
		t.Fatal(err)
	}

	// новий, порожній каталог даних
	t.Setenv("UAANIME_DATA_DIR", t.TempDir())
	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	mustExit(t, 0, code, out, errOut)
	if strings.Contains(out, strings.SplitN(i18n.MsgResume, "%", 2)[0]) {
		t.Fatal("порожній каталог не має пам'ятати resume")
	}
	code, out, errOut = runCLI(t, "import", path)
	mustExit(t, 0, code, out, errOut)
	code, out, errOut = runCLI(t, "play", fixtureTitleID, "1", "--dry-run")
	mustExit(t, 0, code, out, errOut)
	mustContain(t, "stdout", out, fmt.Sprintf(i18n.MsgResume, 14, 32))
	if got := regexpAll(out, i18n.MsgPickedSource); len(got) != 1 || got[0] != studio[0] {
		t.Errorf("студія після імпорту = %v, want %v", got, studio)
	}
}
