package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

const testRemoteURL = "http://vitaliis-macbook-pro.local:51234/r/0123456789abcdef0123456789abcdef"

// Пульт просить наступну серію, поки автоплей вимкнено: намір користувача
// сильніший за налаштування, і ланцюжок іде тим самим шляхом playDoneMsg.
func TestJourneyRemoteNextPlaysNextEpisodeWithoutAutoplay(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	m, fp, _ := journeyModel(t,
		held,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	m.eng.Autoplay = false
	live := &playback.Live{}
	m.eng.Live = live
	tr := &trace{}

	// пульт живе на власній горутині, як net/http; pump тим часом блокується
	// всередині Run
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			snap, err := live.Snapshot()
			if err != nil {
				done <- err
				return
			}
			if snap.Playing {
				done <- live.Next()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		done <- fmt.Errorf("пульт не дочекався сесії")
	}()

	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenStudio)
	m = press(t, m, tr, tea.KeyEnter, "")

	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	mustScreen(t, m, screenEpisodes)
	starts := fp.Starts()
	if len(starts) != 2 {
		t.Fatalf("запусків плеєра = %d, want 2 (сліди: %v)", len(starts), tr.screens)
	}
	if !strings.HasSuffix(starts[1].MediaTitle, " · 2") {
		t.Errorf("Start[1] = %+v, want серія 2", starts[1])
	}
	if tr.count(screenPlaying) != 1 {
		t.Errorf("входів на екран відтворення = %d, want 1", tr.count(screenPlaying))
	}
	if m.status != fmt.Sprintf(i18n.MsgProgressSaved, 0, 30) {
		t.Errorf("статус = %q, want прогрес 00:30 серії 2", m.status)
	}
}

func TestPlayingFrameShowsRemoteURL(t *testing.T) {
	for _, tt := range []struct {
		w, h     int
		wantURL  bool
		wantHint bool
	}{
		{80, 24, false, false},
		{120, 40, true, false},
		{40, 12, false, true},
	} {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			m := newTestModel(t)
			m.remote.URL = testRemoteURL
			m.screen = screenPlaying
			m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: tt.w, Height: tt.h})

			content := m.View().Content
			plain := ansi.Strip(content)
			for i, line := range strings.Split(content, "\n") {
				if w := lipgloss.Width(line); w > tt.w {
					t.Errorf("рядок %d ширший за вікно (%d > %d): %q", i, w, tt.w, ansi.Strip(line))
				}
			}
			// 80 колонок: повний «Пульт: <url>» не влазить, голий URL — влазить
			if got := strings.Contains(plain, testRemoteURL); got == tt.wantHint {
				t.Errorf("кадр %dx%d: адреса пульта = %v (вузьке вікно має ховати її цілком):\n%s", tt.w, tt.h, got, plain)
			}
			if got := strings.Contains(plain, fmt.Sprintf(i18n.TuiRemote, testRemoteURL)); got != tt.wantURL {
				t.Errorf("кадр %dx%d: повний рядок пульта = %v, want %v", tt.w, tt.h, got, tt.wantURL)
			}
			if got := strings.Contains(plain, i18n.TuiRemoteNarrow); got != tt.wantHint {
				t.Errorf("кадр %dx%d: вузька підказка = %v, want %v:\n%s", tt.w, tt.h, got, tt.wantHint, plain)
			}
		})
	}
}

func TestPlayingFrameWithoutRemote(t *testing.T) {
	m := newTestModel(t)
	m.screen = screenPlaying
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if plain := ansi.Strip(m.View().Content); strings.Contains(plain, i18n.TuiRemoteNarrow) || strings.Contains(plain, "/r/") {
		t.Errorf("без пульта рядка бути не має:\n%s", plain)
	}
}

// ---- плейлист пульта (S19) ----

// waitLivePlaying — телефон на власній горутині: чекає, поки сесія з'явиться у
// вікні Live, як це робить обробник HTTP.
func waitLivePlaying(live *playback.Live) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := live.Snapshot()
		if err != nil {
			return err
		}
		if snap.Playing {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("пульт не дочекався сесії")
}

