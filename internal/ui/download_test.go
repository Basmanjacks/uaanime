package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/download"
	"github.com/Basmanjacks/uaanime/internal/downloadtest"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// Завантаження в TUI: справжній менеджер, справжній HLS-стенд за підставним
// транспортом і справжня файлова система в t.TempDir. Мокати менеджер тут
// безглуздо — перевіряється саме стик «клавіша → черга → диск → рядок списку».

// dlHeaders — заголовки потоку, яких стенд вимагає на кожному запиті (так само
// поводиться moonanime).
var dlHeaders = map[string]string{
	"Referer":         "https://host-a.invalid/",
	"User-Agent":      "uaanime-test",
	"Accept":          "*/*",
	"Accept-Language": "uk-UA",
}

// hlsExtractor віддає адресу стенда замість справжнього хоста: решта шляху
// (резолв, Pick, план, черга) лишається продакшеновою.
type hlsExtractor struct{ url string }

func (hlsExtractor) ID() string          { return "hls-stub" }
func (hlsExtractor) Handles(string) bool { return true }
func (e hlsExtractor) Extract(context.Context, string, string) ([]extractor.Stream, error) {
	return []extractor.Stream{{URL: e.url, Headers: dlHeaders}}, nil
}

type dlHarness struct {
	m        Model
	mgr      *download.Manager
	srv      *downloadtest.HLSServer
	player   *playertest.Player
	dlDir    string
	dataDir  string
	settings *store.Config
}

// newDownloadModel — журнейна модель із чергою завантажень. Вікно 80×24, як у
// решті журнейних тестів.
func newDownloadModel(t *testing.T, opts downloadtest.HLSOptions, sessions ...*playertest.Session) *dlHarness {
	t.Helper()
	if opts.Referer == "" {
		opts.Referer = dlHeaders["Referer"]
	}
	srv := downloadtest.NewHLS(t, opts)
	dataDir, dlDir := t.TempDir(), t.TempDir()
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	f := download.NewFetcher(downloadtest.Transport(srv.Rewrites()))
	mgr := download.NewManager(f)
	t.Cleanup(mgr.Close)

	fp := &playertest.Player{Sessions: sessions}
	eng := &playback.Engine{
		Provider:        journeyProvider(),
		Extractors:      []extractor.Extractor{hlsExtractor{url: srv.URL()}},
		Store:           st,
		Lib:             lib,
		Player:          fp,
		DownloadDir:     dlDir,
		JournalInterval: time.Millisecond,
	}
	cfg := store.DefaultConfig()
	cfg.DownloadDir = dlDir
	m := New(eng, Options{Cfg: cfg, DataDir: dataDir, Downloads: mgr, Fetcher: f})
	m.refreshEvery = 0
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	return &dlHarness{m: m, mgr: mgr, srv: srv, player: fp, dlDir: dlDir, dataDir: dataDir, settings: cfg}
}

// openEpisodes — пошук → картка → список серій, тим самим шляхом, що й людина.
func (h *dlHarness) openEpisodes(t *testing.T) {
	t.Helper()
	tr := &trace{}
	h.m = press(t, h.m, tr, '/', "/")
	h.m.input.SetValue("фрірен")
	h.m = press(t, h.m, tr, tea.KeyEnter, "")
	h.m = press(t, h.m, tr, tea.KeyEnter, "")
	mustScreen(t, h.m, screenEpisodes)
}

// pressPlan — D на серії: клавіша ставить статус «готую», а відповідь плану
// приходить окремим повідомленням (команда з дедлайном, як і резолв).
func (h *dlHarness) pressPlan(t *testing.T) {
	t.Helper()
	m, cmd := pressTestKey(t, h.m, 'D', "D")
	if m.status != i18n.TuiDlPreparing {
		t.Fatalf("статус після D = %q, want %q", m.status, i18n.TuiDlPreparing)
	}
	if cmd == nil {
		t.Fatal("D не повернув команди підготовки")
	}
	h.m, _ = updateTestModel(t, m, cmd())
}

