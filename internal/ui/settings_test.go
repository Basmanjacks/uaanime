package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Basmanjacks/uaanime/internal/extractor"
	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/library"
	"github.com/Basmanjacks/uaanime/internal/playback"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
	"github.com/Basmanjacks/uaanime/internal/providertest"
	"github.com/Basmanjacks/uaanime/internal/store"
)

// namedPlayer — плеєр із заданим ID для перевірки підписів і fallback.
type namedPlayer struct {
	fakePlayer
	id string
}

func (p namedPlayer) ID() string { return p.id }

func newSettingsModel(t *testing.T, opts Options) Model {
	t.Helper()
	m, _ := newSettingsModelDir(t, opts)
	return m
}

func newSettingsModelDir(t *testing.T, opts Options) (Model, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := &playback.Engine{Store: st, Lib: &library.Library{}, Player: fakePlayer{}, Autoplay: true}
	if opts.DataDir == "" {
		opts.DataDir = dir
	}
	m := New(eng, opts)
	// без розміру вікна список не малює рядків
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, dir
}

func openSettings(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = pressTestKey(t, m, ',', ",")
	if m.screen != screenSettings {
		t.Fatalf("screen after , = %d, want %d", m.screen, screenSettings)
	}
	return m
}

func selectSetting(t *testing.T, m *Model, id settingID) {
	t.Helper()
	selectTestItem(t, m, func(it item) bool {
		p, ok := it.payload.(payloadSetting)
		return ok && p.id == id
	})
}

func settingRow(t *testing.T, m Model, id settingID) item {
	t.Helper()
	for _, it := range homeItems(t, m) {
		if p, ok := it.payload.(payloadSetting); ok && p.id == id {
			return it
		}
	}
	t.Fatalf("рядка налаштування %q немає", id)
	return item{}
}

func noteTitles(t *testing.T, m Model) []string {
	t.Helper()
	var notes []string
	for _, it := range homeItems(t, m) {
		if it.note {
			notes = append(notes, it.title)
		}
	}
	return notes
}

