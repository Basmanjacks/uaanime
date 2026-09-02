package player

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// EndReason — чому закінчилося відтворення. Це різні сценарії для UI:
// eof → серія переглянута, quit → користувач вийшов, error → проблема з потоком.
type EndReason string

const (
	EndEOF   EndReason = "eof"
	EndQuit  EndReason = "quit"
	EndError EndReason = "error"
)

// mpvEventGrace — скільки чекати на читача після чистого виходу mpv: подія
// end-file уже може лежати в сокеті, і вона точніша за запасний EndQuit.
const mpvEventGrace = 100 * time.Millisecond

// mpvTestArgs — тестовий хук: інтеграційні тести додають сюди --no-config
// та null-виводи. У продакшн-сигнатурі таких аргументів немає.
var mpvTestArgs []string

// mpvSession — запущений mpv під контролем через JSON IPC.
// Шлях сокета не закладає POSIX в API: на Windows це буде named pipe (поза v1).
type mpvSession struct {
	*process
	dir string // приватний каталог сокета, видаляється в Close

	mu        sync.Mutex
	conn      net.Conn
	nextID    int
	pending   map[int]chan ipcResponse
	readerErr error

	readerDone chan struct{}
}

var _ Session = (*mpvSession)(nil)

func startMPV(ctx context.Context, streamURL, mediaTitle string, headers map[string]string, startSec float64) (*mpvSession, error) {
	// Сокет живе у власному каталозі 0700: у спільному /tmp будь-який локальний
	// користувач міг би під'єднатися до IPC і надіслати mpv команду run.
	dir, err := os.MkdirTemp("", "uaanime-mpv-")
	if err != nil {
		return nil, fmt.Errorf("mpv: тимчасовий каталог: %w: %w", err, errs.ErrPlayer)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()
	sock := filepath.Join(dir, "mpv.sock")

	args := append([]string{"--input-ipc-server=" + sock}, mpvArgs(mediaTitle, headers, startSec)...)
	args = append(args, mpvTestArgs...)
	cmd := exec.Command("mpv", append(args, streamURL)...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mpv: %w: %w", err, errs.ErrPlayer)
	}

	// Сесія (а з нею і жнець процесу) існує до першої спроби з'єднання:
	// інакше невдалий dial не мав би кому віддати вбитий процес.
	s := newMPVSession(cmd, dir)
	conn, err := dialRetry(ctx, s.done, "unix", sock)
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("mpv IPC: %w", err)
	}
	s.attachIPC(conn)
	ok = true
	return s, nil
}

func newMPVSession(cmd *exec.Cmd, dir string) *mpvSession {
	s := &mpvSession{
		dir:        dir,
		pending:    make(map[int]chan ipcResponse),
		readerDone: make(chan struct{}),
	}
	s.process = newProcess(cmd, s.classifyExit)
	return s
}

// attachIPC вмикає читача сокета; до цього моменту сесія лише тримає процес.
func (s *mpvSession) attachIPC(conn net.Conn) {
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	go s.readLoop()
}

// classifyExit — причина завершення за виходом процесу. Після чистого виходу
// читач має коротке вікно, щоб доставити вже записану подію end-file: вона
// публікується через той самий endOnce і має пріоритет над запасним EndQuit.
func (s *mpvSession) classifyExit(waitErr error, closing bool) EndReason {
	if closing {
		return EndQuit
	}
	select {
	case <-s.readerDone:
	case <-time.After(mpvEventGrace):
	}
	if waitErr != nil {
		return EndError
	}
	return EndQuit
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
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	reader := bufio.NewReader(conn)
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
		return nil, fmt.Errorf("mpv IPC: сесію закрито: %w", errs.ErrPlayer)
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
		return nil, fmt.Errorf("mpv IPC: побудова запиту: %w: %w", err, errs.ErrPlayer)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("mpv IPC: запис: %w: %w", err, errs.ErrPlayer)
	}

	return s.await(response, 5*time.Second)
}

// await чекає на відповідь. Відповідь могла прийти тим самим рядком, після
// якого сокет закрився (mpv відповідає і виходить): тоді обидві гілки select
// готові разом, а Go обирає випадково — тому вже доставлена відповідь має
// пріоритет над «читач зупинився».
func (s *mpvSession) await(response <-chan ipcResponse, timeout time.Duration) (json.RawMessage, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-response:
		return unpackResponse(result)
	case <-s.readerDone:
		select {
		case result := <-response:
			return unpackResponse(result)
		default:
		}
		return nil, s.readerFailure()
	case <-timer.C:
		return nil, fmt.Errorf("mpv IPC: тайм-аут запиту: %w: %w", context.DeadlineExceeded, errs.ErrPlayer)
	}
}

func unpackResponse(result ipcResponse) (json.RawMessage, error) {
	if result.Error != "success" {
		return nil, fmt.Errorf("mpv IPC: %s: %w", result.Error, errs.ErrPlayer)
	}
	return result.Data, nil
}

func (s *mpvSession) readerFailure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readerErr == nil {
		return fmt.Errorf("mpv IPC: читач зупинився: %w", errs.ErrPlayer)
	}
	return fmt.Errorf("mpv IPC: читання: %w: %w", s.readerErr, errs.ErrPlayer)
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
	s.publish(reason)
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
		return 0, fmt.Errorf("mpv IPC: %s: %w: %w", name, err, errs.ErrPlayer)
	}
	return value, nil
}

func (s *mpvSession) TimePos() (float64, error)  { return s.floatProperty("time-pos") }
func (s *mpvSession) Duration() (float64, error) { return s.floatProperty("duration") }

func (s *mpvSession) TogglePause() error {
	_, err := s.request("cycle", "pause")
	return err
}

func (s *mpvSession) Paused() (bool, error) {
	data, err := s.request("get_property", "pause")
	if err != nil {
		return false, err
	}
	var paused bool
	if err := json.Unmarshal(data, &paused); err != nil {
		return false, fmt.Errorf("mpv IPC: pause: %w: %w", err, errs.ErrPlayer)
	}
	return paused, nil
}

func (s *mpvSession) Seek(deltaSec float64) error {
	_, err := s.request("seek", deltaSec, "relative")
	return err
}

// Close прибирає сесію: закриває сокет (це зупиняє читача), зупиняє mpv і
// видаляє каталог сокета.
func (s *mpvSession) Close() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	s.process.Close()
	_ = os.RemoveAll(s.dir)
}
