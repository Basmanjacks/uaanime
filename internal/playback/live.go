package playback

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Intent — чого попросив веб-пульт замість природного завершення серії.
// Живе окремо від player.EndReason: причина — це «чому вийшов процес плеєра»,
// а намір — бажання користувача, яке викликачі читають після Run.
type Intent int

const (
	IntentNone Intent = iota
	IntentNext
	IntentStop
	// IntentPlay — адресний запит «грай цю серію»; ціль лежить у Result.Requested.
	IntentPlay
)

// PlayRequest — що саме просять зіграти. Номера серії замало: між публікацією
// списку і запитом TUI міг перейти на інший тайтл, тому запит несе і Ref, і
// покоління списку (Gen), за яким застарілий запит відкидається.
type PlayRequest struct {
	Ref     provider.TitleRef
	Gen     int
	Episode int
}

// EpisodeInfo — один рядок плейлиста для пульта. Лише значення: пульт не має
// права ні читати бібліотеку, ні тримати покажчик на її пам'ять (правило 10).
type EpisodeInfo struct {
	Number      int
	Watched     bool
	PositionSec float64
	// Current — серія, що зараз грає (або яку щойно запустили).
	Current bool
}

// Playlist — опублікований список серій одного тайтлу. Gen 0 означає «списку
// немає»: пульт не має що показувати, а будь-який запит із таким поколінням
// застарілий за визначенням.
type Playlist struct {
	Gen      int
	Ref      provider.TitleRef
	Title    string
	Episodes []EpisodeInfo
}

// ErrStalePlaylist — запит із покоління списку, якого вже немає: людина тапнула
// по списку, що встиг застаріти (тайтл змінився або ми пішли з екрана серій).
var ErrStalePlaylist = errors.New("список серій застарів")

// VolumeUnknown — гучність недоступна (плеєр не відповів або нічого не грає).
// Окреме значення, бо 0 — це легітимна тиша.
const VolumeUnknown = -1.0

// Snapshot — стан сесії для пульта. Лише значення: у пульта немає доступу
// ні до бібліотеки, ні до самої сесії.
type Snapshot struct {
	Playing     bool
	Title       string
	Episode     int
	PositionSec float64
	DurationSec float64
	Paused      bool
	VolumePct   float64
	StopAfter   bool
}

// ErrNotPlaying — команда пульта, коли нічого не грає.
var ErrNotPlaying = errors.New("зараз нічого не грає")

// Live — вікно в сесію, що зараз грає, для веб-пульта. Усі методи async-safe
// і безпечні на nil-приймачі (пульт вимкнено → Engine.Live == nil). Live
// НІКОЛИ не торкається Lib: пульт живе на горутинах net/http, а бібліотека —
// лише на горутині Update (правило 10 AGENTS.md).
type Live struct {
	mu        sync.Mutex
	sess      player.Session
	title     string
	episode   int
	intent    Intent
	requested PlayRequest
	stopAfter bool

	// playlist — те, що пульт показує списком серій; playlistSeq монотонний і
	// НЕ скидається при очищенні: інакше наступна публікація віддала б
	// покоління, яким уже користувався застарілий список у браузері.
	playlist    Playlist
	playlistSeq int
	// requests — поштова скринька на час простою: пульт кладе туди адресний
	// запит, а забирає його горутина Update. Буфер 1, нове витісняє старе:
	// значення має лише останній тап.
	requests chan PlayRequest
}

