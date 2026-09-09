package playback

import (
	"errors"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestAggregateFailuresHasOneClass(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failures []error
		want     error
	}{
		{"offline", []error{errs.ErrOffline, errs.ErrOffline}, errs.ErrOffline},
		{"absent", []error{errs.ErrNoStream, errs.ErrNoStream}, errs.ErrNoStream},
		{"mixed", []error{errs.ErrNoStream, errs.ErrProvider}, errs.ErrProvider},
		{"mixed offline", []error{errs.ErrOffline, errs.ErrNoStream}, errs.ErrProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := aggregateFailures(1, tc.failures)
			for _, class := range []error{errs.ErrOffline, errs.ErrProvider, errs.ErrNoStream} {
				if errors.Is(err, class) != errors.Is(tc.want, class) {
					t.Fatalf("classification %v: %v", class, err)
				}
			}
		})
	}
}
