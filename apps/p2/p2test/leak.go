package p2test

import "testing"

// NoLeakedGoroutines fails the test when goroutines its body started outlive its cleanups: call it first, so its cleanup
// runs last, after the hosts and fixtures the test registered closed. It is CheckLeaks (identities, not counts; a pooled
// idle handler coroutine is ignored only when its stack holds no plugin code), waiting 10 s at most.
func NoLeakedGoroutines(t testing.TB) {
	t.Helper()
	CheckLeaks(t, 0)
}
