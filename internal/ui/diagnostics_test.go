package ui

import (
	"errors"
	"strings"
	"testing"
)

func TestSettingsRemoteDiagnosticsOptIn(t *testing.T) {
	m := newTestModel(t)
	m.remote.Err = errors.New("HTTP 503 secret\x1b[2J")
	rows := m.remoteNotes()
	for _, row := range rows {
		if strings.Contains(row.title, "503") || strings.Contains(row.title, "secret") {
			t.Fatalf("diagnostics leaked: %+v", row)
		}
	}
}
