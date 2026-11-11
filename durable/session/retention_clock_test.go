package session_test

import (
	"errors"
	"sync/atomic"
)

// retentionClock is a controllable wall clock, shared by the retention and the deferred-commit tests.
type retentionClock struct {
	now  atomic.Int64
	fail atomic.Bool
}

func (clock *retentionClock) read() float64 {
	if clock.fail.Load() {
		panic(errors.New("clock"))
	}
	return float64(clock.now.Load())
}

func (clock *retentionClock) advance(ms int64) { clock.now.Add(ms) }
