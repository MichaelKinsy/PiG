package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/src/runtime/session-lock-doc.ts: the session's lock in its Durable state (worker): written by the
// sessions plugin's worker facet from the server's `SessionLock`, read by the tool guard and the prompt sections at each
// use. Absent or nil: the session is not locked.

// SessionLockState is the `p2.session-lock` document.
type SessionLockState struct {
	Lock *SessionLock `json:"lock"`
}

// SessionLockDoc is the session's lock document.
var SessionLockDoc = durable.DefineDoc(durable.DocDefinition[SessionLockState]{
	CommonDocDefinition: durable.CommonDocDefinition[SessionLockState]{
		Kind:           "p2.session-lock",
		Version:        1,
		Initial:        func() SessionLockState { return SessionLockState{} },
		CheckpointWhen: func(SessionLockState, []durable.Op, durable.CheckpointInfo) bool { return true },
	},
	DocumentSemantics: durable.DocumentSemantics{Scope: durable.ScopeSession},
})

// LockOf is the session's lock, or nil when it is not locked.
func LockOf(ctx context.Context, read durable.DocumentReader) (*SessionLock, error) {
	state, err := durable.Snapshot(ctx, read, SessionLockDoc)
	if err != nil || state == nil {
		return nil, err
	}
	return state.Lock, nil
}