// waitState крутить знімок менеджера, доки завдання не дійде до потрібного
// стану; читати канал подій тут не можна — його читає модель.
func (h *dlHarness) waitState(t *testing.T, ep int, want download.State) download.Progress {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, p := range h.mgr.Snapshot() {
			if p.Episode == ep && p.State == want {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("серія %d не дійшла до стану %v; знімок = %+v", ep, want, h.mgr.Snapshot())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// sync — доставити моделі сигнал менеджера так само, як це зробила б
// downloadEventsCmd. Команду відповіді навмисно не виконуємо: вона блокується
// на каналі подій, а синхронний pump тесту чекав би вічно.
func (h *dlHarness) sync(t *testing.T) {
	t.Helper()
	h.m, _ = updateTestModel(t, h.m, downloadMsg{ok: true})
}

func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.Contains(d.Name(), ".part") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// Повний шлях: D → екран якості → Enter → черга → файл на диску → бейдж «на
// диску» → Enter грає з диска, не питаючи мережі.
func TestDownloadJourneyQualityToSavedAndLocalPlay(t *testing.T) {
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3},
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}))
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })

	h.pressPlan(t)
	mustScreen(t, h.m, screenDownloadQuality)
	plain := ansi.Strip(h.m.View().Content)
	for _, want := range []string{"1080p", "720p", "480p", i18n.TuiDlSizeNote} {
		if !strings.Contains(plain, want) {
			t.Errorf("екран якості без %q:\n%s", want, plain)
		}
	}
	// Курсор — на найвищій якості: саме її обирають за замовчуванням.
	if p, ok := h.m.list.SelectedItem().(item).payload.(payloadQuality); !ok || p.height != 1080 {
		t.Fatalf("курсор на %+v, want 1080p", h.m.list.SelectedItem())
	}
	// Заголовок називає, що саме зберігається: серія, тип, студія обраного
	// релізу (яку саме — вирішує той самий Pick, що й у відтворенні).
	title := h.m.dlTitle
	if !strings.HasPrefix(title, fmt.Sprintf(i18n.TuiDlQualityTitle, 1)) ||
		!strings.Contains(title, i18n.KindShort(provider.KindDub)) ||
		!strings.Contains(title, "UA") && !strings.Contains(title, "Amanogawa") {
		t.Errorf("заголовок екрана якості = %q", title)
	}

	// Enter — у чергу; екран повертається до серій, а не веде далі.
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenEpisodes)
	if !strings.Contains(h.m.status, i18n.Downloads(1)) {
		t.Errorf("статус після Enter = %q", h.m.status)
	}
	if got := len(h.mgr.Snapshot()); got != 1 {
		t.Fatalf("завдань у черзі = %d, want 1", got)
	}
	if got := episodeRow(t, h.m, 1).badge; got != i18n.TuiDlBadgeQueued && got != i18n.TuiDlBadgePreparing {
		t.Errorf("бейдж серії 1 одразу після Enter = %q", got)
	}

	done := h.waitState(t, 1, download.StateDone)
	h.sync(t)
	if !strings.Contains(h.m.status, i18n.Bytes(done.Bytes)) {
		t.Errorf("статус після завершення = %q, want розмір %s", h.m.status, i18n.Bytes(done.Bytes))
	}
	row := episodeRow(t, h.m, 1)
	if row.badge != i18n.TuiDlBadgeSaved || row.badgeWarn {
		t.Fatalf("рядок серії 1 = %+v, want бейдж «на диску»", row)
	}
	if !strings.Contains(row.meta, i18n.QualityLabel(1080)) {
		t.Errorf("мета збереженої серії = %q, want якість", row.meta)
	}
	if got := partFiles(t, h.dlDir); len(got) != 0 {
		t.Errorf(".part лишився: %v", got)
	}

	// D на збереженій серії більше не качає: каже, що вона вже на диску.
	h.m, _ = pressTestKey(t, h.m, 'D', "D")
	if want := fmt.Sprintf(i18n.TuiDlAlready, 1); h.m.status != want {
		t.Errorf("статус D на збереженій = %q, want %q", h.m.status, want)
	}

	// Enter — грає з диска: плеєр отримує шлях до файла, а не URL.
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })
	tr := &trace{}
	h.m = press(t, h.m, tr, tea.KeyEnter, "")
	starts := h.player.Starts()
	if len(starts) != 1 {
		t.Fatalf("запусків плеєра = %d, want 1", len(starts))
	}
	if !filepath.IsAbs(starts[0].URL) || len(starts[0].Headers) != 0 {
		t.Fatalf("плеєр отримав %+v, want абсолютний шлях без заголовків", starts[0])
	}
	if _, err := os.Stat(starts[0].URL); err != nil {
		t.Fatalf("файла немає: %v", err)
	}
	if !strings.Contains(strings.Join(tr.statuses, "\n"), i18n.TuiDlPlayingLocal) {
		t.Errorf("статуси = %v, want «%s»", tr.statuses, i18n.TuiDlPlayingLocal)
	}
}

