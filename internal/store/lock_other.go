//go:build !darwin && !linux

package store

import (
	"fmt"
	"io"
)

// Releases target Linux and macOS; fail closed on unsupported systems.
// Fail closed, not open: a no-op lock would let two processes write the same
// file and lose data silently.
func Lock(string, error) (io.Closer, error) {
	return nil, fmt.Errorf("file locking is supported on Linux and macOS only")
}

func LockWriter(string) (io.Closer, error) {
	return nil, fmt.Errorf("writer locking is supported on Linux and macOS only")
}
