package errs

import "errors"

// ErrUndoConflict means the target title changed after the reversible action.
var ErrUndoConflict = errors.New("undo conflicts with current title state")
