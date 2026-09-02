package playback

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Basmanjacks/uaanime/internal/player"
)

// Intent — чого попросив веб-пульт замість природного завершення серії.
// Живе окремо від player.EndReason: причина — це «чому вийшов процес плеєра»,
// а намір — бажання користувача, яке викликачі читають після Run.
type Intent int

const (
	IntentNone Intent = iota
	IntentNext
	IntentStop
)

// Snapshot — стан сесії для пульта. Лише значення: у пульта немає доступу
// ні до бібліотеки, ні до самої сесії.
type Snapshot struct {
	Playing     bool
	Title       string
	Episode     int
	PositionSec float64
	DurationSec float64
	Paused      bool
}

// ErrNotPlaying — команда пульта, коли нічого не грає.
var ErrNotPlaying = errors.New("зараз нічого не грає")

// Live — вікно в сесію, що зараз грає, для веб-пульта. Усі методи async-safe
// і безпечні на nil-приймачі (пульт вимкнено → Engine.Live == nil). Live
// НІКОЛИ не торкається Lib: пульт живе на горутинах net/http, а бібліотека —
// лише на горутині Update (правило 10 AGENTS.md).
type Live struct {
	mu      sync.Mutex
	sess    player.Session
	title   string
	episode int
	intent  Intent
}

// Snapshot читає позицію з сесії ПОЗА м'ютексом: RC-обмін VLC може тривати
// до 5 с і не має блокувати set/clear із Run.
func (l *Live) Snapshot() (Snapshot, error) {
	if l == nil {
		return Snapshot{}, nil
	}
	l.mu.Lock()
	sess, title, episode := l.sess, l.title, l.episode
	l.mu.Unlock()
	if sess == nil {
		return Snapshot{}, nil
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
	return Snapshot{
		Playing:     true,
		Title:       title,
		Episode:     episode,
		PositionSec: pos,
		DurationSec: dur,
		Paused:      paused,
	}, nil
}

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
	l.intent = IntentNone
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

// takeIntent віддає намір рівно один раз.
func (l *Live) takeIntent() Intent {
	if l == nil {
		return IntentNone
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	intent := l.intent
	l.intent = IntentNone
	return intent
}
