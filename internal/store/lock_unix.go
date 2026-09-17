//go:build darwin || linux

package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

// Lock holds an inter-process flock on path until Close or process exit; busy
// is returned when someone else already holds it, so each caller names the
// conflict in its own words. Never unlink the lock file: a second inode would
// split the lock and both processes would happily "own" it.
func Lock(path string, busy error) (io.Closer, error) {
	// 0755: the path may live inside the download folder, which is a media
	// library the user opens in Finder — not the private 0700 data directory
	// (LockWriter creates that one itself, before calling here).
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, busy
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f, nil
}

// LockWriter holds the data directory's single writer lease until Close or
// process exit.
func LockWriter(dir string) (io.Closer, error) {
	// Каталог даних приватний: у library.json видно, що людина дивиться.
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return Lock(filepath.Join(dir, "writer.lock"), errs.ErrStoreBusy)
}
