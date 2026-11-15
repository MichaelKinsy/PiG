//go:build unix

package workers

import (
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// pi: packages/chord/src/next-node/workers/process.ts
// A worker that fails right after it started tells `started`, then `fatal` with its reason, and exits once both are written
// (main.ts `#fatal`: `process.send(fatal, () => process.exit(1))`). Node emits what the child wrote on its channel before
// the child's `exit` event, so the component, which records the exit's reason from the `fatal` it heard before, shows the
// worker's own message. In Go the channel's reader and the exit's waiter are two goroutines: an exit told before the
// `fatal` was is recorded as the fixed `worker failed to start`, which is what
// TestFailuresStayAFatalCrossesRedactedTheSecretNeverReachesThePublicState saw (13 and 11 runs of 2,000 at -cpu 1 and 2 on
// main). This forces that interleaving: the reader is held on `started`, so the `fatal` is still unread when the child has
// ended and the waiter saw its exit; the bubble's Wait returns once everything the waiter can do while the reader is held
// was done. The exit is not told then, and is told after the `fatal` when the reader is released.
func TestAFatalTheChannelsReaderHasNotToldWhenTheWorkerEndedIsToldBeforeItsExit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var told []string
		note := func(what string) {
			mu.Lock()
			defer mu.Unlock()
			told = append(told, what)
		}
		seen := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(told)
		}
		held, release := make(chan struct{}), make(chan struct{})
		process, err := NewWorkerProcess(
			ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("lastwords"), OnOutput: func(string, []byte) {}},
			ProcessEvents{
				Control: func(message FromWorker) {
					if message.Kind == FromWorkerStarted {
						close(held)
						<-release
					}
					note("control " + string(message.Kind))
				},
				LinkLost:   func() {},
				LinkClosed: func() {},
				Exited:     func(Exit) { note("exit") },
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		<-held
		synctest.Wait()
		early := seen()
		close(release)
		<-process.Exited()
		if len(early) != 0 {
			t.Errorf("told while the reader holds the child's `fatal` unread: %v, want nothing: the exit waits for the child's last words", early)
		}
		if want := []string{"control started", "control fatal", "exit"}; !reflect.DeepEqual(seen(), want) {
			t.Errorf("told = %v, want %v: the `fatal` before the exit", seen(), want)
		}
	})
}
