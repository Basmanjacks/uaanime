package errs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestOffline(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "DNS",
			err:  &net.DNSError{Err: "no such host", Name: "example.invalid", IsNotFound: true},
			want: true,
		},
		{
			name: "deadline у обгортці",
			err:  fmt.Errorf("запит: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "звичайна помилка",
			err:  errors.New("збій розбору"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Offline(tt.err); got != tt.want {
				t.Fatalf("Offline(%v) = %v, очікував %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestErrNoPlayer(t *testing.T) {
	if got, want := ErrNoPlayer.Error(), "не знайдено жодного відеоплеєра"; got != want {
		t.Fatalf("ErrNoPlayer = %q, очікував %q", got, want)
	}
}
