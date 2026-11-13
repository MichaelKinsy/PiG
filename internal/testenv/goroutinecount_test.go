package testenv

import (
	"sync"
	"testing"
	"testing/synctest"
)

// A goroutine outside the caller's bubble that starts between two counts is not in their difference, as the frame timer
// of another test's renderer is not; a goroutine of the bubble is, until it has ended.
func TestLiveGoroutinesCountsTheCallersBubble(t *testing.T) {
	// The channels are made outside the bubble, so the goroutines outside it may use them.
	spawn, spawned, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var outside sync.WaitGroup
	outside.Go(func() {
		<-spawn
		outside.Go(func() {
			close(spawned)
			<-release
		})
		<-release
	})
	defer outside.Wait()
	defer close(release)
	synctest.Test(t, func(t *testing.T) {
		before := LiveGoroutines()
		if before < 1 {
			t.Fatalf("the bubble holds %d goroutines, want the caller at least", before)
		}
		close(spawn)
		<-spawned
		if n := LiveGoroutines() - before; n != 0 {
			t.Errorf("a goroutine outside the bubble started between two counts and their difference is %d, want 0", n)
		}
		stop := make(chan struct{})
		go func() { <-stop }()
		synctest.Wait()
		if n := LiveGoroutines() - before; n != 1 {
			t.Errorf("a goroutine of the bubble started between two counts and their difference is %d, want 1", n)
		}
		close(stop)
		synctest.Wait()
		if n := LiveGoroutines() - before; n != 0 {
			t.Errorf("the goroutine of the bubble ended and the difference is %d, want 0", n)
		}
	})
}
