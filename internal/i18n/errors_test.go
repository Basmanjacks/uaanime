package i18n

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestErrorText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "offline",
			err:  fmt.Errorf("пошук: %w", errs.ErrOffline),
			want: MsgOffline,
		},
		{
			name: "no stream",
			err:  fmt.Errorf("серія: %w", errs.ErrNoStream),
			want: MsgNoPlayableHost,
		},
		{
			name: "no player",
			err:  fmt.Errorf("старт: %w", errs.ErrNoPlayer),
			want: MsgNoPlayer,
		},
		{
			name: "player",
			err:  fmt.Errorf("сокет: %w", errs.ErrPlayer),
			want: MsgPlayerUnavailable,
		},
		{
			name: "provider",
			err:  fmt.Errorf("сторінка: %w", errs.ErrProvider),
			want: MsgSourceUnavailable,
		},
		{
			name: "unclassified",
			err:  errors.New("невідома помилка"),
			want: MsgOperationFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ErrorText(tt.err); got != tt.want {
				t.Fatalf("ErrorText(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

// Помилка цитує embed із чужої сторінки: керуюча послідовність не має дійти
// до термінала навіть у тексті помилки.
func TestErrorTextStripsControlSequences(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("embed %q: %w", "https://x/\x1b[2J", errs.ErrNoStream)
	got := ErrorText(err)
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("ErrorText = %q, містить ESC", got)
	}
}

func TestErrorTextHidesDiagnostics(t *testing.T) {
	for _, class := range []error{errs.ErrProvider, errs.ErrPlayer} {
		err := fmt.Errorf("HTTP 503 https://example.invalid/private?token=secret: %w", class)
		got := ErrorText(err)
		if strings.Contains(got, "503") || strings.Contains(got, "https://") || strings.Contains(got, "secret") {
			t.Fatalf("leaked diagnostics: %s", got)
		}
	}
}

func TestDebugDiagnosticsAreOptInAndClean(t *testing.T) {
	err := fmt.Errorf("HTTP 503 https://example.invalid/\x1b[2J: %w", errs.ErrProvider)
	if got := ErrorTextWithDebug(err, true); !strings.Contains(got, "HTTP 503") || strings.ContainsRune(got, '\x1b') {
		t.Fatalf("debug: %q", got)
	}
	if got := ErrorTextWithDebug(err, false); got != ErrorText(err) {
		t.Fatalf("default: %q", got)
	}
}