// Snapshot читає позицію з сесії ПОЗА м'ютексом: RC-обмін VLC може тривати
// до 5 с і не має блокувати set/clear із Run.
func (l *Live) Snapshot() (Snapshot, error) {
	if l == nil {
		return idleSnapshot(), nil
	}
	l.mu.Lock()
	sess, title, episode, stopAfter := l.sess, l.title, l.episode, l.stopAfter
	l.mu.Unlock()
	if sess == nil {
		return idleSnapshot(), nil
	}
	pos, err := sess.TimePos()
	if err != nil {
		return Snapshot{}, fmt.Errorf("пульт: позиція: %w", err)
	}
	dur, err := sess.Duration()
	if err != nil {
		return Snapshot{}, fmt.Errorf("пульт: тривалість: %w", err)
	}
	paused, err := sess.Paused()
	if err != nil {
		return Snapshot{}, fmt.Errorf("пульт: стан паузи: %w", err)
	}
	// Гучність — єдине поле, чия помилка не валить снапшот: плеєр без звукової
	// доріжки лишається керованим, а пульт просто ховає повзунок.
	volume, err := sess.Volume()
	if err != nil {
		volume = VolumeUnknown
	}
	return Snapshot{
		Playing:     true,
		Title:       title,
		Episode:     episode,
		PositionSec: pos,
		DurationSec: dur,
		Paused:      paused,
		VolumePct:   volume,
		StopAfter:   stopAfter,
	}, nil
}

func idleSnapshot() Snapshot { return Snapshot{VolumePct: VolumeUnknown} }

func (l *Live) session() (player.Session, error) {
	if l == nil {
		return nil, ErrNotPlaying
	}
	l.mu.Lock()
	sess := l.sess
	l.mu.Unlock()
	if sess == nil {
		return nil, ErrNotPlaying
	}
	return sess, nil
}

func (l *Live) TogglePause() error {
	sess, err := l.session()
	if err != nil {
		return err
	}
	return sess.TogglePause()
}

func (l *Live) Seek(deltaSec float64) error {
	sess, err := l.session()
	if err != nil {
		return err
	}
	return sess.Seek(deltaSec)
}

func (l *Live) SeekTo(posSec float64) error {
	sess, err := l.session()
	if err != nil {
		return err
	}
	return sess.SeekTo(posSec)
}

// AddVolume міняє гучність кроком: читає поточну, клампить і ставить абсолютну.
// Відносної команди в player.Session немає навмисно — жоден із бекендів не має
// такої, що приймала б відсотки й тримала стелю (див. player.Session).
func (l *Live) AddVolume(delta float64) error {
	sess, err := l.session()
	if err != nil {
		return err
	}
	current, err := sess.Volume()
	if err != nil {
		return fmt.Errorf("пульт: гучність: %w", err)
	}
	return sess.SetVolume(min(max(current+delta, 0), 100))
}

// SetStopAfter/StopAfter — «досидіти цю серію й зупинитись». Прапорець живе
// тут, а не в конфізі: це разове бажання, спільне для TUI і пульта.
func (l *Live) SetStopAfter(on bool) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.stopAfter = on
	l.mu.Unlock()
}

func (l *Live) StopAfter() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopAfter
}

// Next просить наступну серію: намір записується, сесія закривається, і далі
// працює звичайний ланцюжок playDoneMsg / cmdPlay. Автоплей тут не питається:
// явне бажання користувача сильніше за налаштування.
func (l *Live) Next() error { return l.end(IntentNext) }

// Stop зупиняє ланцюжок серій; від Esc відрізняється лише наміром у Result.
func (l *Live) Stop() error { return l.end(IntentStop) }

// end від'єднує сесію ПІД м'ютексом і закриває її ПОЗА ним: після цього
// Snapshot чесно каже «нічого не грає», а не падає на закритій сесії — тому
// HTTP-шар може відповісти 200 одразу після команди.
func (l *Live) end(intent Intent) error {
	if l == nil {
		return ErrNotPlaying
	}
	l.mu.Lock()
	sess := l.sess
	if sess == nil {
		l.mu.Unlock()
		return ErrNotPlaying
	}
	l.sess = nil
	l.intent = intent
	l.mu.Unlock()
	sess.Close()
	return nil
}

// set відкриває вікно на нову сесію. Намір скидається на кожному старті, щоб
// залишок від Run, який помер до Finish, не протік у наступну серію.
func (l *Live) set(sess player.Session, title string, episode int) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.sess, l.title, l.episode = sess, title, episode
	l.intent, l.requested, l.stopAfter = IntentNone, PlayRequest{}, false
	l.mu.Unlock()
}

// clear закриває вікно; намір лишається до takeIntent у Finish.
func (l *Live) clear() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.sess, l.title, l.episode = nil, "", 0
	l.mu.Unlock()
}

