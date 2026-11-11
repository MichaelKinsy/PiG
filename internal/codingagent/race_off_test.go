//go:build !race

package codingagent

// exitRaceChildrenAtOnce does nothing without the race detector: only a binary built with -race sleeps before it exits.
func exitRaceChildrenAtOnce() error { return nil }
