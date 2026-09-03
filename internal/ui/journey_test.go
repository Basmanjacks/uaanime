package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// Наскрізний TUI-сценарій: одна клавіатурна сесія від домівки до перегляду
// трьох серій і назад. Провайдер — стаб, плеєр — playertest; решта справжня.

const journeyEpisodes = 3

var journeyRef = provider.TitleRef{Provider: "test", Slug: "4465-frieren", Name: "Фрірен", URL: "https://test.invalid/4465-frieren"}

func journeySources(ep int) []provider.Source {
	n := fmt.Sprint(ep)
	return []provider.Source{
		{Studio: "FANVOXUA", Kind: provider.KindDub, Embed: "https://host-a.invalid/e/fanvox-" + n, Referer: journeyRef.URL, Episode: ep},
		{Studio: "Amanogawa", Kind: provider.KindDub, Embed: "https://host-b.invalid/e/amanogawa-" + n, Referer: journeyRef.URL, Episode: ep},
		{Studio: "Glass Moon", Kind: provider.KindSub, Embed: "https://host-a.invalid/e/glass-" + n, Referer: journeyRef.URL, Episode: ep},
	}
}

func journeyProvider() providertest.Stub {
	card := provider.TitleCard{TitleRef: journeyRef, Year: 2023, EpAired: journeyEpisodes, HasDub: true}
	eps := make([]provider.Episode, journeyEpisodes)
	for i := range eps {
		eps[i] = provider.Episode{Number: i + 1, Releases: []provider.Release{
			{Studio: "FANVOXUA", Kind: provider.KindDub},
			{Studio: "Amanogawa", Kind: provider.KindDub},
			{Studio: "Glass Moon", Kind: provider.KindSub},
		}}
	}
	return providertest.Stub{
		IDValue:   "test",
		NameValue: "Test",
		CapsValue: provider.Caps{Search: true, Catalog: true},
		SearchFn: func(_ context.Context, q string, _ int) (provider.Page, error) {
			if strings.Contains(strings.ToLower(q), "фрірен") {
				return provider.Page{Titles: []provider.TitleCard{card}}, nil
			}
			return provider.Page{}, nil
		},
		CatalogFn: func(_ context.Context, kind provider.CatalogKind) ([]provider.TitleCard, error) {
			return testCards(string(kind), 3), nil
		},
		EpisodesFn: func(context.Context, provider.TitleRef) ([]provider.Episode, error) { return eps, nil },
		SourcesFn: func(_ context.Context, _ provider.TitleRef, ep int) ([]provider.Source, error) {
			return journeySources(ep), nil
		},
	}
}

// journeyModel — модель поверх справжнього сховища у t.TempDir() і рушія з
// фейковим плеєром; вікно 80×24 — мінімальний розмір із брифу.
func journeyModel(t *testing.T, sessions ...*playertest.Session) (Model, *playertest.Player, *store.Store) {
	t.Helper()
	return journeyModelIn(t, t.TempDir(), sessions...)
}

// journeyModelIn — те саме, але в заданому каталозі: сценарій, який перевіряє
// файли на диску, мусить знати, куди дивитися.
func journeyModelIn(t *testing.T, dir string, sessions ...*playertest.Session) (Model, *playertest.Player, *store.Store) {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatalf("LoadLibrary: %v", err)
	}
	fp := &playertest.Player{Sessions: sessions}
	eng := &playback.Engine{
		Provider:        journeyProvider(),
		Extractors:      []extractor.Extractor{stubExtractor{}},
		Store:           st,
		Lib:             lib,
		Player:          fp,
		Autoplay:        true,
		JournalInterval: time.Millisecond,
	}
	m := New(eng, Options{})
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, fp, st
}

// trace — журнал станів між повідомленнями: по ньому тест доводить, що
// питання про студію було рівно одне і що перегляд справді стартував.
type trace struct {
	screens []screen
	quit    bool
}

func (tr *trace) count(s screen) int {
	n := 0
	for i, sc := range tr.screens {
		// рахуємо входи на екран, а не кадри
		if sc == s && (i == 0 || tr.screens[i-1] != s) {
			n++
		}
	}
	return n
}

