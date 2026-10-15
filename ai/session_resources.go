package ai

import "fmt"

// SessionResourceCleanup releases provider resources scoped to a logical
// session, such as cached transport connections.
type SessionResourceCleanup func(sessionID string)

type sessionResourceCleanupEntry struct {
	id      int
	cleanup SessionResourceCleanup
}

var (
	sessionResourceCleanups []sessionResourceCleanupEntry
	nextCleanupID           int
)

// RegisterSessionResourceCleanup registers a session-scoped cleanup hook and
// returns an unregister function. Mirrors upstream registerSessionResourceCleanup.
func RegisterSessionResourceCleanup(cleanup SessionResourceCleanup) func() {
	nextCleanupID++
	id := nextCleanupID
	sessionResourceCleanups = append(sessionResourceCleanups, sessionResourceCleanupEntry{id: id, cleanup: cleanup})
	return func() {
		for i, existing := range sessionResourceCleanups {
			if existing.id == id {
				sessionResourceCleanups = append(sessionResourceCleanups[:i], sessionResourceCleanups[i+1:]...)
				return
			}
		}
	}
}

// sessionResourcesCleanupError is cleanupSessionResources' AggregateError (session-resources.ts:19): the message is fixed and the
// hooks' failures are its members, in registration order.
type sessionResourcesCleanupError struct{ errors []error }

func (e *sessionResourcesCleanupError) Error() string   { return "Failed to cleanup session resources" }
func (e *sessionResourcesCleanupError) Unwrap() []error { return e.errors }

// CleanupSessionResources calls every registered cleanup hook with the session id, in registration order, and keeps going when one
// panics (a JavaScript throw). It returns nil when every hook returned, and otherwise an error whose message is "Failed to cleanup
// session resources" and that wraps each hook's failure (session-resources.ts cleanupSessionResources throws an AggregateError). An
// empty session id is `undefined`.
func CleanupSessionResources(sessionID string) error {
	var errs []error
	for _, entry := range sessionResourceCleanups {
		if entry.cleanup == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err, ok := r.(error); ok {
						errs = append(errs, fmt.Errorf("session resource cleanup panicked: %w", err))
					} else {
						errs = append(errs, fmt.Errorf("session resource cleanup panicked: %v", r))
					}
				}
			}()
			entry.cleanup(sessionID)
		}()
	}
	if len(errs) > 0 {
		return &sessionResourcesCleanupError{errors: errs}
	}
	return nil
}
