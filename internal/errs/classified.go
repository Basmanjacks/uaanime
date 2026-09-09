package errs

import "fmt"

// WithClass separates an aggregate's public classification from diagnostics.
// Unwrapping all attempts would expose contradictory classes to errors.Is.
func WithClass(class, cause error) error { return &classified{class: class, cause: cause} }

type classified struct{ class, cause error }

func (e *classified) Error() string { return fmt.Sprintf("%v: %v", e.class, e.cause) }
func (e *classified) Unwrap() error { return e.class }
