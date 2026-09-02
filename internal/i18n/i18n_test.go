package i18n

import (
	"testing"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestKindLabel(t *testing.T) {
	tests := []struct {
		kind provider.Kind
		want string
	}{
		{provider.KindDub, "дубляж"},
		{provider.KindVoiceover, "озвучення"},
		{provider.KindSub, "субтитри"},
		{provider.KindMulti, "змішано"},
		// Невідомий kind не показується як є — сирий рядок із диска в терміналі неприпустимий.
		{provider.Kind("unknown"), "змішано"},
		{provider.Kind("\x1b[2J"), "змішано"},
	}

	for _, tt := range tests {
		if got := KindLabel(tt.kind); got != tt.want {
			t.Errorf("KindLabel(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}
