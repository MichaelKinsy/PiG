package testenv

import (
	"os"
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"
	"time"
)

// A counter read in its own process does not see goroutines the package's process creates: here, a timer loop left running, as a test that does not stop what it started leaves one.
func TestInOwnProcessCountsOnlyTheTestsGoroutines(t *testing.T) {
	if os.Getenv(ownProcessEnv) != t.Name() {
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				done := make(chan struct{})
				time.AfterFunc(time.Millisecond, func() { close(done) })
				<-done
			}
		})
		defer wg.Wait()
		defer close(stop)
	}
	if !InOwnProcess(t) {
		return
	}
	runtime.GC() // the first collection starts the GC's worker goroutines; start them before the window
	sample := []metrics.Sample{{Name: "/sched/goroutines-created:goroutines"}}
	metrics.Read(sample)
	before := sample[0].Value.Uint64()
	time.Sleep(50 * time.Millisecond) // a window in which the parent's timer loop creates a goroutine every millisecond
	metrics.Read(sample)
	if created := sample[0].Value.Uint64() - before; created != 0 {
		t.Fatalf("%d goroutines created in a test that creates none", created)
	}
}