func TestHomeSettingsRowLastInMoreAndCommaOpens(t *testing.T) {
	m := newSettingsModel(t, Options{})
	seedTestLibrary(&m, testRefs("lib", 1), library.StateWatching)
	items := homeItems(t, m)
	idx := -1
	for i, it := range items {
		if _, ok := it.payload.(payloadSettings); ok {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("на домівці немає рядка налаштувань")
	}
	if items[idx].icon != "⚙" || items[idx].title != i18n.TuiSettingsItem {
		t.Fatalf("рядок = %+v", items[idx])
	}
	// останній у «ЩЕ»: далі або кінець, або заголовок каталогу
	if idx+1 < len(items) && !items[idx+1].header {
		t.Fatalf("після налаштувань іде звичайний рядок: %+v", items[idx+1])
	}

	// Enter на рядку
	m.list.Select(idx)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenSettings {
		t.Fatalf("Enter на рядку: екран %d", m.screen)
	}
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome || m.list.GlobalIndex() != idx {
		t.Fatalf("після Esc: екран %d, курсор %d (want %d)", m.screen, m.list.GlobalIndex(), idx)
	}

	// клавіша ,
	m = openSettings(t, m)
	if !strings.Contains(ansi.Strip(m.View().Content), strings.ToUpper(i18n.TuiBlockPlayback)) {
		t.Fatal("немає секції «Перегляд»")
	}
	if !strings.Contains(i18n.TuiHintHome, ", ") {
		t.Fatal("підказка домівки не згадує клавішу ,")
	}
}

func TestSettingsGateDuringPendingAndPlaying(t *testing.T) {
	m := newSettingsModel(t, Options{})
	snap := m.snapshot()
	m.pending = &snap
	m, _ = pressTestKey(t, m, ',', ",")
	if m.screen != screenHome {
		t.Fatal("налаштування відкрилися під час навігації в польоті")
	}
	m.pending = nil
	m.playCancel = func() {}
	m, _ = pressTestKey(t, m, ',', ",")
	if m.screen != screenHome {
		t.Fatal("налаштування відкрилися під час відтворення")
	}
}

func TestSettingsCycleAutoplaySaves(t *testing.T) {
	m := newSettingsModel(t, Options{})
	m = openSettings(t, m)
	selectSetting(t, &m, settingAutoplay)
	if got := settingRow(t, m, settingAutoplay).meta; got != i18n.TuiValAuto {
		t.Fatalf("початкове значення = %q", got)
	}
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if got := settingRow(t, m, settingAutoplay).meta; got != i18n.TuiValManual {
		t.Fatalf("після → = %q", got)
	}
	if m.eng.Autoplay {
		t.Fatal("eng.Autoplay не вимкнено")
	}
	if m.status != i18n.TuiSetSaved {
		t.Fatalf("статус = %q", m.status)
	}
	cfg, err := m.eng.Store.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Autoplay != "never" {
		t.Fatalf("config.json autoplay = %q", cfg.Autoplay)
	}
	// ← повертає по колу
	m, _ = pressTestKey(t, m, tea.KeyLeft, "")
	if !m.eng.Autoplay || m.cfg.Autoplay != "always" {
		t.Fatalf("після ← = %q", m.cfg.Autoplay)
	}
	// курсор лишився на тому самому рядку
	if p, ok := m.list.SelectedItem().(item).payload.(payloadSetting); !ok || p.id != settingAutoplay {
		t.Fatalf("курсор з'їхав: %+v", m.list.SelectedItem())
	}
}

func TestSettingsRemoteValueScreenRestarts(t *testing.T) {
	var got []string
	info := RemoteInfo{URL: "http://mac.local:51234/r/0123456789abcdef0123456789abcdef", AltURL: "http://192.168.1.2:51234/r/0123456789abcdef0123456789abcdef"}
	m := newSettingsModel(t, Options{
		Remote: info,
		RestartRemote: func(mode string) RemoteInfo {
			got = append(got, mode)
			switch mode {
			case "open":
				return RemoteInfo{URL: "http://mac.local:51234/", AltURL: "http://192.168.1.2:51234/"}
			case "off":
				return RemoteInfo{}
			}
			return info
		},
	})
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = openSettings(t, m)
	notes := noteTitles(t, m)
	if len(notes) < 2 || notes[0] != info.URL || !strings.Contains(notes[1], info.AltURL) {
		t.Fatalf("довідкові рядки = %v", notes)
	}

	selectSetting(t, &m, settingRemote)
	cursor := m.list.GlobalIndex()
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenSettingValue {
		t.Fatalf("екран = %d", m.screen)
	}
	if sel, ok := m.list.SelectedItem().(item); !ok || sel.icon != m.ic.Done || sel.title != i18n.TuiRemoteTokened {
		t.Fatalf("✓ не на поточному: %+v", m.list.SelectedItem())
	}
	if !strings.Contains(ansi.Strip(m.View().Content), i18n.TuiRemoteOpenNote) {
		t.Fatal("пояснення значень не видно")
	}
	m, _ = pressTestKey(t, m, tea.KeyDown, "")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if len(got) != 1 || got[0] != "open" {
		t.Fatalf("RestartRemote викликано з %v", got)
	}
	if m.screen != screenSettings || m.list.GlobalIndex() != cursor {
		t.Fatalf("після вибору: екран %d, курсор %d (want %d)", m.screen, m.list.GlobalIndex(), cursor)
	}
	if got := settingRow(t, m, settingRemote).meta; got != i18n.TuiRemoteOpen {
		t.Fatalf("рядок режиму = %q", got)
	}
	if notes := noteTitles(t, m); len(notes) < 1 || notes[0] != "http://mac.local:51234/" {
		t.Fatalf("адреса не оновилась: %v", notes)
	}
	if !strings.Contains(m.status, "http://mac.local:51234/") {
		t.Fatalf("статус = %q", m.status)
	}
	if m.cfg.Remote != "open" {
		t.Fatalf("cfg.Remote = %q", m.cfg.Remote)
	}
	// Esc з екрана налаштувань — на домівку; кадр екрана значень знято
	m, _ = pressTestKey(t, m, tea.KeyEsc, "")
	if m.screen != screenHome {
		t.Fatalf("після Esc: екран %d", m.screen)
	}

	// вимкнено: адреси немає, статус про вимкнення
	m = openSettings(t, m)
	selectSetting(t, &m, settingRemote)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.cfg.Remote != "off" || m.status != i18n.TuiRemoteOffStatus || m.remote.URL != "" {
		t.Fatalf("off: cfg=%q status=%q url=%q", m.cfg.Remote, m.status, m.remote.URL)
	}
	if notes := noteTitles(t, m); len(notes) != 1 || !strings.HasPrefix(notes[0], "дані:") {
		t.Fatalf("довідкові рядки при off = %v", notes)
	}
}

func TestSettingsRemoteStatusesAndNotes(t *testing.T) {
	cases := []struct {
		name   string
		info   RemoteInfo
		status string
		err    string
		note   string
	}{
		{"ephemeral", RemoteInfo{URL: "http://mac.local:5/r/x", Ephemeral: true, SavedPort: 51234}, "51234", "", "http://mac.local:5/r/x"},
		{"fatal", RemoteInfo{Err: errors.New("listen: boom")}, "", "boom", "boom"},
		{"warn", RemoteInfo{URL: "http://mac.local:5/r/x", Warn: errors.New("write: denied")}, "http://mac.local:5/r/x", "", "denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newSettingsModel(t, Options{RestartRemote: func(string) RemoteInfo { return tc.info }})
			m = openSettings(t, m)
			selectSetting(t, &m, settingRemote)
			m, _ = pressTestKey(t, m, tea.KeyRight, "")
			if !strings.Contains(m.status, tc.status) {
				t.Fatalf("статус = %q, want містить %q", m.status, tc.status)
			}
			if !strings.Contains(m.errText, tc.err) {
				t.Fatalf("errText = %q, want містить %q", m.errText, tc.err)
			}
			if notes := strings.Join(noteTitles(t, m), "\n"); !strings.Contains(notes, tc.note) {
				t.Fatalf("довідкові рядки = %q, want містить %q", notes, tc.note)
			}
		})
	}
	// без хука — лише конфіг
	m := newSettingsModel(t, Options{})
	m = openSettings(t, m)
	selectSetting(t, &m, settingRemote)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.cfg.Remote != "open" || m.status != i18n.TuiSetSaved {
		t.Fatalf("без хука: cfg=%q status=%q", m.cfg.Remote, m.status)
	}
}

