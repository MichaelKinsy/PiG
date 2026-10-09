package codingagent

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func newStatusTestSelector(loader func() ([]SessionInfo, error), allLoaders ...func() ([]SessionInfo, error)) *SessionSelectorComponent {
	allLoader := loader
	if len(allLoaders) > 0 {
		allLoader = allLoaders[0]
	}
	return newLoadedSessionSelector(loader, allLoader, nil, nil, "", nil)
}

func TestSessionSelectorLoadErrorExpiresAtPiDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		selector := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, errors.New("status-load-error") })
		clean := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, nil }).Render(120)
		initial := selector.Render(120)
		if !strings.Contains(strings.Join(initial, "\n"), "Failed to load sessions: status-load-error") {
			t.Fatalf("load error not visible: %q", initial)
		}
		time.Sleep(3999 * time.Millisecond)
		if got := selector.Render(120); !reflect.DeepEqual(got, initial) {
			t.Fatalf("status expired early: %q", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if got := selector.Render(120); !reflect.DeepEqual(got, clean) {
			t.Fatalf("load failure did not restore exact navigation hints at 4000ms:\n got %q\nwant %q", got, clean)
		}
	})
}

// upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:990-1004 — the error's deadline starts when the awaited load fails, not when loading begins.
func TestSessionSelectorAsyncFailureStartsItsOwnStatusDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		result := make(chan sessionLoadResult, 1)
		loader := func(*sessionLoad, SessionListProgress) <-chan sessionLoadResult { return result }
		selector := newSessionSelectorWithLoaders(loader, loader, nil, nil, "", nil)
		defer selector.close()
		time.Sleep(3 * time.Second)
		result <- sessionLoadResult{err: errors.New("deferred failure")}
		selector.drainLoadUpdates()
		initial := selector.Render(120)
		if !strings.Contains(strings.Join(initial, "\n"), "Failed to load sessions: deferred failure") {
			t.Fatalf("deferred error missing: %q", initial)
		}
		time.Sleep(3999 * time.Millisecond)
		if got := selector.Render(120); !reflect.DeepEqual(got, initial) {
			t.Fatalf("asynchronous failure expired early: %q", got)
		}
		time.Sleep(time.Millisecond)
		clean := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, nil })
		defer clean.close()
		if got, want := selector.Render(120), clean.Render(120); !reflect.DeepEqual(got, want) {
			t.Fatalf("asynchronous failure retained status: got %q want %q", got, want)
		}
	})
}

func TestSessionSelectorStatusReplacementRestartsAndCancelsTimers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		selector := newStatusTestSelector(func() ([]SessionInfo, error) { return nil, nil })
		defer selector.clearStatusMessage()
		selector.setStatusMessage("first", true, sessionSelectorLoadErrorTimeout)
		firstTimer := selector.statusState.timer
		time.Sleep(3 * time.Second)
		selector.setStatusMessage("second", true, sessionSelectorLoadErrorTimeout)
		time.Sleep(time.Second)
		selector.Render(120)
		if selector.statusState.message != "second" {
			t.Fatal("the first deadline cleared its replacement")
		}
		select {
		case <-firstTimer.C:
			t.Fatal("replacing a status left its timer active")
		default:
		}
		time.Sleep(3 * time.Second)
		selector.Render(120)
		if selector.statusState.message != "" || selector.statusTimeout() != nil {
			t.Fatal("replacement did not expire at its own deadline")
		}
		selector.setStatusMessage("timed", true, sessionSelectorLoadErrorTimeout)
		oldTimer := selector.statusState.timer
		selector.setStatusMessage("persistent", false, 0)
		time.Sleep(sessionSelectorLoadErrorTimeout)
		selector.Render(120)
		if selector.statusState.message != "persistent" || selector.statusTimeout() != nil {
			t.Fatal("a prior timer changed an untimed status")
		}
		select {
		case <-oldTimer.C:
			t.Fatal("untimed replacement left its predecessor timer active")
		default:
		}
	})
}

func TestSessionSelectorLeavingClearsStatusTimer(t *testing.T) {
	for _, input := range []string{"\x1b", "\r"} {
		t.Run(input, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				selector := newStatusTestSelector(func() ([]SessionInfo, error) { return []SessionInfo{{Path: "session.jsonl", ID: "session"}}, nil })
				selector.setStatusMessage("failure", true, sessionSelectorLoadErrorTimeout)
				timer := selector.statusState.timer
				selector.HandleInput(input)
				if !selector.Done() || selector.statusState.message != "" || selector.statusTimeout() != nil {
					t.Fatal("leaving the selector retained its status")
				}
				time.Sleep(sessionSelectorLoadErrorTimeout)
				select {
				case <-timer.C:
					t.Fatal("leaving the selector retained its timer")
				default:
				}
			})
		})
	}
}
