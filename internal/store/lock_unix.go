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

// LockWriter holds the data directory's single writer lease until Close or
// process exit. Never unlink the lock file: a second inode would split the lock.
func LockWriter(dir string) (io.Closer, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "writer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errs.ErrStoreBusy
		}
		return nil, fmt.Errorf("lock library: %w", err)
	}
	return f, nil
}
