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

type app struct {
	provider   provider.Provider
	extractors []extractor.Extractor
	store      *store.Store
	lib        *library.Library
	cfg        *store.Config
}

func newApp() (*app, error) {
	var rt http.RoundTripper
	if os.Getenv("UAANIME_FIXTURES") == "1" {
		rt = httpx.MultiTransport{
			anitube.FixtureTransport("internal/provider/anitube/testdata"),
			ashdi.FixtureTransport("internal/extractor/ashdi/testdata"),
		}
	}
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
	if _, err := st.RecoverJournal(lib); err != nil {
		return nil, err
	}
	return &app{
		provider:   anitube.New(client),
		extractors: []extractor.Extractor{ashdi.New(client)},
		store:      st,
		lib:        lib,
		cfg:        cfg,
	}, nil
}

func run(args []string) int {
	var positional []string
	jsonOut, dryRun := false, false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--dry-run":
			dryRun = true
		default:
			positional = append(positional, a)
		}
	}
	// без аргументів — TUI (потрібен термінал)
	if len(positional) == 0 {
		return runTUI()
	}

	// doctor/export — одноаргументні; import/search/episodes — два; resolve/play — три
	minArgs := map[string]int{"doctor": 1, "export": 1, "import": 2, "search": 2, "episodes": 2, "resolve": 3, "play": 3}
	if need, ok := minArgs[positional[0]]; !ok || len(positional) < need {
		fmt.Fprintln(os.Stderr, i18n.MsgUsage)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	a, err := newApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch positional[0] {
	case "doctor":
		return a.cmdDoctor(ctx, jsonOut)
	case "export":
		if err := a.store.Export(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	case "import":
		return a.cmdImport(positional[1])
	case "search":
		return a.cmdSearch(ctx, strings.Join(positional[1:], " "), jsonOut)
	case "episodes":
		return a.cmdEpisodes(ctx, positional[1], jsonOut)
	case "resolve":
		if len(positional) != 3 {
			break
		}
		ep, ok := parseEpisode(positional[2])
		if !ok {
			return 2
		}
		return a.cmdResolve(ctx, positional[1], ep, jsonOut)
	case "play":
		if len(positional) != 3 {
			break
		}
		ep, ok := parseEpisode(positional[2])
		if !ok {
			return 2
		}
		return a.cmdPlay(ctx, positional[1], ep, dryRun)
	}
	fmt.Fprintln(os.Stderr, i18n.MsgUsage)
	return 2
}

func runTUI() (code int) {
	// жоден panic не долітає до користувача: bubbletea відновлює термінал,
	// а тут — останній рубіж
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "внутрішня помилка: %v\n", r)
			code = 1
		}
	}()
	if fi, _ := os.Stdout.Stat(); fi == nil || fi.Mode()&os.ModeCharDevice == 0 {
		fmt.Fprintln(os.Stderr, i18n.MsgNeedTTY)
		return 2
	}
	a, err := newApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := ui.Run(a.engine()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// doctorReport — стан системи для людини і для --json.
type doctorReport struct {
	Players   []doctorPlayer       `json:"players"`
	DataDir   string               `json:"data_dir"`
	Providers []doctorProviderInfo `json:"providers"`
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
			fmt.Printf(i18n.MsgDoctorPlayerOK+"\n", p.ID)
		} else {
			fmt.Printf(i18n.MsgDoctorPlayerMissing+"\n", p.ID)
		}
	}
	if !foundPlayer {
		fmt.Println(playerInstallHint())
	}
	fmt.Printf(i18n.MsgDoctorDataDir+"\n", rep.DataDir)
	for _, p := range rep.Providers {
		if p.Alive {
			fmt.Printf(i18n.MsgDoctorProviderOK+"\n", p.Name)
		} else {
			last := i18n.MsgDoctorNever
			if p.LastOK != nil {
				last = p.LastOK.Format("2006-01-02 15:04")
			}
			fmt.Printf(i18n.MsgDoctorProviderDown+"\n", p.Name, last)
		}
	}
	return 0
}

func (a *app) cmdImport(path string) int {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = f.Close() }()
	if err := a.store.Import(f); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(i18n.MsgImported)
	return 0
}

func parseEpisode(s string) (int, bool) {
	ep, err := strconv.Atoi(s)
	if err != nil || ep < 1 {
		fmt.Fprintf(os.Stderr, i18n.MsgBadEpisode+"\n", s)
		return 0, false
	}
	return ep, true
}

// title-id: "<slug>" або "anitube:<slug>".
func (a *app) refFromID(id string) provider.TitleRef {
	slug := strings.TrimPrefix(id, a.provider.ID()+":")
	return anitube.RefFromSlug(slug)
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
		fmt.Println(i18n.MsgNothingFound)
		return 0
	}
	for _, r := range page.Titles {
		fmt.Printf("%s\t%s\t%s\t%s\n", titleID(r.TitleRef), r.Name, cardYear(r), cardRating(r))
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
	eps, offline, err := a.engine().EpisodesCached(ctx, a.refFromID(id))
	if err != nil {
		printCommandError(err)
		return 1
	}
	if offline && !jsonOut {
		fmt.Fprintln(os.Stderr, i18n.MsgOfflineCache)
	}
	if jsonOut {
		return printJSON(eps)
	}
	for _, e := range eps {
		parts := make([]string, 0, len(e.Releases))
		for _, r := range e.Releases {
			parts = append(parts, fmt.Sprintf("%s (%s)", r.Studio, r.Kind))
		}
		fmt.Printf("%d\t%s\n", e.Number, strings.Join(parts, ", "))
	}
	return 0
}

