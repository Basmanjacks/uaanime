package library

import (
	"testing"
	"time"

	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestRecordPositionAndResume(t *testing.T) {
	lib := &Library{}
	ref := provider.TitleRef{Provider: "anitube", Slug: "1-x", Name: "X"}
	title := lib.EnsureTitle(ref, func() string { return "id-1" })
	if lib.EnsureTitle(ref, func() string { return "id-2" }) != title {
		t.Fatal("EnsureTitle створив дубль")
	}

	now := time.Now()
	// вийшли на 14:32 з 24 хв
	lib.RecordPosition(title.ID, 3, 872, 1440, now)
	ep, pos, ok := lib.Resume(title.ID)
	if !ok || ep != 3 || pos != 872 {
		t.Fatalf("Resume = (%d, %v, %v)", ep, pos, ok)
	}

	// додивилися до 95% — завершено, resume пропонує наступну
	lib.RecordPosition(title.ID, 3, 1368, 1440, now.Add(time.Minute))
	p := lib.ProgressFor(title.ID, 3)
	if p == nil || !p.Completed {
		t.Fatalf("очікував completed на 95%%: %+v", p)
	}
	ep, pos, ok = lib.Resume(title.ID)
	if !ok || ep != 4 || pos != 0 {
		t.Fatalf("Resume після завершення = (%d, %v, %v)", ep, pos, ok)
	}

	if e := lib.EntryFor(title.ID); e.LastEpisode != 3 {
		t.Fatalf("LastEpisode = %d", e.LastEpisode)
	}
}

func TestCompletionThresholdNotReached(t *testing.T) {
	lib := &Library{}
	lib.RecordPosition("t", 1, 100, 1440, time.Now()) // ~7%
	if lib.ProgressFor("t", 1).Completed {
		t.Fatal("7% не є завершенням")
	}
}
