// uaanime — термінальний перегляд аніме українською.
//
// Phase 1: headless-команди (search/episodes/resolve/play). TUI — Phase 3.
// UAANIME_FIXTURES=1 підміняє мережу файлами testdata/ (запуск з кореня репозиторію).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/extractor/ashdi"
	"github.com/Basmanjacks/uaanime/internal/extractor/moonanime"
	"github.com/Basmanjacks/uaanime/internal/extractor/tortuga"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/provider/anitube"
	"github.com/Basmanjacks/uaanime/internal/store"
	"github.com/Basmanjacks/uaanime/internal/ui"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// stdout/stderr — єдині виходи headless-команд. Змінні, а не os.Stdout напряму,
// щоб табличні тести run() читали вивід без підміни файлових дескрипторів.
// Перевірка «це термінал?» у runTUI лишається на справжньому os.Stdout.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// Помилку запису у власний термінал перевіряти нема сенсу: повідомити про неї
// теж нікуди. Обгортки роблять це рішення явним один раз замість `_, _ =`
// біля кожного з сорока викликів.
func outf(format string, a ...any) { _, _ = fmt.Fprintf(stdout, format, a...) }
func outln(a ...any)               { _, _ = fmt.Fprintln(stdout, a...) }
func errf(format string, a ...any) { _, _ = fmt.Fprintf(stderr, format, a...) }
func errln(a ...any)               { _, _ = fmt.Fprintln(stderr, a...) }

type app struct {
	provider   provider.Provider
	extractors []extractor.Extractor
	store      *store.Store
	lib        *library.Library
	cfg        *store.Config
	dataDir    string
}

func newApp(readOnly bool) (*app, error) { return newAppWith(newTransport(), readOnly) }

// newTransport — шов для тестів: наскрізні сценарії підставляють фікстури
// з інжекцією збоїв мережі. У продакшні — мережа або фікстури за env.
var newTransport = defaultTransport

func defaultTransport() http.RoundTripper {
	if os.Getenv("UAANIME_FIXTURES") == "1" {
		return fixtureTransport()
	}
	return nil
}

// fixtureTransport — усі фікстурні транспорти разом; шляхи відносні до кореня
// репозиторію. Окремою функцією, щоб наскрізні тести могли обгорнути його
// інжекцією збоїв мережі.
func fixtureTransport() httpx.MultiTransport {
	return httpx.MultiTransport{
		anitube.FixtureTransport("internal/provider/anitube/testdata"),
		ashdi.FixtureTransport("internal/extractor/ashdi/testdata"),
		tortuga.FixtureTransport("internal/extractor/tortuga/testdata"),
		moonanime.FixtureTransport("internal/extractor/moonanime/testdata"),
	}
}

// detectPlayer і journalInterval — шви для тестів: наскрізні сценарії
// підставляють фейковий плеєр і короткий крок журналу, проходячи справжній
// шлях Begin → Run → Finish. journalInterval 0 = дефолт рушія (5 с).
var (
	detectPlayer    = player.Detect
	journalInterval time.Duration
	// newLive — той самий шов для вікна в сесію: наскрізний сценарій має
	// дотягнутися до Live рівно так, як це робить пульт зі своєї горутини.
	newLive = func() *playback.Live { return &playback.Live{} }
)

// newAppWith збирає застосунок поверх заданого транспорту (nil — мережа).
// readOnly пропускає злиття журналу: команда, запущена ПІД ЧАС відтворення
// (doctor — щоб побачити адресу пульта), інакше з'їла б журнал активної
// сесії, а TUI зі старою бібліотекою в пам'яті перезаписав би відновлений
// прогрес на Finish.
func newAppWith(rt http.RoundTripper, readOnly bool) (*app, error) {
	client := httpx.NewClient(rt)

	dir, err := store.DataDir()
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		return nil, err
	}
	cfg, err := st.LoadConfig()
	if err != nil {
		return nil, err
	}
	// вцілілий після збою журнал зливається на старті
	if !readOnly {
		if _, err := st.RecoverJournal(lib); err != nil {
			return nil, err
		}
	}
	return &app{
		provider:   anitube.New(client),
		extractors: []extractor.Extractor{ashdi.New(client), tortuga.New(client), moonanime.New(client)},
		store:      st,
		lib:        lib,
		cfg:        cfg,
		dataDir:    dir,
	}, nil
}