// pump виконує команду так, як це робив би bubbletea: повідомлення в Update,
// повернуту команду — знову в pump, Batch — по черзі. Синхронно, без горутин:
// фонові команди тут детерміновані (стаб і фейк плеєра).
func pump(t *testing.T, m Model, cmd tea.Cmd, tr *trace) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	return deliver(t, m, cmd(), tr)
}

// deliver застосовує одне повідомлення. Batch: спершу виконуються ВСІ команди
// пакета і їхні відповіді застосовуються по черзі, і лише потім — команди,
// породжені цими відповідями. Так швидка відповідь (кеш серій) не чекає на
// довгу (сесія плеєра), як і в реальному bubbletea.
func deliver(t *testing.T, m Model, msg tea.Msg, tr *trace) Model {
	t.Helper()
	switch v := msg.(type) {
	case nil:
		return m
	case tea.BatchMsg:
		msgs := make([]tea.Msg, 0, len(v))
		for _, c := range v {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
		var nexts []tea.Cmd
		for _, inner := range msgs {
			if _, nested := inner.(tea.BatchMsg); nested {
				m = deliver(t, m, inner, tr)
				continue
			}
			var next tea.Cmd
			m, next = step(t, m, inner, tr)
			nexts = append(nexts, next)
		}
		for _, next := range nexts {
			m = pump(t, m, next, tr)
		}
		return m
	case tea.QuitMsg:
		tr.quit = true
		return m
	}
	m, next := step(t, m, msg, tr)
	return pump(t, m, next, tr)
}

func step(t *testing.T, m Model, msg tea.Msg, tr *trace) (Model, tea.Cmd) {
	t.Helper()
	if _, ok := msg.(tea.QuitMsg); ok {
		tr.quit = true
		return m, nil
	}
	m, next := updateTestModel(t, m, msg)
	tr.screens = append(tr.screens, m.screen)
	return m, next
}

func press(t *testing.T, m Model, tr *trace, code rune, text string) Model {
	t.Helper()
	m, cmd := pressTestKey(t, m, code, text)
	tr.screens = append(tr.screens, m.screen)
	return pump(t, m, cmd, tr)
}

func mustScreen(t *testing.T, m Model, want screen) {
	t.Helper()
	if m.screen != want {
		t.Fatalf("екран = %d, want %d (status %q, err %q)", m.screen, want, m.status, m.errText)
	}
}

func TestJourneySearchToPlaybackAndBack(t *testing.T) {
	m, fp, st := journeyModel(t,
		playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}),
		playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}),
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	tr := &trace{}
	mustScreen(t, m, screenHome)
	if !strings.Contains(ansi.Strip(m.View().Content), i18n.TuiEmptyLibrary) {
		t.Fatal("порожня бібліотека має підказати почати з пошуку")
	}

	// «/» → пошук → Enter → результати
	m = press(t, m, tr, '/', "/")
	mustScreen(t, m, screenSearch)
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenSearch)
	if len(m.cards) != 1 || m.cards[0].Slug != journeyRef.Slug {
		t.Fatalf("результати пошуку = %+v", m.cards)
	}

	// Enter на картці → серії
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	if got := len(m.list.Items()); got != journeyEpisodes {
		t.Fatalf("рядків серій = %d, want %d", got, journeyEpisodes)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), fmt.Sprintf(i18n.TuiStudioPinned, i18n.TuiStudioAuto)) {
		t.Error("до першого перегляду заголовок має показувати «озвучка: авто»")
	}

	// Enter на серії 1 → дві студії дубляжу без піна → питання рівно один раз
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenStudio)
	if got := len(m.list.Items()); got != 2 {
		t.Fatalf("варіантів студій = %d, want 2 (саби не пропонуються, коли є дубляж)", got)
	}
	chosen := m.list.SelectedItem().(item).title

	// Enter → пін → грає ep1 → EOF → автоплей ep2 → EOF → ep3 → Quit@30
	m = press(t, m, tr, tea.KeyEnter, "")
	// між серіями автоплей лишається на екрані відтворення — це один вхід
	if tr.count(screenPlaying) != 1 {
		t.Errorf("входів на екран відтворення = %d, want 1 (сліди: %v)", tr.count(screenPlaying), tr.screens)
	}
	if tr.count(screenStudio) != 1 {
		t.Errorf("питання про студію = %d, want рівно 1", tr.count(screenStudio))
	}
	mustScreen(t, m, screenEpisodes)
	if m.status != fmt.Sprintf(i18n.MsgProgressSaved, 0, 30) {
		t.Errorf("статус = %q, want прогрес 00:30", m.status)
	}
	starts := fp.Starts()
	if len(starts) != 3 {
		t.Fatalf("запусків плеєра = %d, want 3", len(starts))
	}
	for i, s := range starts {
		if !strings.HasSuffix(s.MediaTitle, fmt.Sprintf(" · %d", i+1)) || s.StartSec != 0 {
			t.Errorf("Start[%d] = %+v", i, s)
		}
	}

	// список серій показує стани; заголовок — закріплену студію
	content := ansi.Strip(m.View().Content)
	if strings.Count(content, i18n.TuiEpDone) != 2 {
		t.Errorf("переглянутих серій у кадрі = %d, want 2:\n%s", strings.Count(content, i18n.TuiEpDone), content)
	}
	if !strings.Contains(content, fmt.Sprintf(i18n.TuiEpAt, 0, 30)) {
		t.Errorf("серія 3 має показувати «зупинився на 00:30»:\n%s", content)
	}
	if !strings.Contains(content, fmt.Sprintf(i18n.TuiStudioPinned, chosen)) {
		t.Errorf("заголовок має показувати пін %q:\n%s", chosen, content)
	}

	// на диску: пін, стан watching, прогрес трьох серій
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	title := lib.TitleByRef(journeyRef)
	if title == nil {
		t.Fatal("тайтл не збережено")
	}
	if e := lib.EntryLookup(title.ID); e == nil || e.StudioPin != chosen || e.State != library.StateWatching {
		t.Errorf("entry = %+v, want пін %q і watching", e, chosen)
	}
	for ep := 1; ep <= 2; ep++ {
		if p := lib.ProgressFor(title.ID, ep); p == nil || !p.Completed {
			t.Errorf("серія %d має бути завершена: %+v", ep, p)
		}
	}
	if p := lib.ProgressFor(title.ID, 3); p == nil || p.Completed || p.PositionSec != 30 {
		t.Errorf("серія 3 = %+v, want 30 с", p)
	}

	// Esc → результати пошуку → Esc → домівка з «Продовжити» і закладкою
	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenSearch)
	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenHome)
	rows := sectionRows(t, m, i18n.TuiBlockContinue)
	if len(rows) != 1 || rows[0].title != fmt.Sprintf(i18n.TuiContinuePfx, journeyRef.Name, 3) {
		t.Errorf("«Продовжити» = %+v, want серію 3", rows)
	}
	if libraryRow(t, m, journeyRef.Name).meta != i18n.TuiStateWatching {
		t.Error("тайтл має бути в закладках як «переглядаєш»")
	}

	// повторний перегляд серії 3 не питає студію знову і стартує з 30 с
	fp.Sessions = append(fp.Sessions, playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440}))
	selectTestItem(t, &m, func(it item) bool { _, ok := it.payload.(payloadResume); return ok })
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	if tr.count(screenStudio) != 1 {
		t.Errorf("повторне питання про студію — баг: %d", tr.count(screenStudio))
	}
	if starts := fp.Starts(); len(starts) != 4 || starts[3].StartSec != 30 {
		t.Errorf("resume: Start = %+v, want StartSec 30", starts[len(starts)-1])
	}

	// q з домівки — вихід
	m = press(t, m, tr, tea.KeyEsc, "")
	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenHome)
	press(t, m, tr, 'q', "q")
	if !tr.quit {
		t.Error("q на домівці має завершити застосунок")
	}
}

