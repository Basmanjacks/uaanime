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
			want: fmt.Sprintf(MsgPlayerFailed, fmt.Errorf("сокет: %w", errs.ErrPlayer)),
		},
		{
			name: "provider",
			err:  fmt.Errorf("сторінка: %w", errs.ErrProvider),
			want: fmt.Sprintf(MsgProviderFailed, fmt.Errorf("сторінка: %w", errs.ErrProvider)),
		},
		{
			name: "unclassified",
			err:  errors.New("невідома помилка"),
			want: fmt.Sprintf(MsgProviderFailed, errors.New("невідома помилка")),
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
