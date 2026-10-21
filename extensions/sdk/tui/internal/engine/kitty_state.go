package engine

import "sync/atomic"

var kittyProtocolActive atomic.Bool

// SetKittyProtocolActive mirrors upstream keys.ts global Kitty state.
func SetKittyProtocolActive(active bool) { kittyProtocolActive.Store(active) }

// IsKittyProtocolActive reports the global Kitty keyboard protocol state.
func IsKittyProtocolActive() bool { return kittyProtocolActive.Load() }