// Ctrl+C під час гри: вихід лише після того, як Finish злив журнал.
func TestJourneyInterruptDuringPlaybackFlushesProgress(t *testing.T) {
	sess := playertest.NewSession(player.EndQuit, []float64{215}, []float64{1440})
	sess.Hold = true
	m, _, st := journeyModel(t, sess)
	tr := &trace{}

	if err := m.eng.PinStudio(journeyRef, "FANVOXUA"); err != nil {
		t.Fatal(err)
	}
	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)

	// Enter на серії: resolve синхронно, а команду відтворення тримаємо в руках
	m, cmd := pressTestKey(t, m, tea.KeyEnter, "")
	msg := cmd()
	resolved, ok := msg.(resolvedMsg)
	if !ok {
		t.Fatalf("очікував resolvedMsg, отримав %T", msg)
	}
	m, playCmd := updateTestModel(t, m, resolved)
	mustScreen(t, m, screenPlaying)
	// Екран «Грає» керований: унизу підказка з клавішами, а не статус.
	if plain := ansi.Strip(m.View().Content); !strings.Contains(plain, i18n.TuiHintPlaying) {
		t.Errorf("підказки клавіш немає (статус %q):\n%s", m.status, plain)
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- playCmd() }()
	<-sess.Sampled // журнал уже містить позицію

	// Ctrl+C: перша фаза — лише скасування сесії, застосунок ще живий
	m, cmd = updateTestModel(t, m, tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("Ctrl+C під час гри не має завершувати застосунок одразу")
	}
	if !m.quitting {
		t.Fatal("Ctrl+C має позначити відкладений вихід")
	}

	var playDone tea.Msg
	select {
	case playDone = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("скасування не зупинило сесію")
	}
	m, cmd = updateTestModel(t, m, playDone)
	if cmd == nil {
		t.Fatal("після Finish має бути tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("друга фаза виходу має повернути tea.Quit")
	}

	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	title := lib.TitleByRef(journeyRef)
	if p := lib.ProgressFor(title.ID, 1); p == nil || p.PositionSec != 215 {
		t.Errorf("прогрес після Ctrl+C = %+v, want 215", p)
	}
}