// waitJournalFile чекає першого запису журналу сесії.
func waitJournalFile(dir string) error {
	path := filepath.Join(dir, "state", "current.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("журнал сесії не записано")
}

// Тап по рядку «Серія 3» у списку на телефоні під час серії 1: сесія
// закривається з наміром IntentPlay, і ланцюжок іде на названу серію, а не на
// наступну. Прогрес серії 1 при цьому зберігається.
func TestJourneyRemotePlayRequestSwitchesEpisode(t *testing.T) {
	dir := t.TempDir()
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	m, fp, st := journeyModelIn(t, dir,
		held,
		playertest.NewSession(player.EndQuit, []float64{30}, []float64{1440}),
	)
	m.eng.Autoplay = false
	live := &playback.Live{}
	m.eng.Live = live
	tr := &trace{}

	done := make(chan error, 1)
	go func() {
		if err := waitLivePlaying(live); err != nil {
			done <- err
			return
		}
		// журнал уже має позицію: пульт закриває сесію без додаткового
		// семплу, тож збережений прогрес залежить від тіка Run
		if err := waitJournalFile(dir); err != nil {
			done <- err
			return
		}
		pl := live.Playlist()
		if pl.Gen == 0 || len(pl.Episodes) != journeyEpisodes || !pl.Episodes[0].Current {
			done <- fmt.Errorf("плейлист під час гри = %+v", pl)
			return
		}
		done <- live.RequestPlay(pl.Gen, 3)
	}()

	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenStudio)
	m = press(t, m, tr, tea.KeyEnter, "")

	if err := <-done; err != nil {
		t.Fatalf("пульт: %v", err)
	}
	mustScreen(t, m, screenEpisodes)
	starts := fp.Starts()
	if len(starts) != 2 {
		t.Fatalf("запусків плеєра = %d, want 2 (сліди: %v)", len(starts), tr.screens)
	}
	if !strings.HasSuffix(starts[1].MediaTitle, " · 3") {
		t.Errorf("Start[1] = %+v, want серію 3", starts[1])
	}
	if tr.count(screenPlaying) != 1 {
		t.Errorf("входів на екран відтворення = %d, want 1", tr.count(screenPlaying))
	}
	lib, err := st.LoadLibrary()
	if err != nil {
		t.Fatal(err)
	}
	title := lib.TitleByRef(journeyRef)
	if p := lib.ProgressFor(title.ID, 1); p == nil || p.Completed || p.PositionSec != 40 {
		t.Errorf("серія 1 = %+v, want 40 с без Completed", p)
	}
}

// «Продовжити» з домівки: список серій приїжджає вже під час гри
// (navigate:false), і пульт мусить його побачити з підсвіченою серією.
func TestJourneyContinuePublishesPlaylistWhilePlaying(t *testing.T) {
	held := playertest.NewSession(player.EndQuit, []float64{40}, []float64{1440})
	held.Hold = true
	m, _, _ := journeyModel(t,
		playertest.NewSession(player.EndEOF, []float64{1400}, []float64{1440}),
		held,
	)
	m.eng.Autoplay = false
	live := &playback.Live{}
	m.eng.Live = live
	tr := &trace{}

	// перший перегляд: серія 1 до кінця, далі назад на домівку
	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenStudio)
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	m = press(t, m, tr, tea.KeyEsc, "")
	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenHome)
	if gen := live.CurrentGen(); gen != 0 {
		t.Fatalf("на домівці плейлист має бути порожній, а gen = %d", gen)
	}

	type snapshot struct {
		pl  playback.Playlist
		err error
	}
	got := make(chan snapshot, 1)
	go func() {
		if err := waitLivePlaying(live); err != nil {
			got <- snapshot{err: err}
			return
		}
		pl := live.Playlist()
		got <- snapshot{pl: pl, err: live.Stop()}
	}()

	selectTestItem(t, &m, func(it item) bool { _, ok := it.payload.(payloadResume); return ok })
	m = press(t, m, tr, tea.KeyEnter, "")

	res := <-got
	if res.err != nil {
		t.Fatalf("пульт: %v", res.err)
	}
	if res.pl.Gen == 0 || len(res.pl.Episodes) != journeyEpisodes {
		t.Fatalf("плейлист під час «Продовжити» = %+v", res.pl)
	}
	if !res.pl.Ref.Same(journeyRef) || res.pl.Title != journeyRef.Name {
		t.Errorf("плейлист належить чужому тайтлу: %+v", res.pl)
	}
	if !res.pl.Episodes[0].Watched || !res.pl.Episodes[1].Current {
		t.Errorf("серії = %+v, want 1 переглянуту і 2 поточну", res.pl.Episodes)
	}
}