// Друге D на тій самій серії не подвоює завдання: людина натиснула клавішу
// двічі, а не попросила два файли.
func TestDownloadDuplicateKeepsOneJob(t *testing.T) {
	release := make(chan struct{})
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 8})
	h.srv.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })

	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 2, download.StateRunning)
	h.sync(t)

	h.m, _ = pressTestKey(t, h.m, 'D', "D")
	if want := fmt.Sprintf(i18n.TuiDlAlreadyQueued, 2); h.m.status != want {
		t.Errorf("статус повторного D = %q, want %q", h.m.status, want)
	}
	mustScreen(t, h.m, screenEpisodes)
	if got := len(h.mgr.Snapshot()); got != 1 {
		t.Fatalf("завдань = %d, want 1", got)
	}
	if badge := episodeRow(t, h.m, 2).badge; !strings.HasPrefix(badge, strings.SplitN(i18n.TuiDlBadgeActive, " ", 2)[0]) {
		t.Errorf("бейдж активного завантаження = %q", badge)
	}
}

// Без менеджера (Options.Downloads == nil) D чесно каже, що завантажень немає.
func TestDownloadUnavailableWithoutManager(t *testing.T) {
	m, _, _ := journeyModel(t)
	tr := &trace{}
	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)

	m, cmd := pressTestKey(t, m, 'D', "D")
	if cmd != nil {
		t.Fatal("D без менеджера не має нічого запускати")
	}
	if m.status != i18n.TuiDlUnavailable || m.statusKind != statusWarning {
		t.Fatalf("статус = %q (%d)", m.status, m.statusKind)
	}
	mustScreen(t, m, screenEpisodes)
}

// Екран «Завантаження»: секція «зараз», X скасовує активне й не лишає .part,
// а бейдж домівки з'являється лише поки щось у роботі.
func TestDownloadsScreenCancelAndHomeBadge(t *testing.T) {
	release := make(chan struct{})
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 8})
	h.srv.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 3 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 3, download.StateRunning)
	h.sync(t)

	// Домівка: рядок «Завантаження» з бейджем прогресу.
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	mustScreen(t, h.m, screenHome)
	row := homeDownloadsRow(t, h.m)
	if row.badge == "" {
		t.Errorf("рядок домівки без бейджа: %+v", row)
	}

	// D з домівки відкриває екран «Завантаження».
	h.m, _ = pressTestKey(t, h.m, 'D', "D")
	mustScreen(t, h.m, screenDownloads)
	plain := ansi.Strip(h.m.View().Content)
	if !strings.Contains(plain, strings.ToUpper(i18n.TuiDlBlockNow)) {
		t.Errorf("немає секції «зараз»:\n%s", plain)
	}
	if !strings.Contains(plain, i18n.ActiveDownloads(1)) {
		t.Errorf("заголовок без кількості активних:\n%s", plain)
	}

	selectTestItem(t, &h.m, func(it item) bool { _, ok := it.payload.(payloadDownload); return ok })
	h.m, _ = pressTestKey(t, h.m, 'X', "X")
	h.waitState(t, 3, download.StateCancelled)
	h.sync(t)
	if got := partFiles(t, h.dlDir); len(got) != 0 {
		t.Fatalf("після скасування лишився .part: %v", got)
	}
	if h.m.status != fmt.Sprintf(i18n.TuiDlCancelled, 3) {
		t.Errorf("статус = %q", h.m.status)
	}

	// Скасоване завдання прибирається з екрана тим самим X.
	h.m, _ = pressTestKey(t, h.m, 'X', "X")
	for _, li := range h.m.list.Items() {
		if it, ok := li.(item); ok {
			if _, ok := it.payload.(payloadDownload); ok {
				t.Fatalf("рядок завдання не зник: %+v", it)
			}
		}
	}
	// Бейдж домівки зник разом із чергою.
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	mustScreen(t, h.m, screenHome)
	if row := homeDownloadsRow(t, h.m); row.badge != "" {
		t.Errorf("бейдж домівки лишився після скасування: %q", row.badge)
	}
}

