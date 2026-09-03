// Package playertest — керований фейк зовнішнього плеєра для наскрізних тестів
// (cmd, ui, playback). Реальні VLC/mpv у тестах не запускаються: сесія
// відтворює заздалегідь задані позиції, зберігає команди керування й
// завершується вказаною причиною.
package playertest

import (
	"context"
	"os/exec"
	"sync"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/player"
)

// Start — аргументи одного виклику Player.Start; тести звіряють, що плеєр
// отримав саме той потік, заголовки та resume-позицію, що їх обрав рушій.
type Start struct {
	URL        string
	MediaTitle string
	Headers    map[string]string
	StartSec   float64
}

// Call — одна команда керування, записана сесією для перевірки тестом.
type Call struct {
	Op    string
	Delta float64
}

// Player видає сесії по черзі: кожен Start бере наступну з Sessions.
// Вичерпані сесії — errs.ErrPlayer, як у справжнього плеєра, що не піднявся.
type Player struct {
	IDValue  string
	Sessions []*Session

	mu     sync.Mutex
	starts []Start
	next   int
}

func (p *Player) ID() string {
	if p.IDValue == "" {
		return "fake"
	}
	return p.IDValue
}

// Command детермінований: --dry-run має що надрукувати й без реального плеєра.
func (p *Player) Command(streamURL, _ string, _ map[string]string, _ float64) *exec.Cmd {
	return exec.Command("fake-player", streamURL)
}

func (p *Player) Start(_ context.Context, streamURL, mediaTitle string, headers map[string]string, startSec float64) (player.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.starts = append(p.starts, Start{URL: streamURL, MediaTitle: mediaTitle, Headers: headers, StartSec: startSec})
	if p.next >= len(p.Sessions) {
		return nil, errs.ErrPlayer
	}
	s := p.Sessions[p.next]
	p.next++
	s.start()
	return s, nil
}

// Starts — копія записаних викликів Start (безпечна для читання з тесту,
// поки сесія ще працює у фоновій горутині).
func (p *Player) Starts() []Start {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Start, len(p.starts))
	copy(out, p.starts)
	return out
}

// Session відтворює позиції по черзі (остання повторюється) і після того, як
// усі прочитано, рівно один раз надсилає Reason у End() — так Engine.Run встигає
// записати журнал до завершення. Сесія з Reason=EndEOF мусить мати ≥1 позицію:
// Finish ставить Completed лише при наявному записі прогресу.
type Session struct {
	Reason    player.EndReason
	Positions []float64
	Durations []float64
	// Hold затримує завершення: Reason не надсилається, доки тест не викличе
	// Release (або рушій — Close). Потрібен там, де сесію має обірвати сигнал.
	Hold bool

	// Started закривається у Start, Sampled — після першого TimePos: тест
	// синхронізується з фоновою командою без sleep.
	Started chan struct{}
	Sampled chan struct{}

	mu       sync.Mutex
	calls    []Call
	paused   bool
	end      chan player.EndReason
	posIdx   int
	durIdx   int
	endOnce  sync.Once
	sampOnce sync.Once
	startOne sync.Once
}

var _ player.Session = (*Session)(nil)

// NewSession — сесія з ініціалізованими каналами; Reason/Positions/Durations
// можна редагувати до Start.
func NewSession(reason player.EndReason, positions, durations []float64) *Session {
	return &Session{
		Reason:    reason,
		Positions: positions,
		Durations: durations,
		Started:   make(chan struct{}),
		Sampled:   make(chan struct{}),
		end:       make(chan player.EndReason, 1),
	}
}

func (s *Session) start() {
	s.startOne.Do(func() {
		close(s.Started)
		if len(s.Positions) == 0 {
			s.finish()
		}
	})
}

func (s *Session) finish() {
	if s.Hold {
		return
	}
	s.Release()
}

// Release надсилає Reason (один раз) і закриває канал End.
func (s *Session) Release() {
	s.endOnce.Do(func() {
		s.end <- s.Reason
		close(s.end)
	})
}

func (s *Session) TimePos() (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Positions) == 0 {
		return 0, nil
	}
	idx := min(s.posIdx, len(s.Positions)-1)
	s.posIdx++
	s.sampOnce.Do(func() { close(s.Sampled) })
	if s.posIdx >= len(s.Positions) {
		s.finish()
	}
	return s.Positions[idx], nil
}

func (s *Session) Duration() (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Durations) == 0 {
		return 0, nil
	}
	idx := min(s.durIdx, len(s.Durations)-1)
	s.durIdx++
	return s.Durations[idx], nil
}

func (s *Session) TogglePause() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Op: "pause"})
	s.paused = !s.paused
	return nil
}

func (s *Session) Paused() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused, nil
}

func (s *Session) Seek(deltaSec float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Op: "seek", Delta: deltaSec})
	return nil
}

func (s *Session) SeekTo(posSec float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, Call{Op: "seekto", Delta: posSec})
	return nil
}

// Calls повертає копію, щоб асинхронний рушій не міг змінити зріз під час
// перевірки тестом.
func (s *Session) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Session) End() <-chan player.EndReason { return s.end }
func (s *Session) Wait() error                  { return nil }

// Close ідемпотентний і звільняє утримувану сесію: Engine.Run кличе його
// у defer і при скасуванні контексту.
func (s *Session) Close() { s.Release() }
