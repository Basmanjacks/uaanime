package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

const (
	vlcRequestTimeout = 5 * time.Second
	vlcAttemptTimeout = 700 * time.Millisecond
	// Бібліотека позначає серію завершеною на 90%; для непрямого EOF від VLC
	// беремо консервативніші 95%, щоб ранній вихід не спричинив автоперехід.
	vlcEOFThreshold = 0.95
)

// vlcSession — запущений VLC під контролем через текстовий RC IPC.
type vlcSession struct {
	*process

	requestMu sync.Mutex
	stateMu   sync.Mutex
	conn      net.Conn
	reader    *bufio.Reader

	// Кеш останніх виміряних значень: RC відповідає не завжди (до старту
	// відтворення get_time мовчить), а причина завершення рахується вже після
	// виходу VLC, коли питати нема кого.
	cacheMu sync.RWMutex
	lastPos float64
	lastDur float64
	hasPos  bool
	hasDur  bool
}

func newVLCSession(cmd *exec.Cmd) *vlcSession {
	s := &vlcSession{}
	s.process = newProcess(cmd, s.classifyExit)
	return s
}

// attachRC вмикає RC-канал; до цього моменту сесія лише тримає процес.
func (s *vlcSession) attachRC(conn net.Conn) {
	s.stateMu.Lock()
	s.conn = conn
	s.reader = bufio.NewReader(conn)
	s.stateMu.Unlock()
}

// classifyExit — причина завершення за виходом процесу. VLC не повідомляє EOF
// через RC, тому чистий вихід розрізняється за останнім виміром позиції.
func (s *vlcSession) classifyExit(waitErr error, closing bool) EndReason {
	switch {
	case closing:
		return EndQuit
	case waitErr != nil:
		return EndError
	default:
		return s.endReasonOnCleanExit()
	}
}

// request пропускає службовий шум RC, доки не отримає окремий цілий рядок.
// Особливість протоколу (звірено по байтах 2026-08-31): відповідь має вигляд
// "> <число>\r\n", але ПОРОЖНЯ відповідь (get_time до старту відтворення) не
// друкує нічого — лише промпт "> " без переводу рядка. Тому читання одним
// довгим дедлайном зависає: рядок ніколи не завершиться. Натомість — короткі
// спроби з повторним надсиланням команди, доки триває загальний бюджет.
func (s *vlcSession) request(command string) (float64, error) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()

	s.stateMu.Lock()
	conn := s.conn
	reader := s.reader
	s.stateMu.Unlock()
	if conn == nil || reader == nil {
		return 0, fmt.Errorf("VLC RC: сесію закрито: %w", errs.ErrPlayer)
	}

	deadline := time.Now().Add(vlcRequestTimeout)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	// Дренаж: повторні надсилання можуть лишити в буфері відкладені відповіді
	// попередніх команд — без очистки їх прочитав би наступний запит як свої.
	_ = conn.SetDeadline(time.Now().Add(time.Millisecond))
	for {
		if _, err := reader.ReadString('\n'); err != nil {
			break
		}
	}

	for time.Now().Before(deadline) {
		attemptEnd := time.Now().Add(vlcAttemptTimeout)
		if attemptEnd.After(deadline) {
			attemptEnd = deadline
		}
		if err := conn.SetDeadline(attemptEnd); err != nil {
			return 0, fmt.Errorf("VLC RC: дедлайн: %w: %w", err, errs.ErrPlayer)
		}
		if _, err := fmt.Fprintln(conn, command); err != nil {
			return 0, fmt.Errorf("VLC RC: запис: %w: %w", err, errs.ErrPlayer)
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break // порожня відповідь — повторюємо команду
				}
				return 0, fmt.Errorf("VLC RC: читання: %w: %w", err, errs.ErrPlayer)
			}
			valueText := strings.TrimSpace(line)
			// після повторів промпти накопичуються: "> > 5"
			for strings.HasPrefix(valueText, ">") {
				valueText = strings.TrimSpace(strings.TrimPrefix(valueText, ">"))
			}
			value, err := strconv.Atoi(valueText)
			if err == nil {
				return float64(value), nil
			}
		}
	}
	return 0, fmt.Errorf("VLC RC: %s: значення недоступне: %w: %w", command, context.DeadlineExceeded, errs.ErrPlayer)
}

func (s *vlcSession) cachePosition(pos float64) {
	s.cacheMu.Lock()
	s.lastPos = pos
	s.hasPos = true
	s.cacheMu.Unlock()
}

func (s *vlcSession) cacheDuration(dur float64) {
	s.cacheMu.Lock()
	s.lastDur = dur
	s.hasDur = true
	s.cacheMu.Unlock()
}

func (s *vlcSession) TimePos() (float64, error) {
	value, err := s.request("get_time")
	if err == nil {
		s.cachePosition(value)
		return value, nil
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.hasPos {
		return s.lastPos, nil
	}
	return 0, err
}

func (s *vlcSession) Duration() (float64, error) {
	value, err := s.request("get_length")
	if err == nil {
		s.cacheDuration(value)
		return value, nil
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.hasDur {
		return s.lastDur, nil
	}
	return 0, err
}

// endReasonOnCleanExit: VLC із --play-and-exit виходить і після кінця файла, і
// після Ctrl+Q, тому EOF відновлюється з останніх виміряних pos/dur. Виміри
// приходять із тика двигуна (5 с) — для 95 %-евристики цього достатньо.
// Окремий pollLoop прибрано: він назавжди виходив з першої ж порожньої
// відповіді get_time до старту відтворення, тож EOF не детектувався взагалі.
func (s *vlcSession) endReasonOnCleanExit() EndReason {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.hasPos && s.hasDur && s.lastDur > 0 && s.lastPos/s.lastDur >= vlcEOFThreshold {
		return EndEOF
	}
	return EndQuit
}

// Close прибирає сесію: закриває RC-канал і зупиняє процес VLC.
func (s *vlcSession) Close() {
	s.stateMu.Lock()
	conn := s.conn
	s.conn = nil
	s.reader = nil
	s.stateMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	s.process.Close()
}