// candidate — один придатний до відтворення потік для resolve --json.
type candidate struct {
	Studio string           `json:"studio"`
	Kind   provider.Kind    `json:"kind"`
	Host   string           `json:"host"`
	Stream extractor.Stream `json:"stream"`
}

func (a *app) candidates(ctx context.Context, ref provider.TitleRef, ep int) ([]candidate, error) {
	sources, err := a.provider.Sources(ctx, ref, ep)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("серія %d: провайдер не повернув джерел: %w", ep, errs.ErrNoStream)
	}
	var out []candidate
	var failures []error
	for _, src := range sources {
		ex, ok := extractor.Find(a.extractors, src.Embed)
		if !ok {
			failures = append(failures, fmt.Errorf("embed %s: немає підтримуваного екстрактора: %w", src.Embed, errs.ErrNoStream))
			continue
		}
		streams, err := ex.Extract(ctx, src.Embed, src.Referer)
		if err != nil {
			failures = append(failures, err)
			continue // одне мертве джерело не має ховати решту
		}
		if len(streams) == 0 {
			failures = append(failures, fmt.Errorf("екстрактор %s не повернув потоку: %w", ex.ID(), errs.ErrNoStream))
			continue
		}
		for _, st := range streams {
			out = append(out, candidate{Studio: src.Studio, Kind: src.Kind, Host: ex.ID(), Stream: st})
		}
	}
	if len(out) == 0 {
		return nil, aggregateCandidateFailures(ep, failures)
	}
	return out, nil
}

func aggregateCandidateFailures(ep int, failures []error) error {
	offline, noStream := 0, 0
	for _, err := range failures {
		switch {
		case errors.Is(err, errs.ErrOffline) || errs.Offline(err):
			offline++
		case errors.Is(err, errs.ErrNoStream):
			noStream++
		}
	}
	class := errs.ErrProvider
	if len(failures) > 0 && offline == len(failures) {
		class = errs.ErrOffline
	} else if len(failures) > 0 && noStream == len(failures) {
		class = errs.ErrNoStream
	}
	return fmt.Errorf("серія %d: жодне джерело не дало потоку: %w: %w", ep, class, errors.Join(failures...))
}

func (a *app) cmdResolve(ctx context.Context, id string, ep int, jsonOut bool) int {
	cands, err := a.candidates(ctx, a.refFromID(id), ep)
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
		fmt.Printf("%s\t%s\t%s\t%s\n", c.Studio, c.Kind, c.Host, c.Stream.URL)
	}
	return 0
}

func commandErrText(err error) string {
	switch {
	case errors.Is(err, errs.ErrOffline):
		return i18n.MsgOffline
	case errors.Is(err, errs.ErrNoStream):
		return i18n.MsgNoPlayableHost
	default:
		return fmt.Sprintf(i18n.MsgProviderFailed, err)
	}
}

func printCommandError(err error) { fmt.Fprintln(os.Stderr, commandErrText(err)) }

func (a *app) engine() *playback.Engine {
	eng := a.engineWithoutPlayer()
	eng.Player, eng.PlayerFallback, _ = player.Detect(a.cfg.Player)
	eng.Autoplay = a.cfg.Autoplay == "always"
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

	ref := a.refFromID(id)
	var eng *playback.Engine
	if dryRun {
		eng = a.engineWithoutPlayer()
	} else {
		eng = a.engine()
	}

	for {
		fmt.Printf(i18n.MsgResolving+"\n", ep)
		resolveCtx, cancel := context.WithTimeout(sigCtx, 60*time.Second)
		res, err := eng.Resolve(resolveCtx, ref, ep, func(playback.Event) { fmt.Println(i18n.MsgTryingNext) })
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.MsgProviderFailed+"\n", err)
			return 1
		}
		if res.StartSec > 0 {
			fmt.Printf(i18n.MsgResume+"\n", int(res.StartSec)/60, int(res.StartSec)%60)
		}
		fmt.Printf(i18n.MsgPickedSource+"\n", res.Source.Studio, res.Source.Kind, res.HostID)

		if dryRun {
			var backend player.Player = player.VLC{}
			if a.cfg.Player == "mpv" {
				backend = player.MPV{}
			}
			cmd := backend.Command(res.Stream.URL, res.MediaTitle, res.Stream.Headers, res.StartSec)
			fmt.Println(strings.Join(cmd.Args, " "))
			return 0
		}

		if eng.PlayerFallback {
			fmt.Printf(i18n.MsgPlayerFallback+"\n", eng.Player.ID())
		}
		fmt.Println(i18n.MsgLaunchingPlayer)
		result, err := eng.Play(sigCtx, res)
		if errors.Is(err, errs.ErrNoPlayer) {
			fmt.Fprintln(os.Stderr, i18n.MsgNoPlayer)
			fmt.Fprintln(os.Stderr, playerInstallHint())
			return 1
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.MsgPlayerFailed+"\n", err)
			return 1
		}
		if result.PinnedStudio != "" {
			fmt.Printf(i18n.MsgStudioPinned+"\n", result.PinnedStudio)
		}
		if result.Completed {
			fmt.Printf(i18n.MsgEpisodeDone+"\n", ep)
		} else if result.PositionSec > 0 {
			fmt.Printf(i18n.MsgProgressSaved+"\n", int(result.PositionSec)/60, int(result.PositionSec)%60)
		}
		if result.Reason == player.EndError {
			fmt.Fprintf(os.Stderr, i18n.MsgPlayerFailed+"\n", result.Reason)
			return 1
		}
		if result.Reason != player.EndEOF || !eng.Autoplay {
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
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
