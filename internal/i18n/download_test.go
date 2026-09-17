package i18n

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/errs"
)

func TestDownloadErrorsHaveOwnText(t *testing.T) {
	cases := map[error]string{
		errs.ErrDiskFull:          MsgDiskFull,
		errs.ErrNoWriteAccess:     MsgNoWriteAccess,
		errs.ErrCancelled:         MsgDownloadCancelled,
		errs.ErrEncryptedStream:   MsgStreamEncrypted,
		errs.ErrUnsupportedStream: MsgStreamUnsupported,
		errs.ErrStreamExpired:     MsgStreamExpired,
		errs.ErrAlreadySaved:      MsgAlreadySaved,
		errs.ErrDownloadBusy:      MsgDownloadBusy,
	}
	seen := map[string]bool{}
	for err, want := range cases {
		got := ErrorText(fmt.Errorf("wrap: %w", err))
		if got != want {
			t.Errorf("%v: got %q, want %q", err, got, want)
		}
		if seen[got] {
			t.Errorf("%v: text %q shared with another class", err, got)
		}
		seen[got] = true
	}
	if ErrorText(errors.New("x")) != MsgOperationFailed {
		t.Errorf("unknown error must fall back to MsgOperationFailed")
	}
}

func TestBytes(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 Б"},
		{512, "512 Б"},
		{1024, "1 КБ"},
		{1536, "1,5 КБ"},
		{610 * 1024 * 1024, "610 МБ"},
		{1288490188, "1,2 ГБ"},
		{10 * 1024 * 1024 * 1024, "10 ГБ"},
	}
	for _, c := range cases {
		if got := Bytes(c.n); got != c.want {
			t.Errorf("Bytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestDownloadPlurals(t *testing.T) {
	if got := Downloads(1); got != "1 завантаження" {
		t.Errorf("Downloads(1) = %q", got)
	}
	if got := Downloads(5); got != "5 завантажень" {
		t.Errorf("Downloads(5) = %q", got)
	}
	if got := ActiveDownloads(2); got != "2 активних" {
		t.Errorf("ActiveDownloads(2) = %q", got)
	}
	if got := QualityLabel(0); got != MsgDownloadQualityAuto {
		t.Errorf("QualityLabel(0) = %q", got)
	}
	if got := QualityLabel(720); got != "720p" {
		t.Errorf("QualityLabel(720) = %q", got)
	}
}
