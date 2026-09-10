package errs

import "fmt"

// ErrPlayerStart identifies failure before a session exists, while retaining
// the public player-error category for user-facing diagnostics.
var ErrPlayerStart = fmt.Errorf("start session: %w", ErrPlayer)