// options — прапорці, спільні для всіх команд; кожна бере лише ті, що розуміє.
type options struct {
	json   bool
	dryRun bool
}

// command — одна headless-команда. args завжди починається з імені команди,
// тому індекси збігаються з тим, що набрав користувач.
// maxArgs == 0 означає «без верхньої межі» (search склеює решту в запит).
type command struct {
	minArgs int
	maxArgs int
	run     func(a *app, ctx context.Context, args []string, opt options) int
	// readOnly — команду можна запускати під час відтворення (див. newAppWith).
	readOnly bool
}

// Таблиця — єдине джерело правди про арність команд: раніше вона жила
// одночасно в мапі minArgs і в повторних перевірках len(positional) у switch.
var commands = map[string]command{
	"doctor": {readOnly: true, minArgs: 1, maxArgs: 1, run: func(a *app, ctx context.Context, _ []string, opt options) int {
		return a.cmdDoctor(ctx, opt.json)
	}},
	"export": {minArgs: 1, maxArgs: 1, run: func(a *app, _ context.Context, _ []string, _ options) int {
		if err := a.store.Export(stdout); err != nil {
			errln(err)
			return 1
		}
		return 0
	}},
	"import": {minArgs: 2, maxArgs: 2, run: func(a *app, _ context.Context, args []string, _ options) int {
		return a.cmdImport(args[1])
	}},
	"search": {minArgs: 2, run: func(a *app, ctx context.Context, args []string, opt options) int {
		return a.cmdSearch(ctx, strings.Join(args[1:], " "), opt.json)
	}},
	"episodes": {minArgs: 2, maxArgs: 2, run: func(a *app, ctx context.Context, args []string, opt options) int {
		return a.cmdEpisodes(ctx, args[1], opt.json)
	}},
	"resolve": {minArgs: 3, maxArgs: 3, run: func(a *app, ctx context.Context, args []string, opt options) int {
		ep, ok := parseEpisode(args[2])
		if !ok {
			return 2
		}
		return a.cmdResolve(ctx, args[1], ep, opt.json)
	}},
	"play": {minArgs: 3, maxArgs: 3, run: func(a *app, ctx context.Context, args []string, opt options) int {
		ep, ok := parseEpisode(args[2])
		if !ok {
			return 2
		}
		return a.cmdPlay(ctx, args[1], ep, opt.dryRun)
	}},
}

func run(args []string) (code int) {
	// жоден panic не долітає до користувача — ані з TUI, ані з headless-команди
	defer func() {
		if r := recover(); r != nil {
			errf(i18n.MsgInternalError+"\n", r)
			code = 1
		}
	}()

	var positional []string
	var opt options
	for _, a := range args {
		switch a {
		case "--json":
			opt.json = true
		case "--dry-run":
			opt.dryRun = true
		default:
			positional = append(positional, a)
		}
	}
	// без аргументів — TUI (потрібен термінал)
	if len(positional) == 0 {
		return runTUI()
	}

	cmd, ok := commands[positional[0]]
	if !ok || len(positional) < cmd.minArgs || (cmd.maxArgs > 0 && len(positional) > cmd.maxArgs) {
		errln(i18n.MsgUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	a, err := newApp(cmd.readOnly)
	if err != nil {
		errln(err)
		return 1
	}
	return cmd.run(a, ctx, positional, opt)
}

// shellQuote друкує argv так, щоб рядок можна було вставити в оболонку без
// змін: назва тайтлу містить пробіли, а адреса потоку — `?` і `&`.
// Керуючі символи лишаються як є — --dry-run має показувати справжній argv,
// а не його очищену копію; вони вже всередині лапок, а сам текст назви
// почистив provider.CleanText вище за течією.
func shellQuote(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, shellQuoteArg(a))
	}
	return strings.Join(out, " ")
}

func shellQuoteArg(s string) string {
	if s != "" && !strings.ContainsFunc(s, shellNeedsQuote) {
		return s
	}
	// POSIX: усередині одинарних лапок спецсимволів немає, а сама лапка
	// закривається, екранується і відкривається знову.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellNeedsQuote(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", r)
}