// Ручна позначка не змінює активної серії, але змінює список на телефоні —
// отже, і його покоління.
func TestMarkWatchedRepublishesPlaylist(t *testing.T) {
	m := newTestModel(t)
	live := &playback.Live{}
	m.eng.Live = live
	ref := testRefs("playlist-mark", 1)[0]
	m.ref, m.reqID = ref, 1
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: ref, eps: testEpisodes(3), req: 1, navigate: true})
	mustScreen(t, m, screenEpisodes)

	first := live.CurrentGen()
	if first == 0 || len(live.Playlist().Episodes) != 3 {
		t.Fatalf("плейлист після списку серій = %+v", live.Playlist())
	}
	selectTestItem(t, &m, func(it item) bool { p, ok := it.payload.(payloadEp); return ok && p.num == 2 })
	m, _ = pressTestKey(t, m, 'x', "x")

	pl := live.Playlist()
	if pl.Gen == first {
		t.Fatalf("покоління не змінилося після позначки: %d", pl.Gen)
	}
	if !pl.Episodes[1].Watched || pl.Episodes[0].Watched {
		t.Fatalf("плейлист = %+v, want переглянуту лише серію 2", pl.Episodes)
	}
}

// Тайтл A → домівка → тайтл B: на домівці списку немає взагалі, а список B
// приходить із новим поколінням — старе посилання з телефона не спрацює.
func TestPlaylistFollowsTitleChanges(t *testing.T) {
	m := newTestModel(t)
	live := &playback.Live{}
	m.eng.Live = live
	refs := testRefs("playlist-switch", 2)

	m.ref, m.reqID = refs[0], 1
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: refs[0], eps: testEpisodes(2), req: 1, navigate: true})
	genA := live.CurrentGen()
	if genA == 0 {
		t.Fatal("список A не опубліковано")
	}

	m.showHome()
	if live.CurrentGen() != 0 {
		t.Fatalf("на домівці gen = %d, want 0", live.CurrentGen())
	}

	m.ref = refs[1]
	m.reqID = 2
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: refs[1], eps: testEpisodes(4), req: 2, navigate: true})
	pl := live.Playlist()
	if pl.Gen == genA || pl.Gen == 0 {
		t.Fatalf("gen B = %d, а в A був %d", pl.Gen, genA)
	}
	if !pl.Ref.Same(refs[1]) || len(pl.Episodes) != 4 {
		t.Fatalf("плейлист B = %+v", pl)
	}
}