func TestSettingsPlayerFallbackAndMissing(t *testing.T) {
	detect := func(id string) (player.Player, bool, error) {
		switch id {
		case "vlc":
			return namedPlayer{id: "vlc"}, false, nil
		case "mpv":
			return namedPlayer{id: "vlc"}, true, nil // mpv немає — fallback на VLC
		}
		return nil, false, nil
	}
	m := newSettingsModel(t, Options{DetectPlayer: detect})
	m = openSettings(t, m)
	selectSetting(t, &m, settingPlayer)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	plain := ansi.Strip(m.View().Content)
	if !strings.Contains(plain, "mpv") || !strings.Contains(plain, i18n.MsgNoPlayer) {
		t.Fatalf("екран значень плеєра:\n%s", plain)
	}
	m, _ = pressTestKey(t, m, tea.KeyDown, "")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.cfg.Player != "mpv" || !m.eng.PlayerFallback || m.eng.Player.ID() != "vlc" {
		t.Fatalf("після вибору mpv: cfg=%q fallback=%v player=%v", m.cfg.Player, m.eng.PlayerFallback, m.eng.Player)
	}
	if want := "граю через vlc"; !strings.Contains(m.status, want) {
		t.Fatalf("статус = %q", m.status)
	}

	m = newSettingsModel(t, Options{DetectPlayer: func(string) (player.Player, bool, error) { return nil, false, nil }})
	m = openSettings(t, m)
	selectSetting(t, &m, settingPlayer)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.errText != i18n.MsgNoPlayer || m.eng.Player != nil {
		t.Fatalf("без плеєра: errText=%q player=%v", m.errText, m.eng.Player)
	}
}