func homeDownloadsRow(t *testing.T, m Model) item {
	t.Helper()
	for _, li := range m.list.Items() {
		if it, ok := li.(item); ok {
			if _, ok := it.payload.(payloadDownloads); ok {
				return it
			}
		}
	}
	t.Fatal("рядка «Завантаження» на домівці немає")
	return item{}
}

// Екран шляху: набраний шлях зберігається в конфіг і в рушій.
func TestSettingsDownloadDirSaves(t *testing.T) {
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3})
	next := filepath.Join(t.TempDir(), "кіно")

	h.m, _ = pressTestKey(t, h.m, ',', ",")
	mustScreen(t, h.m, screenSettings)
	selectTestItem(t, &h.m, func(it item) bool {
		p, ok := it.payload.(payloadSetting)
		return ok && p.id == settingDownloadDir
	})
	// ←/→ на цьому рядку нічого не змінює: значення не зі списку.
	before := h.m.cfg.DownloadDir
	h.m, _ = pressTestKey(t, h.m, tea.KeyRight, "")
	if h.m.cfg.DownloadDir != before || h.m.screen != screenSettings {
		t.Fatalf("←/→ змінило шлях: %q", h.m.cfg.DownloadDir)
	}

	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenDownloadDir)
	if !h.m.input.Focused() {
		t.Fatal("поле шляху не отримало фокус")
	}
	if got := h.m.input.Value(); got != shortenHome(before) {
		t.Fatalf("поле = %q, want поточний шлях %q", got, shortenHome(before))
	}
	h.m.input.SetValue(next)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenSettings)
	if h.m.cfg.DownloadDir != next || h.m.eng.DownloadDir != next {
		t.Fatalf("cfg = %q, eng = %q, want %q", h.m.cfg.DownloadDir, h.m.eng.DownloadDir, next)
	}
	if _, err := os.Stat(next); err != nil {
		t.Fatalf("папку не створено: %v", err)
	}
	saved, err := h.m.eng.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if saved.DownloadDir != next {
		t.Fatalf("на диску %q, want %q", saved.DownloadDir, next)
	}
	if h.m.status != i18n.TuiSetSaved {
		t.Errorf("статус = %q", h.m.status)
	}
}

// Поки черга жива, папку міняти не можна: файл, докачаний у стару, став би
// невидимим для пошуку збережених серій.
func TestSettingsDownloadDirRefusesWhileBusy(t *testing.T) {
	release := make(chan struct{})
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 8})
	h.srv.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 1, download.StateRunning)

	before := h.m.cfg.DownloadDir
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, ',', ",")
	mustScreen(t, h.m, screenSettings)
	selectTestItem(t, &h.m, func(it item) bool {
		p, ok := it.payload.(payloadSetting)
		return ok && p.id == settingDownloadDir
	})
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenDownloadDir)
	h.m.input.SetValue(filepath.Join(t.TempDir(), "інша"))
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenDownloadDir)
	if h.m.status != i18n.TuiDlPathBusy || h.m.statusKind != statusWarning {
		t.Fatalf("статус = %q (%d)", h.m.status, h.m.statusKind)
	}
	if h.m.cfg.DownloadDir != before {
		t.Fatalf("шлях змінився попри чергу: %q", h.m.cfg.DownloadDir)
	}
}