// «Продовжити B», коли в моделі ще лежать серії A: ранній resolvedMsg не має
// права опублікувати чужий список — до приходу серій B пульт не показує нічого.
func TestPlaylistNeverPublishesForeignEpisodes(t *testing.T) {
	m := newTestModel(t)
	live := &playback.Live{}
	m.eng.Live = live
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	refs := testRefs("playlist-foreign", 2)

	// серії A вже в моделі
	m.reqID = 1
	m.ref = refs[0]
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: refs[0], eps: testEpisodes(3), req: 1, navigate: true})
	live.ClearPlaylist()

	// «Продовжити B»: resolve випередив episodes
	m.ref, m.reqID = refs[1], 2
	m, _ = updateTestModel(t, m, resolvedMsg{req: 2, res: &playback.Resolved{
		Ref: refs[1], Episode: 2,
		Source: provider.Source{Studio: "Studio", Kind: provider.KindDub, Episode: 2},
	}})
	mustScreen(t, m, screenPlaying)
	if pl := live.Playlist(); pl.Gen != 0 {
		t.Fatalf("опубліковано список без серій цього тайтлу: %+v", pl)
	}

	// серії B приїхали — тепер список є, і він саме B
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: refs[1], eps: testEpisodes(5), req: 2, navigate: false})
	pl := live.Playlist()
	if !pl.Ref.Same(refs[1]) || len(pl.Episodes) != 5 || !pl.Episodes[1].Current {
		t.Fatalf("плейлист B = %+v", pl)
	}
}

// Між тапом на телефоні й обробкою запиту список міг змінитися: такий запит
// відкидається мовчки, і резолв не починається.
func TestRemotePlayRequestDroppedWhenPlaylistChanged(t *testing.T) {
	m := newTestModel(t)
	live := &playback.Live{}
	m.eng.Live = live
	ref := testRefs("playlist-stale", 1)[0]
	m.ref, m.reqID = ref, 1
	m, _ = updateTestModel(t, m, episodesDoneMsg{ref: ref, eps: testEpisodes(3), req: 1, navigate: true})

	stale := live.CurrentGen()
	if err := live.RequestPlay(stale, 3); err != nil {
		t.Fatalf("RequestPlay: %v", err)
	}
	req := <-live.Requests()
	// поки запит їхав, список перевидали (наприклад, позначкою «переглянуто»)
	m.publishPlaylist()

	before := m.reqID
	m, cmd := updateTestModel(t, m, remotePlayMsg{req: req})
	if m.status == i18n.TuiResolving || m.pendingEp != 0 || m.reqID != before {
		t.Fatalf("застарілий запит почав резолв: status %q, ep %d", m.status, m.pendingEp)
	}
	if cmd == nil {
		t.Fatal("скриньку не переозброєно")
	}

	// свіжий запит із того ж списку працює
	if err := live.RequestPlay(live.CurrentGen(), 3); err != nil {
		t.Fatalf("RequestPlay(свіжий): %v", err)
	}
	m, _ = updateTestModel(t, m, remotePlayMsg{req: <-live.Requests()})
	if m.pendingEp != 3 || m.status != i18n.TuiResolving {
		t.Fatalf("свіжий запит не почав резолв: status %q, ep %d", m.status, m.pendingEp)
	}
}

// Пошук → серії A → Esc назад у пошук: список тайтлу вже не наш, тому тап по
// ньому з телефона отримує відмову, а не запускає чужу серію.
func TestPlaylistClearedWhenLeavingEpisodes(t *testing.T) {
	m, _, _ := journeyModel(t)
	live := &playback.Live{}
	m.eng.Live = live
	tr := &trace{}

	m = press(t, m, tr, '/', "/")
	m.input.SetValue("фрірен")
	m = press(t, m, tr, tea.KeyEnter, "")
	m = press(t, m, tr, tea.KeyEnter, "")
	mustScreen(t, m, screenEpisodes)
	gen := live.CurrentGen()
	if gen == 0 {
		t.Fatal("на екрані серій список має бути опублікований")
	}

	m = press(t, m, tr, tea.KeyEsc, "")
	mustScreen(t, m, screenSearch)
	if err := live.RequestPlay(gen, 2); !errors.Is(err, playback.ErrStalePlaylist) {
		t.Fatalf("тап по старому списку = %v, want ErrStalePlaylist", err)
	}
	select {
	case req := <-live.Requests():
		t.Fatalf("застарілий запит потрапив у скриньку: %+v", req)
	default:
	}
	if m.status == i18n.TuiResolving {
		t.Fatalf("статус = %q, резолву бути не мало", m.status)
	}
}
