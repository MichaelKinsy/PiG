package codingagent

import (
	"testing"
	"time"
)

func scopeSession(id string) SessionInfo {
	return SessionInfo{Path: "/tmp/" + id + ".jsonl", ID: id, Created: time.UnixMilli(0), Modified: time.UnixMilli(0), MessageCount: 1, FirstMessage: "hello", AllMessagesText: "hello"}
}

// Upstream packages/coding-agent/test/session-selector-path-delete.test.ts:187,219 requires two further Tab inputs while the All loader is unresolved. This entry-boundary guard must pass before those scope-race cases can execute.
func TestSessionSelectorScopeInputDoesNotAwaitLoader(t *testing.T) {
	started, release, handled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	selector := newSessionSelectorFromListers(
		func(SessionListOptions) ([]SessionInfo, error) { return []SessionInfo{scopeSession("current")}, nil },
		func(SessionListOptions) ([]SessionInfo, error) {
			close(started)
			<-release
			return []SessionInfo{scopeSession("all")}, nil
		},
		nil, nil, "", sessionSelectorInputBindings(t),
	)
	go func() { selector.HandleInput("\t"); close(handled) }()
	<-started
	select {
	case <-handled:
	case <-time.After(time.Second): // A deadlock watchdog; the deliberately unresolved loader defines the ordering obligation.
		t.Error("scope input waited for the unresolved All loader; subsequent Tab inputs cannot execute")
	}
	close(release)
	<-handled
	selector.close()
}
