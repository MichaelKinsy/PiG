// Package codexwsobserve lets the provider bridge learn that a session's Codex WebSocket debug counters changed (D74): the helper process that owns the bridge keeps a replica of them, because Pi's
// getOpenAICodexWebSocketDebugStats is synchronous. Production code outside the bridge installs no observer; with none installed Notify costs one atomic load and allocates nothing.
package codexwsobserve

import "sync/atomic"

var installed atomic.Pointer[func(sessionID string)]

// Install sets the observer until the returned restore function runs. The observer is called after the counters changed and after the store's lock was released, so it may read them. An empty
// session id means every session's counters were cleared.
func Install(observer func(sessionID string)) (restore func()) {
	previous := installed.Swap(&observer)
	return func() { installed.Store(previous) }
}

// Notify tells the installed observer, if any, that sessionID's counters changed.
func Notify(sessionID string) {
	if observer := installed.Load(); observer != nil {
		(*observer)(sessionID)
	}
}
