package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/workers"
)

// Ports apps/p2/src/runtime/process-lock.ts: one process per name. Chord's lock waits while another process holds it;
// p2's servers refuse instead (one server per data directory), after waiting wait for a holder that is exiting.

// TakeProcessLock takes the lock name, or fails when another process still holds it after wait (at least 1 ms); the
// returned function releases it.
func TakeProcessLock(name string, wait time.Duration) (release func() error, err error) {
	//portlint:allow ctxbackground the wait is bounded by its argument; Pi's takeProcessLock has no signal either
	ctx, cancel := context.WithTimeout(context.Background(), max(wait, time.Millisecond))
	defer cancel()
	lock, err := workers.ProcessLock(name, ctx)
	if err != nil {
		return nil, fmt.Errorf("%s is held by another process: %w", name, err)
	}
	return lock.Release, nil
}
