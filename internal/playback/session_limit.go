package playback

import (
	"github.com/Basmanjacks/uaanime/internal/errs"
	"github.com/Basmanjacks/uaanime/internal/player"
	"github.com/Basmanjacks/uaanime/internal/provider"
)

// SessionLimit distinguishes a finished allowance from ordinary autoplay.
// It belongs to the chain, not to any individual player process.
type SessionLimit struct {
	Enabled   bool
	Remaining int
}

const MaxSessionLimit = 12

func (l *Live) BeginChain(ref provider.TitleRef) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.chainRef, l.chainActive, l.limit = ref, true, SessionLimit{}
	l.mu.Unlock()
}

func (l *Live) EndChain() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.chainRef, l.chainActive, l.limit = provider.TitleRef{}, false, SessionLimit{}
	l.mu.Unlock()
}

func (l *Live) Limit() SessionLimit {
	if l == nil {
		return SessionLimit{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

func (l *Live) SetSessionLimit(n int) error {
	if n < 0 || n > MaxSessionLimit {
		return errs.ErrInvalidSessionLimit
	}
	if l == nil {
		return ErrNotPlaying
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess == nil {
		return ErrNotPlaying
	}
	l.limit = SessionLimit{Enabled: n > 0, Remaining: n}
	return nil
}

func (l *Live) ToggleStopAfter() error {
	if l == nil {
		return ErrNotPlaying
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sess == nil {
		return ErrNotPlaying
	}
	if l.limit.Enabled && l.limit.Remaining == 1 {
		l.limit = SessionLimit{}
	} else {
		l.limit = SessionLimit{Enabled: true, Remaining: 1}
	}
	return nil
}

// finishSession consumes an intent once but retains the decremented budget in
// Live. Commands are rejected between player sessions, so this snapshot cannot
// overwrite a newer HTTP change. Repeated Finish must not consume another slot.
func (l *Live) finishSession(reason player.EndReason) (Intent, PlayRequest, SessionLimit) {
	if l == nil {
		return IntentNone, PlayRequest{}, SessionLimit{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	intent, requested := l.intent, l.requested
	l.intent, l.requested = IntentNone, PlayRequest{}
	if !l.finished && reason == player.EndEOF && intent == IntentNone && l.limit.Enabled && l.limit.Remaining > 0 {
		l.limit.Remaining--
	}
	l.finished = true
	return intent, requested, l.limit
}

// ContinueEpisode is the single continuation policy for TUI and CLI. The bool
// is necessary because providers legitimately publish an episode numbered zero.
func ContinueEpisode(r *Result, err error, quitting bool, ref provider.TitleRef, current int, episodes []provider.Episode, autoplay bool) (int, bool) {
	if r == nil || err != nil || quitting || r.Intent == IntentStop {
		return 0, false
	}
	if r.Intent == IntentPlay {
		if !r.Requested.Ref.Same(ref) {
			return 0, false
		}
		for _, ep := range episodes {
			if ep.Number == r.Requested.Episode {
				return ep.Number, true
			}
		}
		return 0, false
	}
	if r.Intent != IntentNext {
		if r.Reason != player.EndEOF {
			return 0, false
		}
		if r.SessionLimit.Enabled {
			if r.SessionLimit.Remaining == 0 {
				return 0, false
			}
		} else if !autoplay {
			return 0, false
		}
	}
	return NextEpisodeNumber(episodes, current)
}
