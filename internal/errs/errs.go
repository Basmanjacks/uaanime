// Package errs — сентинел-помилки, за якими UI і CLI розрізняють три класи збоїв.
package errs

import (
	"context"
	"errors"
	"net"
)

var (
	ErrOffline  = errors.New("немає з'єднання")
	ErrNoStream = errors.New("потік не знайдено")
	ErrNoPlayer = errors.New("не знайдено жодного відеоплеєра")
	ErrProvider = errors.New("джерело зламалось")
	// ErrPlayer — плеєр знайдено, але сесія не піднялася (старт, IPC, сокет).
	// Це інший клас, ніж ErrNoPlayer: підказка «встановіть плеєр» тут хибна.
	ErrPlayer = errors.New("плеєр не запустився")
)

// Offline розпізнає помилки, якими стандартний HTTP-клієнт обгортає
// недоступну мережу, DNS і тайм-аут запиту, а також уже класифіковані
// ErrOffline — щоб виклики не дублювали errors.Is поруч із Offline.
func Offline(err error) bool {
	if errors.Is(err, ErrOffline) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