// Шлях без доступу на запис не зберігається — і каже про це, а не мовчить.
func TestSettingsDownloadDirRejectsUnwritable(t *testing.T) {
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3})
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	target := filepath.Join(parent, "нова")

	h.m, _ = pressTestKey(t, h.m, ',', ",")
	selectTestItem(t, &h.m, func(it item) bool {
		p, ok := it.payload.(payloadSetting)
		return ok && p.id == settingDownloadDir
	})
	before := h.m.cfg.DownloadDir
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.m.input.SetValue(target)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	mustScreen(t, h.m, screenDownloadDir)
	if !strings.HasPrefix(h.m.errText, strings.SplitN(i18n.TuiDlPathNoWrite, ":", 2)[0]) {
		t.Fatalf("помилка = %q, want «немає доступу»", h.m.errText)
	}
	if h.m.cfg.DownloadDir != before {
		t.Fatalf("шлях змінився: %q", h.m.cfg.DownloadDir)
	}

	// Відносний шлях теж не проходить: він означав би різні папки залежно
	// від того, звідки запустили застосунок.
	h.m.input.SetValue("кіно")
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	if h.m.errText != i18n.TuiDlPathInvalid {
		t.Fatalf("помилка на відносному шляху = %q", h.m.errText)
	}
}

// Вихід під час завантаження: клавіша спершу попереджає, друге натискання
// виходить і прибирає за собою; сигнал не питає ніколи.
func TestDownloadQuitArmsOnceThenCloses(t *testing.T) {
	release := make(chan struct{})
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 8})
	h.srv.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 1, download.StateRunning)

	m, cmd := updateTestModel(t, h.m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("перший Ctrl+C під час завантаження не має завершувати застосунок")
	}
	if m.status != i18n.TuiDlQuitWarn || !m.quitArmed {
		t.Fatalf("статус = %q, quitArmed = %v", m.status, m.quitArmed)
	}
	// Будь-яка інша клавіша знімає зведення.
	disarmed, _ := pressTestKey(t, m, tea.KeyDown, "")
	if disarmed.quitArmed {
		t.Fatal("інша клавіша мала скинути попередження")
	}

	m, cmd = updateTestModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("друге Ctrl+C має завершувати застосунок")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("друге Ctrl+C має повернути tea.Quit")
	}
	if got := partFiles(t, h.dlDir); len(got) != 0 {
		t.Fatalf("після виходу лишився .part: %v", got)
	}
	for _, p := range m.dl.Snapshot() {
		if !p.State.Terminal() {
			t.Fatalf("завдання не зупинене: %+v", p)
		}
	}
}

// Сигнал виходить одразу: процес, який не вмирає від SIGTERM, ламає все,
// що ним керує.
func TestDownloadSignalQuitsImmediately(t *testing.T) {
	release := make(chan struct{})
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 8})
	h.srv.BlockSegment(5, release)
	t.Cleanup(func() { close(release) })
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 1, download.StateRunning)

	m, cmd := updateTestModel(t, h.m, signalMsg{})
	if cmd == nil {
		t.Fatal("сигнал має завершувати застосунок одразу")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("сигнал має повернути tea.Quit")
	}
	if m.quitArmed {
		t.Fatal("сигнал не має нічого зводити")
	}
	if got := partFiles(t, h.dlDir); len(got) != 0 {
		t.Fatalf("після сигналу лишився .part: %v", got)
	}
}