func TestSettingsStudioListAndReset(t *testing.T) {
	m := newSettingsModel(t, Options{})
	m = openSettings(t, m)
	if got := settingRow(t, m, settingStudio).meta; got != i18n.TuiSetStudioEmpty {
		t.Fatalf("порожня бібліотека: %q", got)
	}
	selectSetting(t, &m, settingStudio)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.screen != screenSettings {
		t.Fatal("Enter без знайомих студій відкрив екран значень")
	}
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.cfg.FavoriteStudio != "" {
		t.Fatalf("→ без студій змінив значення: %q", m.cfg.FavoriteStudio)
	}

	// із піном у бібліотеці
	m.eng.Lib.Entries = []*library.Entry{{TitleID: "t", StudioPin: "AniUA"}}
	m.showSettings(m.list.GlobalIndex())
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.cfg.FavoriteStudio != "AniUA" || m.eng.Prefs.FavoriteStudio != "AniUA" {
		t.Fatalf("студія не застосована: cfg=%q prefs=%q", m.cfg.FavoriteStudio, m.eng.Prefs.FavoriteStudio)
	}

	// старе значення з конфігу при порожній бібліотеці — у списку з ✓, скидається
	m = newSettingsModel(t, Options{Cfg: &store.Config{FavoriteStudio: "Old"}})
	m = openSettings(t, m)
	if got := settingRow(t, m, settingStudio).meta; got != "Old" {
		t.Fatalf("старе значення = %q", got)
	}
	selectSetting(t, &m, settingStudio)
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if sel := m.list.SelectedItem().(item); sel.title != "Old" || sel.icon != m.ic.Done {
		t.Fatalf("✓ не на старому значенні: %+v", sel)
	}
	m, _ = pressTestKey(t, m, tea.KeyUp, "")
	m, _ = pressTestKey(t, m, tea.KeyEnter, "")
	if m.cfg.FavoriteStudio != "" || m.eng.Prefs.FavoriteStudio != "" {
		t.Fatalf("не скинулось: cfg=%q", m.cfg.FavoriteStudio)
	}
	// множина знову лише «не вибрано» — рядок каже, звідки візьмуться варіанти
	if got := settingRow(t, m, settingStudio).meta; got != i18n.TuiSetStudioEmpty {
		t.Fatalf("після скидання = %q", got)
	}
}

func TestSettingsSanitizesExternalText(t *testing.T) {
	const osc = "\x1b]8;;http://evil.invalid\x07"
	cfg := store.DefaultConfig()
	cfg.FavoriteStudio = "Ani" + osc + "UA"
	m := newSettingsModel(t, Options{
		Cfg:     cfg,
		DataDir: "/tmp/" + osc + "data",
		Remote:  RemoteInfo{URL: "http://mac.local:5/", Warn: errors.New("warn " + osc)},
		RestartRemote: func(string) RemoteInfo {
			return RemoteInfo{Err: errors.New("fatal " + osc)}
		},
	})
	m = openSettings(t, m)
	check := func(stage string) {
		t.Helper()
		raw := m.View().Content
		if strings.Contains(raw, "\x1b]") || strings.Contains(raw, "evil.invalid") {
			t.Fatalf("%s: OSC дійшла до терміналу:\n%q", stage, raw)
		}
		plain := ansi.Strip(raw)
		if !strings.Contains(plain, "дані:") {
			t.Fatalf("%s: довідка про каталог зникла", stage)
		}
	}
	check("екран")
	if !strings.Contains(ansi.Strip(m.View().Content), "AniUA") {
		t.Fatal("студію не видно")
	}
	selectSetting(t, &m, settingRemote)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.errText == "" {
		t.Fatal("помилка перезапуску не показана")
	}
	check("errText після перезапуску")
}