// Кадри: кожен екран на кожному розмірі вміщується у вікно і не панікує.
func TestJourneyFramesFitWindow(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {120, 40}, {43, 18}, {40, 12}, {20, 5}}
	for _, size := range sizes {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m, _, _ := journeyModel(t,
				playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}),
				playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
			)
			// рядок пульта — частина кадру «Грає», перевіряється на всіх розмірах
			m.remote.URL = testRemoteURL
			tr := &trace{}
			// каталог із Init — домівка з блоками
			m = pump(t, m, m.Init(), tr)
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: size.w, Height: size.h})

			// 20×5 — лише «не панікує»: хром (заголовок, поле, підказка) сам
			// більший за таке вікно, і бриф його не обіцяє.
			strict := size.w >= 40 && size.h >= 12
			check := func(name string) {
				t.Helper()
				content := m.View().Content
				if !strict {
					return
				}
				lines := strings.Split(content, "\n")
				if h := len(lines); h > size.h {
					t.Errorf("%s: %d рядків у вікні %d", name, h, size.h)
				}
				for i, line := range lines {
					if w := lipgloss.Width(line); w > size.w {
						t.Errorf("%s: рядок %d ширший за вікно (%d > %d): %q", name, i, w, size.w, ansi.Strip(line))
					}
				}
			}

			check("home")
			plain := ansi.Strip(m.View().Content)
			wantBanner := size.w >= brandWidth()+4 && size.h >= brandChromeHeight+brandMinListRows
			if hasBanner := strings.Contains(plain, strings.TrimSpace(m.brandBanner()[2])); hasBanner != wantBanner {
				t.Errorf("home: банер=%v, want %v", hasBanner, wantBanner)
			}
			if !wantBanner && !strings.Contains(plain, i18n.TuiAppTitle) {
				t.Error("home: без банера має бути однорядковий fallback із назвою")
			}

			m = press(t, m, tr, '/', "/")
			check("search")
			m.input.SetValue("фрірен")
			m = press(t, m, tr, tea.KeyEnter, "")
			check("search-results")
			m = press(t, m, tr, tea.KeyEnter, "")
			mustScreen(t, m, screenEpisodes)
			check("episodes")
			m = press(t, m, tr, tea.KeyEnter, "")
			mustScreen(t, m, screenStudio)
			check("studio")
			// перегляд: після EOF+Quit знову на серіях зі станами
			m = press(t, m, tr, tea.KeyEnter, "")
			mustScreen(t, m, screenEpisodes)
			check("episodes-after-play")
			m = press(t, m, tr, tea.KeyEsc, "")
			m = press(t, m, tr, tea.KeyEsc, "")
			mustScreen(t, m, screenHome)
			check("home-with-library")
		})
	}
}

