//go:build !darwin && !linux

package store

import (
	"fmt"
	"io"
)

// Releases target Linux and macOS; fail closed on unsupported systems.
func LockWriter(string) (io.Closer, error) {
	return nil, fmt.Errorf("writer locking is supported on Linux and macOS only")
}