func TestSettingsSaveErrorWins(t *testing.T) {
	m, dir := newSettingsModelDir(t, Options{})
	// config.json як каталог із вмістом: атомарне перейменування не пройде
	if err := os.MkdirAll(filepath.Join(dir, "config.json", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	m = openSettings(t, m)
	selectSetting(t, &m, settingAutoplay)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.errText == "" || m.status != "" {
		t.Fatalf("errText=%q status=%q", m.errText, m.status)
	}
	if plain := ansi.Strip(m.View().Content); strings.Contains(plain, i18n.TuiSetSaved) {
		t.Fatal("показано «Збережено» попри невдалий запис")
	}
	if m.eng.Autoplay {
		t.Fatal("значення в пам'яті не застосоване")
	}
}

func TestSettingsViewFitsAndNarrowURL(t *testing.T) {
	url := "http://vitaliis-macbook-pro.local:51234/r/0123456789abcdef0123456789abcdef"
	m := newSettingsModel(t, Options{Remote: RemoteInfo{URL: url}})
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = openSettings(t, m)
	content := m.View().Content
	for _, line := range strings.Split(content, "\n") {
		if w := ansi.StringWidth(line); w > 80 {
			t.Fatalf("рядок ширший за 80 (%d): %q", w, ansi.Strip(line))
		}
	}
	plain := ansi.Strip(content)
	for _, want := range []string{strings.ToUpper(i18n.TuiBlockPlayback), strings.ToUpper(i18n.TuiBlockRemote), strings.ToUpper(i18n.TuiBlockAbout), url} {
		if !strings.Contains(plain, want) {
			t.Fatalf("немає %q:\n%s", want, plain)
		}
	}
	m, _ = updateTestModel(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	m.showSettings(m.list.GlobalIndex())
	plain = ansi.Strip(m.View().Content)
	if strings.Contains(plain, "/r/") || !strings.Contains(plain, i18n.TuiRemoteNarrow) {
		t.Fatalf("вузький термінал показує обрізаний URL:\n%s", plain)
	}
}

func TestSettingsAsciiIcon(t *testing.T) {
	t.Setenv("UAANIME_ASCII", "1")
	m := newSettingsModel(t, Options{})
	for _, it := range homeItems(t, m) {
		if _, ok := it.payload.(payloadSettings); ok {
			if it.icon != "*" {
				t.Fatalf("іконка = %q", it.icon)
			}
			return
		}
	}
	t.Fatal("рядка налаштувань немає")
}

// Резолв у польоті читає знімок Prefs: зміна налаштувань після Esc не
// гониться з фоновою командою (тест має сенс під -race).
func TestSettingsPrefsChangeDuringResolve(t *testing.T) {
	m := newSettingsModel(t, Options{})
	release := make(chan struct{})
	m.eng.Provider = providertest.Stub{
		IDValue: "test", NameValue: "Test",
		SourcesFn: func(ctx context.Context, _ provider.TitleRef, _ int) ([]provider.Source, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return []provider.Source{{Studio: "X", Kind: provider.KindDub, Embed: "https://stream.invalid/e"}}, nil
		},
	}
	m.eng.Extractors = []extractor.Extractor{stubExtractor{}}
	ref := provider.TitleRef{Provider: "test", Slug: "a"}
	cmd := m.resolveCmd(ref, 1, m.nextReq(), m.eng.ResolveHints(ref, 1))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	m = openSettings(t, m)
	selectSetting(t, &m, settingKind)
	m, _ = pressTestKey(t, m, tea.KeyRight, "")
	if m.eng.Prefs.PreferKind != provider.KindVoiceover {
		t.Fatalf("Prefs = %+v", m.eng.Prefs)
	}
	close(release)
	if msg, ok := (<-done).(resolvedMsg); !ok || msg.err != nil {
		t.Fatalf("резолв: %+v", msg)
	}
}
