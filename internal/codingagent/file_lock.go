package codingagent

import (
	"time"

	"github.com/gofrs/flock"
)

// Upstream acquireLockSyncWithRetry (settings-manager.ts) and
// acquireTrustLockSync (trust-manager.ts) try lockSync up to maxAttempts
// times, waiting delayMs between attempts while the lock is held.
const (
	// upstream: packages/coding-agent/src/core/settings-manager.ts:acquireLockSyncWithRetry
	syncLockMaxAttempts = 10
	// upstream: packages/coding-agent/src/core/settings-manager.ts:acquireLockSyncWithRetry
	syncLockDelay = 20 * time.Millisecond
)

// acquireSyncLockWithRetry mirrors those helpers: it tries lock up to
// syncLockMaxAttempts times with syncLockDelay between attempts, and reports
// false when the lock stays held.
func acquireSyncLockWithRetry(lock *flock.Flock) (bool, error) {
	for attempt := 1; ; attempt++ {
		locked, err := lock.TryLock()
		if err != nil || locked {
			return locked, err
		}
		if attempt == syncLockMaxAttempts {
			return false, nil
		}
		time.Sleep(syncLockDelay)
	}
}
