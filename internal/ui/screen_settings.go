package ui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Basmanjacks/uaanime/internal/i18n"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// settingID — ключ налаштування на екрані; збігається з полем config.json
// лише за змістом, не за назвою: екран показує людські підписи.
type settingID string

const (
	settingPlayer   settingID = "player"
	settingAutoplay settingID = "autoplay"
	settingKind     settingID = "kind"
	settingStudio   settingID = "studio"
	settingRemote   settingID = "remote"
)

// settingsOrder — порядок рядків у секції «Перегляд»; пульт живе у своїй.
var settingsOrder = []settingID{settingPlayer, settingAutoplay, settingKind, settingStudio}

// settingValue — одне значення налаштування: сире (для config.json), підпис
// і пояснення (другий рядок на екрані значень).
type settingValue struct {
	value, label, note string
}

func settingTitle(id settingID) string {
	switch id {
	case settingPlayer:
		return i18n.TuiSetPlayer
	case settingAutoplay:
		return i18n.TuiSetAutoplay
	case settingKind:
		return i18n.TuiSetKind
	case settingStudio:
		return i18n.TuiSetStudio
	case settingRemote:
		return i18n.TuiSetRemote
	}
	return ""
}

func (m *Model) settingCurrent(id settingID) string {
	switch id {
	case settingPlayer:
		return m.cfg.Player
	case settingAutoplay:
		return m.cfg.Autoplay
	case settingKind:
		return m.cfg.PreferKind
	case settingStudio:
		return m.cfg.FavoriteStudio
	case settingRemote:
		return m.cfg.Remote
	}
	return ""
}

// settingValues — усі значення налаштування. Для студії множина будується з
// поточного значення і знайомих студій: «не вибрано» є завжди, тому старе
// значення з config.json можна скинути навіть із порожньою бібліотекою.
func (m *Model) settingValues(id settingID) []settingValue {
	switch id {
	case settingPlayer:
		// Назви плеєрів — назви продуктів, не перекладаються.
		values := []settingValue{{value: "vlc", label: "VLC"}, {value: "mpv", label: "mpv"}}
		if m.opts.DetectPlayer != nil {
			for i := range values {
				p, _, err := m.opts.DetectPlayer(values[i].value)
				if err != nil || p == nil || p.ID() != values[i].value {
					values[i].note = i18n.MsgNoPlayer
				}
			}
		}
		return values
	case settingAutoplay:
		return []settingValue{
			{value: "always", label: i18n.TuiValAuto, note: i18n.TuiValAutoNote},
			{value: "never", label: i18n.TuiValManual, note: i18n.TuiValManualNote},
		}
	case settingKind:
		return []settingValue{
			{value: string(provider.KindDub), label: i18n.KindLabel(provider.KindDub), note: i18n.TuiSetKindDubNote},
			{value: string(provider.KindVoiceover), label: i18n.KindLabel(provider.KindVoiceover), note: i18n.TuiSetKindVoiceNote},
			{value: string(provider.KindSub), label: i18n.KindLabel(provider.KindSub), note: i18n.TuiSetKindSubNote},
		}
	case settingStudio:
		values := []settingValue{{value: "", label: i18n.TuiSetStudioNone}}
		seen := map[string]bool{}
		// Конфіг міг прийти не через LoadConfig (нормалізацію) — підпис чистимо.
		if cur := m.cfg.FavoriteStudio; cur != "" {
			values = append(values, settingValue{value: cur, label: provider.CleanText(cur)})
			seen[cur] = true
		}
		for _, s := range m.eng.KnownStudios() {
			if !seen[s] {
				values = append(values, settingValue{value: s, label: s})
			}
		}
		return values
	case settingRemote:
		return []settingValue{
			{value: "on", label: i18n.TuiRemoteTokened, note: i18n.TuiRemoteTokenNote},
			{value: "open", label: i18n.TuiRemoteOpen, note: i18n.TuiRemoteOpenNote},
			{value: "off", label: i18n.TuiRemoteOff, note: i18n.TuiRemoteOffNote},
		}
	}
	return nil
}

