package download

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/httpx"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// Manager — черга завантажень: одне активне завдання, решта чекає.
//
// Одне активне навмисно. Два паралельні качання ділять той самий канал і
// закінчуються не швидше, зате кожне повзе вдвічі повільніше, а прогрес у
// списку стає незрозумілим. Черга ще й знімає цілий клас гонок: конвеєр пише
// в один .part, і поки він один, порядок операцій із диском очевидний.
//
// Стан менеджера — авторитетний: UI читає Snapshot, а канал Events лише каже
// «щось змінилося». Тому загублений сигнал (канал ємності 1, неблокуючий send)
// нічого не ламає — наступний знімок усе одно покаже правду.
type Manager struct {
	f *httpx.Fetcher

	mu      sync.Mutex
	entries []*entry
	byKey   map[jobKey]*entry
	events  chan struct{}
	wake    chan struct{}
	done    chan struct{}
	started bool
	closed  bool

	nextID atomic.Int64
	once   sync.Once
	wg     sync.WaitGroup
}

type entry struct {
	job  Job
	prog Progress
	// cancel != nil лише поки завдання виконується.
	cancel context.CancelFunc
}

// NewManager збирає менеджер. Горутина-планувальник не стартує, доки не
// з'явиться перше завдання: у більшості сесій нічого не завантажують.
func NewManager(f *httpx.Fetcher) *Manager {
	return &Manager{
		f:      f,
		byKey:  make(map[jobKey]*entry),
		events: make(chan struct{}, 1),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// jobKey ідентифікує запитаний варіант; Name та URL не є ідентичністю тайтлу.
type jobKey struct {
	provider, slug string
	episode        int
	release        provider.Release
	height         int
}

func keyFor(j Job) jobKey {
	return jobKey{j.Ref.Provider, j.Ref.Slug, j.Episode, j.Release, j.Height}
}

// Enqueue ставить варіант серії в чергу. Повторний виклик для того самого
// релізу й запитаної якості, який ще не завершився, повертає наявний id.
func (m *Manager) Enqueue(j Job) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, fmt.Errorf("менеджер завантажень закрито: %w", errs.ErrCancelled)
	}
	key := keyFor(j)
	if e, ok := m.byKey[key]; ok && !e.prog.State.Terminal() {
		return e.prog.ID, nil
	}
	id := m.nextID.Add(1)
	e := &entry{job: j, prog: Progress{
		ID:              id,
		Ref:             j.Ref,
		Episode:         j.Episode,
		Release:         j.Release,
		RequestedHeight: j.Height,
		Height:          j.Height,
		Dir:             j.Dir,
		State:           StateQueued,
	}}
	m.entries = append(m.entries, e)
	m.byKey[key] = e
	if !m.started {
		m.started = true
		m.wg.Add(1)
		go m.loop()
	}
	m.wakeLocked()
	m.signalLocked()
	return id, nil
}

// Cancel зупиняє завдання. Для активного скасування — робота run: він прибере
// .part під локом і сам поставить термінальний стан.
func (m *Manager) Cancel(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.find(id)
	if e == nil || e.prog.State.Terminal() {
		return false
	}
	if e.cancel != nil {
		e.cancel()
		return true
	}
	m.markCancelled(e)
	m.signalLocked()
	return true
}

// Forget прибирає завдання зі знімка. Активне не прибирається: спершу Cancel —
// інакше рядок зник би з екрана, а качання тривало б далі.
func (m *Manager) Forget(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.find(id)
	if e == nil || e.prog.State == StateResolving || e.prog.State == StateRunning {
		return false
	}
	// Позначаємо скасованим ДО видалення: планувальник міг щойно взяти це
	// завдання з черги і перевіряє стан перед стартом — інакше забуте
	// завдання качалося б далі, не показуючись у знімку.
	if e.prog.State == StateQueued {
		m.markCancelled(e)
	}
	for i, x := range m.entries {
		if x == e {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			break
		}
	}
	key := keyFor(e.job)
	if m.byKey[key] == e {
		delete(m.byKey, key)
	}
	m.signalLocked()
	return true
}

