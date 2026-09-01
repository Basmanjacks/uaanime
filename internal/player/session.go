package player

import (
	"bufio"
	"context"
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

// mpvSession — запущений mpv під контролем через JSON IPC.
// Шлях сокета не закладає POSIX в API: на Windows це буде named pipe (поза v1).
type mpvSession struct {
	cmd  *exec.Cmd
	sock string

	mu        sync.Mutex
	conn      net.Conn
	nextID    int
	pending   map[int]chan ipcResponse
	readerErr error

	readerDone chan struct{}
	end        chan EndReason
	endOnce    sync.Once
}

func startMPV(streamURL, mediaTitle string, headers map[string]string, startSec float64, extraArgs ...string) (*mpvSession, error) {
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("uaanime-mpv-%d-%d.sock", os.Getpid(), time.Now().UnixNano()))
	base := (MPV{}).Command(streamURL, mediaTitle, headers, startSec)
	args := append([]string{"--input-ipc-server=" + sock}, base.Args[1:]...)
	// Службові аргументи мають стояти перед URL, який mpv очікує останнім.
	args = append(args[:len(args)-1], append(append([]string{}, extraArgs...), streamURL)...)

	cmd := exec.Command(base.Path, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv: %w", err)
	}

	// mpv створює сокет після старту; чекаємо до 10 с.
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
	return newMPVSession(cmd, sock, conn), nil
}

func newMPVSession(cmd *exec.Cmd, sock string, conn net.Conn) *mpvSession {
	s := &mpvSession{
		cmd:        cmd,
		sock:       sock,
		conn:       conn,
		pending:    make(map[int]chan ipcResponse),
		readerDone: make(chan struct{}),
		end:        make(chan EndReason, 1),
	}
	go s.readLoop()
	return s
}

type ipcResponse struct {
	Error     string          `json:"error"`
	Data      json.RawMessage `json:"data"`
	RequestID int             `json:"request_id"`
	Event     string          `json:"event"`
	Reason    string          `json:"reason"`
}

// readLoop є єдиним читачем сокета: події обробляє одразу, а відповіді
// передає запиту з відповідним request_id.
func (s *mpvSession) readLoop() {
	reader := bufio.NewReader(s.conn)
	defer close(s.readerDone)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			s.mu.Lock()
			s.readerErr = err
			s.mu.Unlock()
			return
		}
		var response ipcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			continue // Пошкоджений рядок від зовнішнього процесу не має панікувати.
		}
		if response.Event != "" {
			s.handleEvent(response)
			continue
		}
		s.mu.Lock()
		responseCh := s.pending[response.RequestID]
		if responseCh != nil {
			select {
			case responseCh <- response:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func (s *mpvSession) request(command ...any) (json.RawMessage, error) {
	s.mu.Lock()
	if s.conn == nil {
		s.mu.Unlock()
		return nil, errors.New("mpv IPC: сесію закрито")
	}
	s.nextID++
	id := s.nextID
	response := make(chan ipcResponse, 1)
	s.pending[id] = response
	conn := s.conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	req, err := json.Marshal(map[string]any{"command": command, "request_id": id})
	if err != nil {
		return nil, fmt.Errorf("mpv IPC: побудова запиту: %w", err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("mpv IPC: запис: %w", err)
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case result := <-response:
		if result.Error != "success" {
			return nil, fmt.Errorf("mpv IPC: %s", result.Error)
		}
		return result.Data, nil
	case <-s.readerDone:
		return nil, s.readerFailure()
	case <-timer.C:
		return nil, fmt.Errorf("mpv IPC: тайм-аут запиту: %w", context.DeadlineExceeded)
	}
}

func (s *mpvSession) readerFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readerErr == nil {
		return errors.New("mpv IPC: читач зупинився")
	}
	return fmt.Errorf("mpv IPC: читання: %w", s.readerErr)
}

func (s *mpvSession) handleEvent(response ipcResponse) {
	if response.Event != "end-file" {
		return
	}
	reason := EndError
	switch response.Reason {
	case "eof":
		reason = EndEOF
	case "quit", "stop":
		reason = EndQuit
	}
	s.endOnce.Do(func() { s.end <- reason })
}

// floatProperty: mpv віддає time-pos/duration як число або як помилку,
// поки властивість ще недоступна (буферизація).
func (s *mpvSession) floatProperty(name string) (float64, error) {
	data, err := s.request("get_property", name)
	if err != nil {
		return 0, err
	}
	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return 0, fmt.Errorf("mpv IPC: %s: %w", name, err)
	}
	return value, nil
}

func (s *mpvSession) TimePos() (float64, error)  { return s.floatProperty("time-pos") }
func (s *mpvSession) Duration() (float64, error) { return s.floatProperty("duration") }

// End повертає канал, що отримає причину завершення відтворення.
func (s *mpvSession) End() <-chan EndReason { return s.end }

// Wait чекає завершення mpv. Після чистого виходу читач має коротке вікно,
// щоб доставити вже записану в сокет подію end-file до запасного EndQuit.
func (s *mpvSession) Wait() error {
	if s.cmd == nil {
		return errors.New("mpv: процес не запущено")
	}
	err := s.cmd.Wait()
	if err != nil {
		s.endOnce.Do(func() { s.end <- EndError })
		return err
	}
	select {
	case <-s.readerDone:
	case <-time.After(100 * time.Millisecond):
	}
	s.endOnce.Do(func() { s.end <- EndQuit })
	return nil
}

// Close прибирає сесію: закриває сокет, зупиняє mpv, видаляє файл сокета.
func (s *mpvSession) Close() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	_ = os.Remove(s.sock)
}
