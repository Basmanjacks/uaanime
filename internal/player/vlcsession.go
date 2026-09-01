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
)

const (
	vlcRequestTimeout = 5 * time.Second
	vlcAttemptTimeout = 700 * time.Millisecond
	vlcPollInterval   = time.Second
	// Бібліотека позначає серію завершеною на 90%; для непрямого EOF від VLC
	// беремо консервативніші 95%, щоб ранній вихід не спричинив автоперехід.
	vlcEOFThreshold = 0.95
)

// vlcSession — запущений VLC під контролем через текстовий RC IPC.
type vlcSession struct {
	cmd *exec.Cmd

	requestMu sync.Mutex
	stateMu   sync.Mutex
	conn      net.Conn
	reader    *bufio.Reader

	cacheMu sync.RWMutex
	lastPos float64
	lastDur float64
	hasPos  bool
	hasDur  bool
	hasPair bool

	stopPoll chan struct{}
	stopOnce sync.Once
	end      chan EndReason
	endOnce  sync.Once
}

func newVLCSession(cmd *exec.Cmd, conn net.Conn) *vlcSession {
	s := &vlcSession{
		cmd:      cmd,
		conn:     conn,
		reader:   bufio.NewReader(conn),
		stopPoll: make(chan struct{}),
		end:      make(chan EndReason, 1),
	}
	go s.pollLoop()
	return s
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
		return 0, errors.New("VLC RC: сесію закрито")
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
			return 0, fmt.Errorf("VLC RC: дедлайн: %w", err)
		}
		if _, err := fmt.Fprintln(conn, command); err != nil {
			return 0, fmt.Errorf("VLC RC: запис: %w", err)
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break // порожня відповідь — повторюємо команду
				}
				return 0, fmt.Errorf("VLC RC: читання: %w", err)
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
	return 0, fmt.Errorf("VLC RC: %s: значення недоступне: %w", command, context.DeadlineExceeded)
}

func (s *vlcSession) pollLoop() {
	ticker := time.NewTicker(vlcPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopPoll:
			return
		case <-ticker.C:
			pos, err := s.request("get_time")
			if err != nil {
				return
			}
			dur, err := s.request("get_length")
			if err != nil {
				return
			}
			s.cacheSample(pos, dur)
		}
	}
}

func (s *vlcSession) cacheSample(pos, dur float64) {
	s.cacheMu.Lock()
	s.lastPos = pos
	s.lastDur = dur
	s.hasPos = true
	s.hasDur = true
	s.hasPair = true
	s.cacheMu.Unlock()
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

func (s *vlcSession) End() <-chan EndReason { return s.end }

func (s *vlcSession) endReasonOnCleanExit() EndReason {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.hasPair && s.lastDur > 0 && s.lastPos/s.lastDur >= vlcEOFThreshold {
		return EndEOF
	}
	return EndQuit
}

func (s *vlcSession) Wait() error {
	if s.cmd == nil {
		err := errors.New("vlc: процес не запущено")
		s.endOnce.Do(func() { s.end <- EndError })
		return err
	}
	if err := s.cmd.Wait(); err != nil {
		s.endOnce.Do(func() { s.end <- EndError })
		return err
	}
	s.endOnce.Do(func() { s.end <- s.endReasonOnCleanExit() })
	return nil
}

// Close прибирає сесію: зупиняє опитування, закриває TCP і процес VLC.
func (s *vlcSession) Close() {
	s.stopOnce.Do(func() { close(s.stopPoll) })
	s.stateMu.Lock()
	conn := s.conn
	s.conn = nil
	s.reader = nil
	s.stateMu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
}
