package ai

import (
	"errors"
	"fmt"
)

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

// CleanupSessionResources invokes all registered cleanup hooks for the given
// session id and joins cleanup errors. Empty session ids are ignored by hooks
// that scope by session.
func CleanupSessionResources(sessionID string) error {
	var errs []error
	for _, entry := range sessionResourceCleanups {
		if entry.cleanup == nil {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, fmt.Errorf("session resource cleanup panicked: %v", r))
				}
			}()
			entry.cleanup(sessionID)
		}()
	}
	return errors.Join(errs...)
}