// takeIntent віддає намір і його ціль рівно один раз — разом, бо для
// IntentPlay намір без Requested нічого не означає.
func (l *Live) takeIntent() (Intent, PlayRequest) {
	if l == nil {
		return IntentNone, PlayRequest{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	intent, requested := l.intent, l.requested
	l.intent, l.requested = IntentNone, PlayRequest{}
	return intent, requested
}

// SetPlaylist публікує список серій для пульта й повертає його покоління.
// Викликається ЛИШЕ з горутини-власника Lib (Update у TUI, послідовний цикл у
// CLI): сюди приходять уже зняті значення, тож HTTP-горутини бібліотеки не
// торкаються. Кожна публікація — нове покоління, починаючи з 1.
func (l *Live) SetPlaylist(ref provider.TitleRef, title string, episodes []EpisodeInfo) int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.playlistSeq++
	l.playlist = Playlist{
		Gen:   l.playlistSeq,
		Ref:   ref,
		Title: title,
		// Копія: слайс лишається в моделі UI і може бути перебудований на місці.
		Episodes: slices.Clone(episodes),
	}
	return l.playlist.Gen
}

// ClearPlaylist прибирає список: показувати серії тайтлу, з якого користувач
// уже пішов, гірше, ніж не показувати нічого.
func (l *Live) ClearPlaylist() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.playlist = Playlist{}
	l.mu.Unlock()
}

// Playlist віддає копію поточного списку. async-safe.
func (l *Live) Playlist() Playlist {
	if l == nil {
		return Playlist{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	pl := l.playlist
	pl.Episodes = slices.Clone(pl.Episodes)
	return pl
}

// CurrentGen — покоління опублікованого списку; 0, якщо списку немає.
func (l *Live) CurrentGen() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.playlist.Gen
}

// RequestPlay — адресний запит «грай серію n зі списку покоління gen». Сам
// нічого не запускає: під час гри закриває сесію з наміром IntentPlay (далі
// спрацьовує звичайний ланцюжок Finish → Result.Requested), а в простої кладе
// запит у скриньку, звідки його забере горутина Update. async-safe.
func (l *Live) RequestPlay(gen, n int) error {
	if l == nil {
		return ErrStalePlaylist
	}
	l.mu.Lock()
	// gen 0 не збігається ніколи: це «списку немає», а не покоління.
	if gen == 0 || gen != l.playlist.Gen {
		l.mu.Unlock()
		return ErrStalePlaylist
	}
	req := PlayRequest{Ref: l.playlist.Ref, Gen: gen, Episode: n}
	sess := l.sess
	if sess == nil {
		ch := l.requestChan()
		l.mu.Unlock()
		pushRequest(ch, req)
		return nil
	}
	// Та сама послідовність, що в end: від'єднуємо сесію під м'ютексом, а
	// закриваємо поза ним, щоб HTTP відповів одразу.
	l.sess = nil
	l.intent, l.requested = IntentPlay, req
	l.mu.Unlock()
	sess.Close()
	return nil
}

// Requests — скринька адресних запитів, що прийшли, поки нічого не грало.
// Канал ніколи не закривається: його читає довгоживуча команда TUI.
func (l *Live) Requests() <-chan PlayRequest {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requestChan()
}

// requestChan створює скриньку на першу потребу; викликач тримає м'ютекс.
func (l *Live) requestChan() chan PlayRequest {
	if l.requests == nil {
		l.requests = make(chan PlayRequest, 1)
	}
	return l.requests
}

// pushRequest кладе запит, витісняючи попередній: якщо ніхто не встиг забрати
// старий тап, значення має лише новий. Ніколи не блокує — інакше HTTP-горутина
// чекала б на горутину Update.
func pushRequest(ch chan PlayRequest, req PlayRequest) {
	select {
	case ch <- req:
	default:
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- req:
		default:
		}
	}
}

// takeStopAfter віддає прапорець рівно один раз: він стосується сесії, що
// щойно завершилася, і не має пережити її в наступну.
func (l *Live) takeStopAfter() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	stopAfter := l.stopAfter
	l.stopAfter = false
	return stopAfter
}