// settingLabel — підпис поточного значення для рядка «назва · значення».
func (m *Model) settingLabel(id settingID) string {
	cur := m.settingCurrent(id)
	values := m.settingValues(id)
	if id == settingStudio && cur == "" && len(values) == 1 {
		return i18n.TuiSetStudioEmpty
	}
	for _, v := range values {
		if v.value == cur {
			return v.label
		}
	}
	return provider.CleanText(cur)
}

// showSettings будує екран налаштувань. cursor < 0 — на перший рядок.
func (m *Model) showSettings(cursor int) {
	m.setScreen(screenSettings)
	items := []item{{header: true, title: i18n.TuiBlockPlayback}}
	for _, id := range settingsOrder {
		items = append(items, item{title: settingTitle(id), meta: m.settingLabel(id), payload: payloadSetting{id: id}})
	}
	items = append(items,
		item{header: true, spacer: true},
		item{header: true, title: i18n.TuiBlockRemote},
		item{title: settingTitle(settingRemote), meta: m.settingLabel(settingRemote), payload: payloadSetting{id: settingRemote}})
	items = append(items, m.remoteNotes()...)
	items = append(items,
		item{header: true, spacer: true},
		item{header: true, rule: true, title: i18n.TuiBlockAbout},
		m.note(fmt.Sprintf(i18n.TuiDataDir, shortenHome(m.opts.DataDir))))
	if cursor < 0 {
		cursor = firstRow(items)
	}
	_ = m.setItems(items, cursor)
}

// note — довідковий рядок. Текст приходить ззовні (шляхи, адреси, помилки),
// тому чиститься тут, а не в кожного, хто його формує.
func (m *Model) note(text string) item {
	return item{header: true, note: true, title: provider.CleanText(text)}
}

// remoteNotes — адреса пульта під «Режим». Обрізаний URL гірший за жоден
// (половина токена нікуди не веде), тому у вузькому списку — лише підказка.
func (m *Model) remoteNotes() []item {
	var notes []item
	fits := func(text string) bool {
		limit := m.list.Width() - rowIndent
		return m.list.Width() <= 0 || lipgloss.Width(text) <= limit
	}
	switch {
	case m.remote.Err != nil:
		notes = append(notes, m.note(fmt.Sprintf(i18n.MsgRemoteFailed, m.remote.Err)))
	case m.remote.URL != "":
		if fits(m.remote.URL) {
			notes = append(notes, m.note(m.remote.URL))
			if alt := fmt.Sprintf(i18n.TuiRemoteAltURL, m.remote.AltURL); m.remote.AltURL != "" && fits(alt) {
				notes = append(notes, m.note(alt))
			}
		} else {
			notes = append(notes, m.note(i18n.TuiRemoteNarrow))
		}
	}
	if m.remote.Warn != nil {
		notes = append(notes, m.note(fmt.Sprintf(i18n.MsgRemoteIdentityUnsaved, m.remote.Warn)))
	}
	return notes
}

func shortenHome(dir string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(dir, home) {
		return "~" + strings.TrimPrefix(dir, home)
	}
	return dir
}

// showSettingValue — екран значень: той самий список у двох рядках (підпис +
// пояснення), ✓ на поточному, як вибір студії.
func (m *Model) showSettingValue(id settingID) {
	m.settingID = id
	m.setScreen(screenSettingValue)
	m.setDelegate(true)
	cur := m.settingCurrent(id)
	values := m.settingValues(id)
	items := make([]item, 0, len(values))
	cursor := 0
	for i, v := range values {
		it := item{title: v.label, meta: v.note, payload: payloadSettingValue{id: id, value: v.value}}
		if v.value == cur {
			it.icon, it.iconAccent = m.ic.Done, true
			cursor = i
		}
		items = append(items, it)
	}
	_ = m.setItems(items, cursor)
}

