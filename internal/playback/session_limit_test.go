package playback

import (
	"errors"
	"testing"

	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/playertest"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

func TestSessionLimitSurvivesSeriesAndStopsExactly(t *testing.T) {
	eng, sess, res, _ := liveEngine(t)
	id, _, err := eng.Begin(res)
	if err != nil {
		t.Fatal(err)
	}
	eng.Live.BeginChain(res.Ref)
	eng.Live.set(sess, res.Ref, res.Name, 1, res.Source.Studio)
	if err := eng.Live.SetSessionLimit(2); err != nil {
		t.Fatal(err)
	}
	eng.Live.clear()
	first, err := eng.Finish(player.EndEOF, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	eps := []provider.Episode{{Number: 1}, {Number: 2}, {Number: 3}}
	if next, ok := ContinueEpisode(first, nil, false, res.Ref, 1, eps, false); !ok || next != 2 {
		t.Fatalf("budget must override autoplay: %d %v %+v", next, ok, first)
	}
	if got := eng.Live.Limit(); !got.Enabled || got.Remaining != 1 {
		t.Fatalf("live lost budget: %+v", got)
	}
	eng.Live.set(playertest.NewSession(player.EndEOF, nil, nil), res.Ref, res.Name, 2, res.Source.Studio)
	eng.Live.clear()
	last, err := eng.Finish(player.EndEOF, id, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ContinueEpisode(last, nil, false, res.Ref, 2, eps, true); ok {
		t.Fatal("exhausted budget allowed autoplay")
	}
	if !last.SessionLimit.Enabled || last.SessionLimit.Remaining != 0 {
		t.Fatalf("lost exhausted state: %+v", last)
	}
	again, _ := eng.Finish(player.EndEOF, id, 2)
	if again.SessionLimit.Remaining != 0 {
		t.Fatal("finish consumed twice")
	}
	eng.Live.EndChain()
	if eng.Live.Limit().Enabled {
		t.Fatal("EndChain retained limit")
	}
}

func TestManualNextReplacesBudgetSlot(t *testing.T) {
	eng, sess, res, _ := liveEngine(t)
	id, _, _ := eng.Begin(res)
	eng.Live.set(sess, res.Ref, res.Name, 1, res.Source.Studio)
	if err := eng.Live.SetSessionLimit(2); err != nil {
		t.Fatal(err)
	}
	if err := eng.Live.Next(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Live.SetSessionLimit(4); !errors.Is(err, ErrNotPlaying) {
		t.Fatalf("transition accepted budget: %v", err)
	}
	r, err := eng.Finish(player.EndQuit, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.SessionLimit.Remaining != 2 {
		t.Fatal("manual switch consumed budget")
	}
	if n, ok := ContinueEpisode(r, nil, false, res.Ref, 1, []provider.Episode{{Number: 2}}, false); !ok || n != 2 {
		t.Fatalf("manual next lost: %d %v", n, ok)
	}
	other := provider.TitleRef{Provider: "stub", Slug: "2-other"}
	eng.Live.set(sess, other, "Other", 1, "Studio")
	if eng.Live.Limit().Enabled {
		t.Fatal("budget leaked to other title")
	}
}

func TestSessionToggleAndDisabledAreDistinct(t *testing.T) {
	l := &Live{}
	if err := l.SetSessionLimit(2); !errors.Is(err, ErrNotPlaying) {
		t.Fatal(err)
	}
	l.set(playertest.NewSession(player.EndQuit, nil, nil), provider.TitleRef{}, "T", 0, "Studio")
	for _, n := range []int{-1, 13} {
		if l.SetSessionLimit(n) == nil {
			t.Fatalf("accepted %d", n)
		}
	}
	if err := l.SetSessionLimit(3); err != nil {
		t.Fatal(err)
	}
	if err := l.ToggleStopAfter(); err != nil {
		t.Fatal(err)
	}
	if !l.StopAfter() || l.Limit().Remaining != 1 {
		t.Fatal("toggle from3 must set1")
	}
	if err := l.ToggleStopAfter(); err != nil {
		t.Fatal(err)
	}
	if l.Limit().Enabled {
		t.Fatal("toggle from1 must disable")
	}
	s, err := l.Snapshot()
	if err != nil || s.Studio != "Studio" || s.SessionLimited {
		t.Fatalf("snapshot %+v %v", s, err)
	}
}

func TestContinuationStopAndEpisodeZero(t *testing.T) {
	ref := provider.TitleRef{Provider: "stub", Slug: "1-title"}
	eps := []provider.Episode{{Number: 0}, {Number: 2}}
	r := &Result{Intent: IntentPlay, Requested: PlayRequest{Ref: ref, Episode: 0}, Reason: player.EndQuit}
	if n, ok := ContinueEpisode(r, nil, false, ref, 2, eps, false); !ok || n != 0 {
		t.Fatal("episode zero rejected")
	}
	for _, tc := range []struct {
		r        *Result
		err      error
		quitting bool
	}{
		{r, errors.New("save failed"), false}, {r, nil, true},
		{&Result{Intent: IntentStop, SessionLimit: SessionLimit{Enabled: true, Remaining: 2}}, nil, false},
		{&Result{Reason: player.EndQuit, SessionLimit: SessionLimit{Enabled: true, Remaining: 2}}, nil, false},
		{&Result{Reason: player.EndEOF, SessionLimit: SessionLimit{Enabled: true, Remaining: 2}}, nil, false},
	} {
		if _, ok := ContinueEpisode(tc.r, tc.err, tc.quitting, ref, 2, eps, true); ok {
			t.Fatalf("unexpected continuation %+v", tc)
		}
	}
}
