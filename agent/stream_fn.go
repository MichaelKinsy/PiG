package agent

import (
	"errors"
	"sync"
)

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
// when no host has installed one. Models with native provider runtimes can
// still stream directly when an agent has no explicit or configured stream.
func GetDefaultStreamFn() (StreamFn, error) {
	defaultStream.RLock()
	defer defaultStream.RUnlock()
	if defaultStream.fn == nil {
		return nil, errors.New("No default stream function configured. Pass streamFn explicitly or call setDefaultStreamFn().")
	}
	return defaultStream.fn, nil
}