// applySetting пише значення в конфіг, зберігає й застосовує до рушія.
// Повертає статус і текст помилки; викликач ставить їх ПІСЛЯ перебудови
// екрана, бо showSettings читає вже оновлений стан пульта.
// sync: пише поля рушія на Update-горутині; фонові команди читають лише знімки.
func (m *Model) applySetting(id settingID, value string) (status, errText string) {
	switch id {
	case settingPlayer:
		m.cfg.Player = value
	case settingAutoplay:
		m.cfg.Autoplay = value
	case settingKind:
		m.cfg.PreferKind = value
	case settingStudio:
		m.cfg.FavoriteStudio = value
	case settingRemote:
		m.cfg.Remote = value
	}
	var saveErr error
	if m.eng.Store != nil {
		saveErr = m.eng.Store.SaveConfig(m.cfg)
	}
	status = i18n.TuiSetSaved
	switch id {
	case settingAutoplay:
		m.eng.Autoplay = m.cfg.Autoplay == "always"
	case settingKind:
		m.eng.Prefs.PreferKind = provider.Kind(m.cfg.PreferKind)
	case settingStudio:
		m.eng.Prefs.FavoriteStudio = m.cfg.FavoriteStudio
	case settingPlayer:
		if m.opts.DetectPlayer != nil {
			p, fallback, err := m.opts.DetectPlayer(m.cfg.Player)
			m.eng.Player, m.eng.PlayerFallback = p, fallback
			switch {
			case err != nil || p == nil:
				errText = i18n.MsgNoPlayer
			case fallback:
				status = fmt.Sprintf(i18n.MsgPlayerFallback, p.ID())
			}
		}
	case settingRemote:
		if m.opts.RestartRemote != nil {
			m.remote = m.opts.RestartRemote(m.cfg.Remote)
			switch {
			case m.remote.Err != nil:
				errText = fmt.Sprintf(i18n.MsgRemoteFailed, m.remote.Err)
			case m.remote.Ephemeral:
				status = fmt.Sprintf(i18n.MsgRemotePortBusy, m.remote.SavedPort, m.remote.URL)
			case m.cfg.Remote == "open":
				status = fmt.Sprintf(i18n.TuiRemoteOpenStatus, m.remote.URL)
			case m.cfg.Remote == "off":
				status = i18n.TuiRemoteOffStatus
			default:
				status = fmt.Sprintf(i18n.TuiRemote, m.remote.URL)
			}
		}
	}
	// Незбережений конфіг важливіший за будь-який успіх: зміна застосована,
	// але не переживе перезапуск.
	if saveErr != nil {
		status, errText = "", fmt.Sprintf(i18n.MsgConfigSaveFailed, saveErr)
	}
	return status, errText
}

// cycleSetting — ←/→ на рядку налаштування: сусіднє значення по колу, без
// переходу на екран значень. Стек не чіпаємо: екран той самий.
func (m *Model) cycleSetting(dir int) {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return
	}
	p, ok := it.payload.(payloadSetting)
	if !ok {
		return
	}
	values := m.settingValues(p.id)
	if len(values) < 2 {
		return
	}
	cur := m.settingCurrent(p.id)
	idx := 0
	for i, v := range values {
		if v.value == cur {
			idx = i
			break
		}
	}
	next := ((idx+dir)%len(values) + len(values)) % len(values)
	status, errText := m.applySetting(p.id, values[next].value)
	m.showSettings(m.list.GlobalIndex())
	m.status, m.errText = status, errText
}

// openSettings — вхід з домівки: кадр у стек, як openSearch.
func (m Model) openSettings() (Model, bool) {
	// Зміна налаштувань пише поля рушія, тому не під час навігації в польоті
	// й не під час відтворення (Run у фоні читає Player).
	if m.pending != nil || m.playCancel != nil {
		return m, false
	}
	m.stack = append(m.stack, m.snapshot())
	m.showSettings(-1)
	m.errText, m.status = "", ""
	return m, true
}

// pickSettingValue — Enter на екрані значень: застосувати й повернутися на
// екран налаштувань. Не через back(): той відновив би старі рядки зі знімка
// й стер статус, а тут потрібні свіжі значення та нова адреса пульта.
func (m Model) pickSettingValue(p payloadSettingValue) Model {
	cursor := -1
	if n := len(m.stack); n > 0 && m.stack[n-1].screen == screenSettings {
		cursor = m.stack[n-1].cursor
		m.stack = m.stack[:n-1]
	}
	status, errText := m.applySetting(p.id, p.value)
	m.showSettings(cursor)
	m.status, m.errText = status, errText
	return m
}