func runTUI() (code int) {
	// жоден panic не долітає до користувача: bubbletea відновлює термінал,
	// а тут — останній рубіж
	defer func() {
		if r := recover(); r != nil {
			errf(i18n.MsgInternalError+"\n", r)
			code = 1
		}
	}()
	if fi, _ := os.Stdout.Stat(); fi == nil || fi.Mode()&os.ModeCharDevice == 0 {
		errln(i18n.MsgNeedTTY)
		return 2
	}
	a, err := newApp(false)
	if err != nil {
		errln(err)
		return 1
	}
	// Сигнали ловить контекст, а не bubbletea: модель має встигнути закрити
	// плеєр і злити журнал до виходу (див. ui.Run).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	eng := a.engine()
	// пульт живе стільки, скільки процес: між серіями сторінка каже «нічого не грає»
	run, err := startRemote(a.store, eng.Live, a.cfg.Remote)
	defer func() { run.Close() }() // замикання: після перезапуску run уже інший
	opts := ui.Options{
		Cfg:          a.cfg,
		DataDir:      a.dataDir,
		Remote:       run.info(err),
		DetectPlayer: detectPlayer,
		// Гарячий перезапуск з екрана налаштувань: нова адреса видна одразу.
		RestartRemote: func(mode string) ui.RemoteInfo {
			run.Close()
			var err error
			run, err = startRemote(a.store, eng.Live, mode)
			return run.info(err)
		},
	}
	if err := ui.Run(ctx, eng, opts); err != nil {
		errln(err)
		return 1
	}
	return 0
}

// doctorReport — стан системи для людини і для --json.
type doctorReport struct {
	Players   []doctorPlayer       `json:"players"`
	DataDir   string               `json:"data_dir"`
	Providers []doctorProviderInfo `json:"providers"`
	Remote    doctorRemote         `json:"remote"`
}

type doctorPlayer struct {
	ID      string `json:"id"`
	Found   bool   `json:"found"`
	Default bool   `json:"default"`
}

type doctorProviderInfo struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Alive   bool       `json:"alive"`
	LastOK  *time.Time `json:"last_ok,omitempty"`
	Message string     `json:"message,omitempty"`
}

func (a *app) cmdDoctor(ctx context.Context, jsonOut bool) int {
	rep := doctorReport{}
	for _, id := range []string{"vlc", "mpv"} {
		rep.Players = append(rep.Players, doctorPlayer{
			ID:      id,
			Found:   player.Found(id),
			Default: id == a.cfg.Player,
		})
	}
	rep.DataDir, _ = store.DataDir()
	rep.Remote = a.doctorRemoteReport()

	health := a.store.LoadHealth()
	info := doctorProviderInfo{ID: a.provider.ID(), Name: a.provider.Name()}
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := a.provider.Search(checkCtx, "аніме", 1); err != nil {
		info.Message = err.Error()
	} else {
		info.Alive = true
		now := time.Now()
		health.Providers[a.provider.ID()] = now
		_ = a.store.SaveHealth(health)
	}
	if t, ok := health.Providers[a.provider.ID()]; ok {
		t := t
		info.LastOK = &t
	}
	rep.Providers = append(rep.Providers, info)

	if jsonOut {
		return printJSON(rep)
	}
	foundPlayer := false
	for _, p := range rep.Players {
		if p.Found {
			foundPlayer = true
			outf(i18n.MsgDoctorPlayerOK+"\n", p.ID)
		} else {
			outf(i18n.MsgDoctorPlayerMissing+"\n", p.ID)
		}
	}
	if !foundPlayer {
		outln(playerInstallHint())
	}
	outf(i18n.MsgDoctorDataDir+"\n", rep.DataDir)
	printDoctorRemote(rep.Remote)
	for _, p := range rep.Providers {
		if p.Alive {
			outf(i18n.MsgDoctorProviderOK+"\n", p.Name)
		} else {
			last := i18n.MsgDoctorNever
			if p.LastOK != nil {
				last = p.LastOK.Format("2006-01-02 15:04")
			}
			outf(i18n.MsgDoctorProviderDown+"\n", p.Name, last)
		}
	}
	return 0
}

func (a *app) cmdImport(path string) int {
	f, err := os.Open(path)
	if err != nil {
		errln(err)
		return 1
	}
	defer func() { _ = f.Close() }()
	if err := a.store.Import(f); err != nil {
		errln(err)
		return 1
	}
	outln(i18n.MsgImported)
	return 0
}

func parseEpisode(s string) (int, bool) {
	ep, err := strconv.Atoi(s)
	if err != nil || ep < 1 {
		errf(i18n.MsgBadEpisode+"\n", s)
		return 0, false
	}
	return ep, true
}

