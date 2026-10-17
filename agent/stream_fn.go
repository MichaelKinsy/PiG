package agent

import (
	"errors"
	"sync"
)

// ErrNoDefaultStreamFunction is Pi's getDefaultStreamFn failure: no stream function was passed and none is configured.
var ErrNoDefaultStreamFunction = errors.New("No default stream function configured. Pass streamFn explicitly or call setDefaultStreamFn().")

// Ports packages/agent/src/stream-fn.ts.
var defaultStream struct {
	sync.RWMutex
	fn StreamFn
}

// SetDefaultStreamFn configures the stream used by newly constructed agents
// when their caller omits StreamFn. Nil removes the configured fallback.
func SetDefaultStreamFn(fn StreamFn) {
	defaultStream.Lock()
	defaultStream.fn = fn
	defaultStream.Unlock()
}

// GetDefaultStreamFn returns the configured host stream function, or an error
// when no host has installed one.
func GetDefaultStreamFn() (StreamFn, error) {
	defaultStream.RLock()
	defer defaultStream.RUnlock()
	if defaultStream.fn == nil {
		return nil, ErrNoDefaultStreamFunction
	}
	return defaultStream.fn, nil
}