// Ручна позначка «переглянуто» на екрані серій: без плеєра, з нуля — тайтл до
// цього не був ані в закладках, ані в історії.
func TestJourneyMarkWatchedFromEpisodes(t *testing.T) {
	m, fp, st := journeyModel(t)
	tr := &trace{}

	// «/» → пошук → Enter → результати → Enter → серії
	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)

	selectTestItem(t, &m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })
	index := m.list.Index()
	m = press(t, m, tr, 'x', "x")
	mustScreen(t, m, screenEpisodes)
	if len(fp.Starts()) != 0 {
		t.Fatalf("позначка не має запускати плеєр: %+v", fp.Starts())
	}
	if m.list.Index() != index {
		t.Errorf("курсор поїхав: %d, want %d", m.list.Index(), index)
	}
	if got := m.list.SelectedItem().(item); got.badge != i18n.TuiEpDone || got.icon != m.ic.Done {
		t.Errorf("рядок серії 2 = %+v, want позначку «переглянуто»", got)
	}
	if m.status != fmt.Sprintf(i18n.TuiEpMarked, 2) {
		t.Errorf("статус = %q", m.status)
	}

	// на диску: тайтл створено з нуля, серія 2 завершена
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	title := lib.TitleByRef(journeyRef)
	if title == nil {
		t.Fatal("тайтл не створено")
	}
	if p := lib.ProgressFor(title.ID, 2); p == nil || !p.Completed {
		t.Fatalf("прогрес серії 2 = %+v, want завершено", p)
	}
	if e := lib.EntryLookup(title.ID); e == nil || e.LastEpisode != 2 {
		t.Fatalf("entry = %+v, want LastEpisode 2", e)
	}

	// домівка пропонує наступну серію
	m = press(t, m, tr, tea.KeyEsc, "")
	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenHome)
	rows := sectionRows(t, m, i18n.TuiBlockContinue)
	if len(rows) != 1 || rows[0].title != fmt.Sprintf(i18n.TuiContinuePfx, journeyRef.Name, 3) {
		t.Fatalf("«Продовжити» = %+v, want серію 3", rows)
	}

	// повернення на серії через закладку і зняття позначки
	selectTestItem(t, &m, func(it item) bool { _, ok := it.payload.(payloadTitle); return ok })
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	selectTestItem(t, &m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })
	m = press(t, m, tr, 'x', "x")
	if got := m.list.SelectedItem().(item); got.badge != "" || got.icon != m.ic.Pending {
		t.Errorf("рядок серії 2 після зняття = %+v", got)
	}
	if m.status != fmt.Sprintf(i18n.TuiEpUnmarked, 2) {
		t.Errorf("статус = %q", m.status)
	}
	if lib, err = st.LoadLibrary(); err != nil {
		t.Fatal(err)
	}
	if p := lib.ProgressFor(title.ID, 2); p != nil {
		t.Fatalf("прогрес серії 2 не зник: %+v", p)
	}
	if e := lib.EntryLookup(title.ID); e == nil || e.LastEpisode != 0 {
		t.Fatalf("entry = %+v, want LastEpisode 0", e)
	}
}