// Кадри нових екранів на всіх розмірах із брифу: нічого не ширше за вікно і
// нічого не вище за нього.
func TestDownloadFramesFitWindow(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {43, 18}, {40, 12}, {20, 5}}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3})
			h.m, _ = updateTestModel(t, h.m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
			strict := size.w >= 40 && size.h >= 12
			check := func(name string) {
				t.Helper()
				lines := strings.Split(h.m.View().Content, "\n")
				if !strict {
					return
				}
				if got := len(lines); got > size.h {
					t.Errorf("%s: %d рядків у вікні %d", name, got, size.h)
				}
				for i, line := range lines {
					if w := lipgloss.Width(line); w > size.w {
						t.Errorf("%s: рядок %d ширший за вікно (%d > %d): %q", name, i, w, size.w, ansi.Strip(line))
					}
				}
			}

			h.openEpisodes(t)
			h.pressPlan(t)
			mustScreen(t, h.m, screenDownloadQuality)
			check("quality")

			h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
			h.waitState(t, 1, download.StateDone)
			h.sync(t)
			check("episodes-saved")

			h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
			h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
			mustScreen(t, h.m, screenHome)
			h.m, _ = pressTestKey(t, h.m, 'D', "D")
			mustScreen(t, h.m, screenDownloads)
			check("downloads")

			h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
			h.m, _ = pressTestKey(t, h.m, ',', ",")
			selectTestItem(t, &h.m, func(it item) bool {
				p, ok := it.payload.(payloadSetting)
				return ok && p.id == settingDownloadDir
			})
			check("settings")
			h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
			mustScreen(t, h.m, screenDownloadDir)
			check("download-dir")
		})
	}
}

// Екран «Завантаження» показує збережене по тайтлах, і Enter на файлі грає
// його з диска — без жодного запиту до провайдера.
func TestDownloadsScreenPlaysSavedFile(t *testing.T) {
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3},
		playertest.NewSession(player.EndQuit, []float64{12}, []float64{1440}))
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 2, download.StateDone)
	h.sync(t)

	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	mustScreen(t, h.m, screenHome)
	h.m, _ = pressTestKey(t, h.m, 'D', "D")
	mustScreen(t, h.m, screenDownloads)
	plain := ansi.Strip(h.m.View().Content)
	if !strings.Contains(plain, strings.ToUpper(journeyRef.Name)) {
		t.Errorf("немає секції тайтлу:\n%s", plain)
	}

	selectTestItem(t, &h.m, func(it item) bool { _, ok := it.payload.(payloadSavedFile); return ok })
	tr := &trace{}
	h.m = press(t, h.m, tr, tea.KeyEnter, "")
	starts := h.player.Starts()
	if len(starts) != 1 || !filepath.IsAbs(starts[0].URL) {
		t.Fatalf("запуски плеєра = %+v", starts)
	}
}

// Невдале завантаження: бейдж попередження на серії, рядок з причиною на
// екрані «Завантаження» і Enter, що пробує ще раз.
func TestDownloadFailedBadgeAndRetry(t *testing.T) {
	h := newDownloadModel(t, downloadtest.HLSOptions{Segments: 3})
	// 403 — підписане посилання протухло: повторювати сегмент немає сенсу,
	// завдання падає одразу.
	h.srv.FailSegment(1, 10, 403)
	h.openEpisodes(t)
	selectTestItem(t, &h.m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 1 })
	h.pressPlan(t)
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	h.waitState(t, 1, download.StateFailed)
	h.sync(t)

	row := episodeRow(t, h.m, 1)
	if row.badge != i18n.TuiDlBadgeFailed || !row.badgeWarn {
		t.Fatalf("рядок серії 1 = %+v, want бейдж «не завантажилось»", row)
	}
	if want := fmt.Sprintf(i18n.TuiDlFailed, 1, i18n.MsgStreamExpired); h.m.status != want {
		t.Errorf("статус = %q, want %q", h.m.status, want)
	}
	if got := partFiles(t, h.dlDir); len(got) != 0 {
		t.Fatalf("після падіння лишився .part: %v", got)
	}

	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, tea.KeyEsc, "")
	h.m, _ = pressTestKey(t, h.m, 'D', "D")
	mustScreen(t, h.m, screenDownloads)
	selectTestItem(t, &h.m, func(it item) bool { _, ok := it.payload.(payloadDownload); return ok })
	if got := h.m.list.SelectedItem().(item); got.badge != i18n.TuiDlBadgeFailed || got.meta == "" {
		t.Fatalf("рядок невдалого завдання = %+v", got)
	}
	// Enter — повторити: завдання знову в черзі (і знову впаде, стенд той самий).
	h.m, _ = pressTestKey(t, h.m, tea.KeyEnter, "")
	if got := len(h.mgr.Snapshot()); got != 1 {
		t.Fatalf("завдань після повтору = %d, want 1", got)
	}
	h.waitState(t, 1, download.StateFailed)
}
