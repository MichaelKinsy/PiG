package codingagent

import (
	"errors"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// acquireSyncLockWithRetry shares Pi's settings/auth/trust directory-lock
// protocol with independently imported Node stores.
func acquireSyncLockWithRetry(path string) (release func() error, locked bool, err error) {
	lease, err := pilock.AcquireSync(path)
	if errors.Is(err, pilock.ErrLocked) && !errors.Is(err, pilock.ErrLegacyLocked) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return lease.Release, true, nil
}