// title-id: "<slug>" або "anitube:<slug>". Слаг приходить з командного рядка,
// тому провайдер його валідує; невалідний аргумент — помилка вжитку (код 2).
func (a *app) refFromID(id string) (provider.TitleRef, bool) {
	slug := strings.TrimPrefix(id, a.provider.ID()+":")
	ref, err := anitube.RefFromSlug(slug)
	if err != nil {
		errf(i18n.MsgBadTitleID+"\n", id)
		return provider.TitleRef{}, false
	}
	return ref, true
}

func titleID(r provider.TitleRef) string { return r.Provider + ":" + r.Slug }

func (a *app) cmdSearch(ctx context.Context, q string, jsonOut bool) int {
	page, err := a.provider.Search(ctx, q, 1)
	if err != nil {
		printCommandError(err)
		return 1
	}
	if jsonOut {
		return printJSON(page.Titles)
	}
	if len(page.Titles) == 0 {
		outln(i18n.MsgNothingFound)
		return 0
	}
	for _, r := range page.Titles {
		outf("%s\t%s\t%s\t%s\n", titleID(r.TitleRef), r.Name, cardYear(r), cardRating(r))
	}
	return 0
}

// Метадані картки — необов'язкові: відсутнє значення лишає колонку порожньою,
// щоб рядок залишався розбірним по табуляціях.
func cardYear(card provider.TitleCard) string {
	if card.Year <= 0 {
		return ""
	}
	return strconv.Itoa(card.Year)
}

func cardRating(card provider.TitleCard) string {
	if card.Rating <= 0 {
		return ""
	}
	return "★" + strconv.FormatFloat(card.Rating, 'f', -1, 64)
}

func (a *app) cmdEpisodes(ctx context.Context, id string, jsonOut bool) int {
	ref, ok := a.refFromID(id)
	if !ok {
		return 2
	}
	eps, offline, err := a.engine().EpisodesCached(ctx, ref)
	if err != nil {
		printCommandError(err)
		return 1
	}
	if offline && !jsonOut {
		errln(i18n.MsgOfflineCache)
	}
	if jsonOut {
		return printJSON(eps)
	}
	for _, e := range eps {
		parts := make([]string, 0, len(e.Releases))
		for _, r := range e.Releases {
			parts = append(parts, fmt.Sprintf("%s (%s)", r.Studio, r.Kind))
		}
		outf("%d\t%s\n", e.Number, strings.Join(parts, ", "))
	}
	return 0
}

func (a *app) cmdResolve(ctx context.Context, id string, ep int, jsonOut bool) int {
	ref, ok := a.refFromID(id)
	if !ok {
		return 2
	}
	cands, err := a.engineWithoutPlayer().ResolveAll(ctx, ref, ep)
	if err != nil || len(cands) == 0 {
		if err == nil {
			err = fmt.Errorf("серія %d: порожній список потоків: %w", ep, errs.ErrNoStream)
		}
		printCommandError(err)
		return 1
	}
	if jsonOut {
		return printJSON(cands)
	}
	for _, c := range cands {
		outf("%s\t%s\t%s\t%s\n", c.Studio, c.Kind, c.Host, c.Stream.URL)
	}
	return 0
}

func printCommandError(err error) { errln(i18n.ErrorText(err)) }

func (a *app) engine() *playback.Engine {
	eng := a.engineWithoutPlayer()
	eng.Player, eng.PlayerFallback, _ = detectPlayer(a.cfg.Player)
	eng.Autoplay = a.cfg.Autoplay == "always"
	eng.JournalInterval = journalInterval
	// Live є завжди: без пульта воно нічого не коштує, а екран налаштувань
	// має куди дивитися, коли пульт вмикають із «вимкнено».
	eng.Live = newLive()
	return eng
}

// engineWithoutPlayer потрібен для --dry-run: команда має будуватися без
// пошуку встановлених програм і працювати навіть на системі без плеєрів.
func (a *app) engineWithoutPlayer() *playback.Engine {
	return &playback.Engine{
		Provider:   a.provider,
		Extractors: a.extractors,
		Store:      a.store,
		Lib:        a.lib,
		Prefs: library.Prefs{
			FavoriteStudio: a.cfg.FavoriteStudio,
			PreferKind:     provider.Kind(a.cfg.PreferKind),
		},
	}
}

