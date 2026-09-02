package player

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// process — спільний життєвий цикл дочірнього плеєра (mpv і VLC).
//
// Єдиний жнець: cmd.Wait() викликає ЛИШЕ горутина, створена в newProcess.
// Раніше Wait і Close змагалися за Cmd.Wait/Process.Wait, тож статус виходу
// діставався випадковому переможцю, а причина завершення могла не з'явитися
// зовсім. Тут кожна сесія публікує рівно одну EndReason.
type process struct {
	cmd     *exec.Cmd
	done    chan struct{} // закривається після повернення cmd.Wait
	waitErr error         // валідний лише після done
	closing atomic.Bool
	end     chan EndReason // буфер 1: публікація ніколи не блокує жнеця
	endOnce sync.Once
}

// newProcess бере вже запущений cmd і одразу стартує жнеця.
//
// classify отримує closing параметром, а не через closure: горутина стартує
// раніше, ніж викликач отримає покажчик на process, тож читання прапорця
// зсередини callback було б гонкою за ініціалізацію самої сесії.
func newProcess(cmd *exec.Cmd, classify func(waitErr error, closing bool) EndReason) *process {
	p := &process{
		cmd:  cmd,
		done: make(chan struct{}),
		end:  make(chan EndReason, 1),
	}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
		p.publish(classify(p.waitErr, p.closing.Load()))
	}()
	return p
}

// publish віддає причину завершення; спрацьовує лише перша (сесія може знати
// причину раніше за вихід процесу — mpv дізнається її з IPC-події end-file).
func (p *process) publish(reason EndReason) {
	p.endOnce.Do(func() { p.end <- reason })
}

// End повертає канал, що отримає рівно одну причину завершення.
func (p *process) End() <-chan EndReason { return p.end }

// Wait чекає завершення процесу; безпечний для повторних і паралельних викликів.
func (p *process) Wait() error {
	<-p.done
	return p.waitErr
}

// Close зупиняє плеєр і чекає жнеця. Ідемпотентний і безпечний паралельно з Wait.
// Прапорець closing відрізняє наш власний Kill від самовільного падіння: після
// Kill cmd.Wait завжди повертає «signal: killed», і без прапорця вихід
// користувача виглядав би як збій плеєра.
func (p *process) Close() {
	p.closing.Store(true)
	select {
	case <-p.done:
	default:
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	}
	<-p.done
}

const (
	dialRetryStep     = 100 * time.Millisecond
	dialRetryBudget   = 10 * time.Second
	dialRetryAttempts = int(dialRetryBudget / dialRetryStep)
)

// dialRetry чекає на керуючий сокет: і mpv, і VLC створюють його вже після
// старту процесу, тому перші спроби завжди отримують «connection refused».
// dead — done-канал жнеця: плеєр, що впав одразу (погані аргументи, немає
// кодека), не має коштувати користувачеві всіх 10 с очікування.
func dialRetry(ctx context.Context, dead <-chan struct{}, network, addr string) (net.Conn, error) {
	var lastErr error
	for i := 0; i < dialRetryAttempts; i++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%s %s: очікування скасовано: %w: %w", network, addr, err, errs.ErrPlayer)
		}
		conn, err := net.Dial(network, addr)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		timer := time.NewTimer(dialRetryStep)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%s %s: очікування скасовано: %w: %w", network, addr, ctx.Err(), errs.ErrPlayer)
		case <-dead:
			timer.Stop()
			return nil, fmt.Errorf("%s %s: плеєр завершився до появи сокета: %w: %w", network, addr, lastErr, errs.ErrPlayer)
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("%s %s: сокет не з'явився: %w: %w", network, addr, lastErr, errs.ErrPlayer)
}
