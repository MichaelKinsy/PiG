package closure

import "testing"

// heavySlots bounds the heavy tests that run at once. Each imports the whole
// repository's denominators or builds a store past the 64 MiB import limit,
// and one holds about 1.2 GB under -race. Run one after another, they exceed
// go test's default ten-minute timeout under -race.
var heavySlots = make(chan struct{}, 4)

// parallelHeavy runs a heavy test in parallel with the other heavy tests, at
// most cap(heavySlots) of them at a time. It changes when the test runs, not
// what it checks.
func parallelHeavy(t *testing.T) {
	t.Helper()
	t.Parallel()
	heavySlots <- struct{}{}
	t.Cleanup(func() { <-heavySlots })
}
