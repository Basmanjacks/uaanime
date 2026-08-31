package player

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// EndReason — чому закінчилося відтворення. Це різні сценарії для UI:
// eof → серія переглянута, quit → користувач вийшов, error → проблема з потоком.
type EndReason string

const (
	EndEOF   EndReason = "eof"
	EndQuit  EndReason = "quit"
	EndError EndReason = "error"
)

// Session — запущений mpv під контролем через JSON IPC.
// Шлях сокета не закладає POSIX в API: на Windows це буде named pipe (поза v1).
type Session struct {
	cmd  *exec.Cmd
	sock string

	mu     sync.Mutex
	conn   net.Conn
	nextID int
	// відповіді на запити приходять упереміш із подіями, тому читач один
	reader *bufio.Reader

	end     chan EndReason
	endOnce sync.Once
}

// Start запускає mpv і чекає готовності IPC-сокета. extraArgs — службові
// аргументи для тестів (--vo=null тощо), продуктовий код їх не передає.
func Start(streamURL, mediaTitle string, headers map[string]string, startSec float64, extraArgs ...string) (*Session, error) {
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("uaanime-mpv-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	base := MPVCommand(streamURL, mediaTitle, headers, startSec)
	args := append([]string{"--input-ipc-server=" + sock}, base.Args[1:]...)
	// extraArgs перед URL (останнім аргументом)
	args = append(args[:len(args)-1], append(append([]string{}, extraArgs...), streamURL)...)

	cmd := exec.Command("mpv", args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv: %w", err)
	}
	s := &Session{cmd: cmd, sock: sock, end: make(chan EndReason, 1)}

	// mpv створює сокет після старту; чекаємо до 10 с
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("mpv IPC: сокет не з'явився: %w", err)
	}
	s.conn = conn
	s.reader = bufio.NewReader(conn)
	return s, nil
}

type ipcResponse struct {
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	RequestID int             `json:"request_id"`
	Event     string          `json:"event"`
	Reason    string          `json:"reason"`
}

// request виконує один IPC-запит. Події end-file приходять без підписки і
// обробляються дорогою: окремого читача немає, тому стан кінця фіксується
// під час регулярних опитувань позиції.
func (s *Session) request(cmd ...any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil, errors.New("mpv IPC: сесію закрито")
	}
	s.nextID++
	id := s.nextID
	req, err := json.Marshal(map[string]any{"command": cmd, "request_id": id})
	if err != nil {
		return nil, err
	}
	if err := s.conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	if _, err := s.conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("mpv IPC: %w", err)
	}
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("mpv IPC: %w", err)
		}
		var res ipcResponse
		if json.Unmarshal(line, &res) != nil {
			continue // невалідний рядок — пропускаємо, не панікуємо
		}
		if res.Event != "" {
			s.handleEvent(res)
			continue
		}
		if res.RequestID != id {
			continue
		}
		if res.Error != "success" {
			return nil, fmt.Errorf("mpv IPC: %s", res.Error)
		}
		return res.Data, nil
	}
}

func (s *Session) handleEvent(res ipcResponse) {
	if res.Event != "end-file" {
		return
	}
	reason := EndError
	switch res.Reason {
	case "eof":
		reason = EndEOF
	case "quit", "stop":
		reason = EndQuit
	}
	s.endOnce.Do(func() { s.end <- reason })
}

// floatProperty: mpv віддає time-pos/duration як число або як помилку,
// поки властивість ще недоступна (буферизація).
func (s *Session) floatProperty(name string) (float64, error) {
	data, err := s.request("get_property", name)
	if err != nil {
		return 0, err
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, fmt.Errorf("mpv IPC: %s: %w", name, err)
	}
	return v, nil
}

func (s *Session) TimePos() (float64, error)  { return s.floatProperty("time-pos") }
func (s *Session) Duration() (float64, error) { return s.floatProperty("duration") }

// End повертає канал, що отримає причину завершення відтворення.
// Процес mpv, що зник без події, теж має розбудити читача — це робить Wait.
func (s *Session) End() <-chan EndReason { return s.end }

// Wait чекає завершення процесу mpv і гарантує, що End розбуджено.
func (s *Session) Wait() error {
	err := s.cmd.Wait()
	if err != nil {
		s.endOnce.Do(func() { s.end <- EndError })
	} else {
		s.endOnce.Do(func() { s.end <- EndQuit })
	}
	return err
}

// Close прибирає сесію: закриває сокет, зупиняє mpv, видаляє файл сокета.
func (s *Session) Close() {
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
	s.mu.Unlock()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	_ = os.Remove(s.sock)
}
