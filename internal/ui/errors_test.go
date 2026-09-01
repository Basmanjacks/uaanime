package ui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/i18n"
)

func TestErrText(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "offline",
			err:  fmt.Errorf("пошук: %w", errs.ErrOffline),
			want: i18n.MsgOffline,
		},
		{
			name: "no stream",
			err:  fmt.Errorf("серія: %w", errs.ErrNoStream),
			want: i18n.MsgNoPlayableHost,
		},
		{
			name: "provider",
			err:  fmt.Errorf("сторінка: %w", errs.ErrProvider),
			want: fmt.Sprintf(i18n.MsgProviderFailed, fmt.Errorf("сторінка: %w", errs.ErrProvider)),
		},
		{
			name: "unclassified",
			err:  errors.New("невідома помилка"),
			want: fmt.Sprintf(i18n.MsgProviderFailed, errors.New("невідома помилка")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errText(tt.err); got != tt.want {
				t.Fatalf("errText(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestEpisodesDoneOfflineUsesCacheStatus(t *testing.T) {
	m := newTestModel(t)
	m.reqID = 1

	m, _ = updateTestModel(t, m, episodesDoneMsg{
		req:      1,
		ref:      testRefs("cached", 1)[0],
		eps:      testEpisodes(1),
		offline:  true,
		navigate: true,
	})

	if m.errText != "" {
		t.Fatalf("errText = %q, want empty", m.errText)
	}
	if m.status != i18n.MsgOfflineCache {
		t.Fatalf("status = %q, want %q", m.status, i18n.MsgOfflineCache)
	}
}