// Retry повторює початковий запит зі свіжою резолюцією потоків. Переваги
// перегляду, фактична якість і поточна папка не змінюють завдання.
func (m *Manager) Retry(id int64) (int64, error) {
	m.mu.Lock()
	e := m.find(id)
	if e == nil || (e.prog.State != StateFailed && e.prog.State != StateCancelled) {
		m.mu.Unlock()
		return 0, fmt.Errorf("завдання не можна повторити: %w", errs.ErrCancelled)
	}
	j := e.job
	m.mu.Unlock()
	newID, err := m.Enqueue(j)
	if err == nil {
		m.Forget(id)
	}
	return newID, err
}

// Snapshot — авторитетний стан черги: спершу активне й те, що чекає (у порядку
// постановки), далі завершене цієї сесії. Порядок стабільний, тож курсор UI не
// стрибає між оновленнями.
func (m *Manager) Snapshot() []Progress {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Progress, 0, len(m.entries))
	for _, e := range m.entries {
		if !e.prog.State.Terminal() {
			out = append(out, e.prog)
		}
	}
	for _, e := range m.entries {
		if e.prog.State.Terminal() {
			out = append(out, e.prog)
		}
	}
	return out
}

// Events — коалесований сигнал «щось змінилося». Закривається у Close, тож
// споживач читає `_, ok := <-ch` і на !ok більше не переармовується.
func (m *Manager) Events() <-chan struct{} { return m.events }

// Close скасовує активне завдання, чекає, доки воно приберe за собою, і
// закриває канал подій. Ідемпотентний: його кличуть і модель на виході, і
// defer у Run — обидва шляхи мають бути безпечними.
func (m *Manager) Close() {
	m.once.Do(func() {
		m.mu.Lock()
		m.closed = true
		for _, e := range m.entries {
			switch {
			case e.prog.State.Terminal():
			case e.cancel != nil:
				e.cancel()
			default:
				m.markCancelled(e)
			}
		}
		close(m.done)
		m.mu.Unlock()
		// Чекаємо ПОЗА локом: планувальник дописує фінальний стан під ним.
		m.wg.Wait()
		// Тепер писати в events нікому: signalLocked мовчить при closed, а
		// сам прапорець виставлено під локом до розблокування.
		close(m.events)
	})
}

func (m *Manager) loop() {
	defer m.wg.Done()
	for {
		e := m.take()
		if e == nil {
			select {
			case <-m.wake:
			case <-m.done:
				return
			}
			continue
		}
		m.execute(e)
	}
}

func (m *Manager) take() *entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	for _, e := range m.entries {
		if e.prog.State == StateQueued {
			return e
		}
	}
	return nil
}

func (m *Manager) execute(e *entry) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.mu.Lock()
	if m.closed || e.prog.State != StateQueued {
		// Встигли скасувати, поки завдання чекало черги.
		m.mu.Unlock()
		return
	}
	id := e.prog.ID
	e.cancel = cancel
	m.mu.Unlock()

	final, _ := run(ctx, m.f, e.job, func(p Progress) {
		p.ID = id
		m.mu.Lock()
		e.prog = p
		m.signalLocked()
		m.mu.Unlock()
	})
	final.ID = id

	m.mu.Lock()
	e.prog = final
	e.cancel = nil
	m.signalLocked()
	m.mu.Unlock()
}

func (m *Manager) find(id int64) *entry {
	for _, e := range m.entries {
		if e.prog.ID == id {
			return e
		}
	}
	return nil
}

func (m *Manager) markCancelled(e *entry) {
	e.prog.State = StateCancelled
	e.prog.Err = fmt.Errorf("серія %d: %w", e.prog.Episode, errs.ErrCancelled)
	e.prog.BytesPerSec, e.prog.ETA = 0, 0
}

// signalLocked і wakeLocked шлють неблокуюче: канали ємності 1, і другий
// сигнал у непрочитаний канал нічого не додає — стан усе одно читають зі
// Snapshot. Кличуться тільки під m.mu, і саме це робить close(events) у Close
// безпечним.
func (m *Manager) signalLocked() {
	if m.closed {
		return
	}
	select {
	case m.events <- struct{}{}:
	default:
	}
}

func (m *Manager) wakeLocked() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
