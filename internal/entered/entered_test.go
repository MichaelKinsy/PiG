package entered

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

func TestGoWaitsUntilTheGoroutineParksOrReturns(t *testing.T) {
	release := make(chan struct{})
	Go(func() { <-release })() // returns once the goroutine waits on release
	close(release)

	var ran atomic.Bool
	Go(func() { ran.Store(true) })()
	if !ran.Load() {
		t.Fatal("wait returned before a function with no wait ran to its end")
	}
}

func TestAwaitReturnsOnceAWokenGoroutineWaitsAgain(t *testing.T) {
	ids := make(chan uint64, 1)
	wake, again := make(chan struct{}), make(chan struct{})
	var resumed atomic.Bool
	go func() {
		ids <- goroutineid.Current()
		<-wake
		resumed.Store(true)
		<-again
	}()
	id := <-ids
	close(wake)
	Await(id, nil)
	if !resumed.Load() {
		t.Fatal("Await returned before the woken goroutine ran to its next wait")
	}
	close(again)
}

func TestParkedReportsAnExitedGoroutineAsNotFound(t *testing.T) {
	ids := make(chan uint64, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ids <- goroutineid.Current()
	}()
	id := <-ids
	<-done
	for range 100 {
		if _, found, _ := Parked(id, nil); !found {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("an exited goroutine is still found")
}

// A wait observed while busy reports true is not the goroutine's own: AwaitUnless keeps polling until busy clears.
func TestAwaitUnlessIgnoresWaitsWhileBusy(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	release := make(chan struct{})
	ids := make(chan uint64, 1)
	go func() {
		ids <- goroutineid.Current()
		<-release
	}()
	id := <-ids
	returned := make(chan struct{})
	go func() {
		AwaitUnless(id, nil, busy.Load)
		close(returned)
	}()
	select {
	case <-returned:
		t.Fatal("AwaitUnless returned while busy")
	case <-time.After(20 * time.Millisecond):
	}
	busy.Store(false)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("AwaitUnless did not return once the goroutine's wait became its own")
	}
	close(release)
}