func (a *app) cmdPlay(_ context.Context, id string, ep int, dryRun bool) int {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ref, ok := a.refFromID(id)
	if !ok {
		return 2
	}
	var eng *playback.Engine
	if dryRun {
		eng = a.engineWithoutPlayer()
	} else {
		eng = a.engine()
		// Помилка не фатальна: без пульта play працює як досі, тому спершу
		// звіт, а run (порожній або живий) закривається однаково.
		run, err := startRemote(a.store, eng.Live, a.cfg.Remote)
		run.reportRemoteErr(err)
		defer run.Close()
		run.announce()
	}

	for {
		outf(i18n.MsgResolving+"\n", ep)
		resolveCtx, cancel := context.WithTimeout(sigCtx, 60*time.Second)
		res, err := eng.Resolve(resolveCtx, ref, ep, func(playback.Event) { outln(i18n.MsgTryingNext) })
		cancel()
		if err != nil {
			errln(i18n.ErrorText(err))
			return 1
		}
		if res.PinFallback {
			if title := eng.Lib.TitleByRef(ref); title != nil {
				if entry := eng.Lib.EntryLookup(title.ID); entry != nil {
					errf(i18n.TuiStudioFallback+"\n", entry.StudioPin, res.Source.Studio)
				}
			}
		}
		if res.StartSec > 0 {
			outf(i18n.MsgResume+"\n", int(res.StartSec)/60, int(res.StartSec)%60)
		}
		outf(i18n.MsgPickedSource+"\n", res.Source.Studio, res.Source.Kind, res.HostID)

		if dryRun {
			cmd := player.ByID(a.cfg.Player).Command(res.Stream.URL, res.MediaTitle, res.Stream.Headers, res.StartSec)
			outln(shellQuote(cmd.Args))
			return 0
		}

		if eng.PlayerFallback {
			outf(i18n.MsgPlayerFallback+"\n", eng.Player.ID())
		}
		outln(i18n.MsgLaunchingPlayer)
		// Пульт має бачити список серій і в headless: цикл послідовний, тож
		// читання бібліотеки тут не порушує правила 10.
		publishPlaylist(sigCtx, eng, ref, ep)
		result, err := eng.Play(sigCtx, res)
		if errors.Is(err, errs.ErrNoPlayer) {
			errln(i18n.MsgNoPlayer)
			errln(playerInstallHint())
			return 1
		}
		if err != nil {
			errf(i18n.MsgPlayerFailed+"\n", err)
			return 1
		}
		if result.PinnedStudio != "" {
			outf(i18n.MsgStudioPinned+"\n", result.PinnedStudio)
		}
		if result.Completed {
			outf(i18n.MsgEpisodeDone+"\n", ep)
		} else if result.PositionSec > 0 {
			outf(i18n.MsgProgressSaved+"\n", int(result.PositionSec)/60, int(result.PositionSec)%60)
		}
		if result.Reason == player.EndError {
			errf(i18n.MsgPlayerFailed+"\n", result.Reason)
			return 1
		}
		// намір пульта сильніший за налаштування: «наступна» йде далі навіть
		// без автоплею, «стоп» уриває ланцюжок навіть з ним
		switch {
		case result.Intent == playback.IntentStop:
			return 0
		case result.Intent == playback.IntentPlay && result.Requested.Episode > 0:
			// адресний запит із пульта: серія названа явно, список не потрібен
			ep = result.Requested.Episode
			continue
		case result.Intent == playback.IntentNext:
		case result.StopAfter:
			// «досидіти й зупинитись» уриває ланцюжок так само, як «стоп»,
			// але вже після того, як серія догралася
			return 0
		case result.Reason == player.EndEOF && eng.Autoplay:
		default:
			return 0
		}

		episodesCtx, cancel := context.WithTimeout(sigCtx, 60*time.Second)
		episodes, _, err := eng.EpisodesCached(episodesCtx, ref)
		cancel()
		if err != nil {
			break
		}
		next, ok := playback.NextEpisodeNumber(episodes, ep)
		if !ok {
			break
		}
		ep = next
	}
	return 0
}

func playerInstallHint() string {
	if runtime.GOOS == "darwin" {
		return i18n.MsgInstallHintMac
	}
	return i18n.MsgInstallHintLinux
}

func printJSON(v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		errln(err)
		return 1
	}
	return 0
}
