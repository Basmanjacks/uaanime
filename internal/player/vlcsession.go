package player

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

var (
	vlcRequestTimeout = 5 * time.Second
	vlcAttemptTimeout = 700 * time.Millisecond
)

const (
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

var _ Session = (*vlcSession)(nil)

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

	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return 0, err
	}

	deadline := time.Now().Add(vlcRequestTimeout)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	drainLocked(conn, reader)

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
			valueText := stripVLCPrompts(line)
			value, err := strconv.Atoi(valueText)
			if err == nil {
				return float64(value), nil
			}
		}
	}
	return 0, fmt.Errorf("VLC RC: %s: значення недоступне: %w: %w", command, context.DeadlineExceeded, errs.ErrPlayer)
}

func (s *vlcSession) snapshotLocked() (net.Conn, *bufio.Reader, error) {
	s.stateMu.Lock()
	conn := s.conn
	reader := s.reader
	s.stateMu.Unlock()
	if conn == nil || reader == nil {
		return nil, nil, fmt.Errorf("VLC RC: сесію закрито: %w", errs.ErrPlayer)
	}
	return conn, reader, nil
}

func drainLocked(conn net.Conn, reader *bufio.Reader) {
	// Дренаж: повторні надсилання можуть лишити в буфері відкладені відповіді
	// попередніх команд — без очистки їх прочитав би наступний запит як свої.
	_ = conn.SetDeadline(time.Now().Add(time.Millisecond))
	for {
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
	}
}

func stripVLCPrompts(line string) string {
	valueText := strings.TrimSpace(line)
	// Після повторів промпти накопичуються: "> > 5".
	for strings.HasPrefix(valueText, ">") {
		valueText = strings.TrimSpace(strings.TrimPrefix(valueText, ">"))
	}
	return valueText
}

// sendLocked не чекає рядка відповіді, бо pause і seek друкують лише промпт
// без переводу рядка; очікування ReadString тут зависло б до дедлайну
// (перевірено VLC 3.0.17.3, 2026-09-02).
func sendLocked(conn net.Conn, reader *bufio.Reader, command string) error {
	drainLocked(conn, reader)
	if err := conn.SetDeadline(time.Now().Add(vlcAttemptTimeout)); err != nil {
		return fmt.Errorf("VLC RC: дедлайн: %w: %w", err, errs.ErrPlayer)
	}
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return fmt.Errorf("VLC RC: запис: %w: %w", err, errs.ErrPlayer)
	}
	return nil
}

// statusLocked читає багаторядковий status лише до рядка стану: наступного
// маркера завершення RC не дає, а до старту відтворення може мовчати зовсім
// (перевірено VLC 3.0.17.3, 2026-09-02).
func statusLocked(conn net.Conn, reader *bufio.Reader) (bool, error) {
	drainLocked(conn, reader)
	deadline := time.Now().Add(vlcRequestTimeout)
	for time.Now().Before(deadline) {
		attemptEnd := time.Now().Add(vlcAttemptTimeout)
		if attemptEnd.After(deadline) {
			attemptEnd = deadline
		}
		if err := conn.SetDeadline(attemptEnd); err != nil {
			return false, fmt.Errorf("VLC RC: дедлайн: %w: %w", err, errs.ErrPlayer)
		}
		if _, err := fmt.Fprintln(conn, "status"); err != nil {
			return false, fmt.Errorf("VLC RC: запис: %w: %w", err, errs.ErrPlayer)
		}
		for {
			line, err := reader.ReadString('\n')
			valueText := stripVLCPrompts(line)
			if strings.Contains(valueText, "( state ") {
				return strings.Contains(valueText, "( state paused )"), nil
			}
			if err != nil {
				var netErr net.Error
				if errors.As(err, &netErr) && netErr.Timeout() {
					break // порожня відповідь — повторюємо команду
				}
				return false, fmt.Errorf("VLC RC: читання: %w: %w", err, errs.ErrPlayer)
			}
		}
	}
	return false, fmt.Errorf("VLC RC: status: значення недоступне: %w: %w", context.DeadlineExceeded, errs.ErrPlayer)
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

func (s *vlcSession) TogglePause() error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	return sendLocked(conn, reader, "pause")
}

func (s *vlcSession) Paused() (bool, error) {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	return statusLocked(conn, reader)
}

func (s *vlcSession) Seek(deltaSec float64) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	// RC сам трактує знак як відносний seek і обмежує від'ємний результат нулем,
	// тож додатковий get_time створив би зайву точку відмови
	// (перевірено VLC 3.0.17.3, 2026-09-02).
	command := fmt.Sprintf("seek %+d", int64(math.Round(deltaSec)))
	return sendLocked(conn, reader, command)
}

func (s *vlcSession) SeekTo(posSec float64) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	// Число БЕЗ знака — абсолютна позиція; знак у тій самій команді означав би
	// відносний seek (див. Seek вище). Від'ємну ціль RC сам обмежує нулем.
	pos := int64(math.Round(posSec))
	if pos < 0 {
		pos = 0
	}
	return sendLocked(conn, reader, fmt.Sprintf("seek %d", pos))
}

// vlcVolumeScale — RC VLC працює в сирій шкалі, де 256 = 100 %. Значення понад
// 256 законні (VLC дозволяє підсилення), тож нормалізація не обрізає стелю.
const vlcVolumeScale = 2.56

// Volume: "volume" без аргументу друкує ціле сире значення окремим рядком —
// такий самий формат, як get_time (перевірено VLC 3.0.17.3, 2026-09-03).
func (s *vlcSession) Volume() (float64, error) {
	raw, err := s.request("volume")
	if err != nil {
		return 0, err
	}
	return raw / vlcVolumeScale, nil
}

func (s *vlcSession) SetVolume(pct float64) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	conn, reader, err := s.snapshotLocked()
	if err != nil {
		return err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	// "volume <raw>" відповіді не друкує, лише промпт — як pause і seek.
	raw := int64(math.Round(pct * vlcVolumeScale))
	return sendLocked(conn, reader, fmt.Sprintf("volume %d", raw))
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
