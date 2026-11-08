package state_test

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Own test (Pi has one thread, so `assertOutside` can only run inside or outside a commit): in Go a caller's goroutine may
// ask while the host's loop runs a commit. The check is a section of the host like the call it guards, so it waits for the
// commit to end: reading the flag from the caller's goroutine returned a spurious `invalid/reentrant` (and raced the loop's
// write of it).
func TestAssertOutsideFromACallersGoroutineWaitsForTheCommitRunningOnTheLoop(t *testing.T) {
	var loop sync.Mutex
	batch := state.NewBatch(func(state.BatchError) {}, nil)
	batch.UseSection(func(work func()) {
		loop.Lock()
		defer loop.Unlock()
		work()
	}, nil)
	inside, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		batch.Section(func() {
			_ = batch.Run(func() error {
				close(inside)
				<-release
				return nil
			})
		})
	}()
	<-inside
	asked := make(chan error, 1)
	go func() { asked <- batch.AssertOutside("watch") }()
	close(release)
	<-done
	if err := <-asked; err != nil {
		t.Fatalf("AssertOutside = %v, want nil: the commit had ended when the caller's check ran", err)
	}
}
