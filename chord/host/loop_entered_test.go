package host

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func enteredWithin(entered <-chan struct{}, d time.Duration) bool {
	select {
	case <-entered:
		return true
	case <-time.After(d):
		return false
	}
}

// A task that has to wait for the host loop has entered: the loop may be the one waiting for it (fast path, no
// polling; a sync.Mutex wait is not a wait for the slow path, so only the fast path can see it).
func TestGoEnteredWaitingForTheLoopEnters(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	t.Cleanup(func() { _ = host.Close() })
	held, release := make(chan struct{}), make(chan struct{})
	go host.run(func() {
		close(held)
		<-release
	})
	<-held
	entered := host.goEntered(func() { host.run(func() {}) })
	entered5s := enteredWithin(entered, 5*time.Second)
	close(release)
	if !entered5s {
		t.Fatal("a task waiting for the loop did not enter")
	}
}

// A host call that does not wait (the loop is free) is synchronous work, as upstream's host calls: the task has not
// entered while it keeps running after it.
func TestGoEnteredUncontendedHostCallIsWork(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	t.Cleanup(func() { _ = host.Close() })
	var stop atomic.Bool
	called := make(chan struct{})
	entered := host.goEntered(func() {
		host.run(func() {})
		close(called)
		for !stop.Load() {
		}
	})
	<-called
	early := enteredWithin(entered, 20*time.Millisecond)
	stop.Store(true)
	if early {
		t.Fatal("an uncontended host call entered the task")
	}
	if !enteredWithin(entered, 5*time.Second) {
		t.Fatal("the task did not enter when it returned")
	}
}

// An upstream function cannot block on a lock, so a sync.Mutex wait is synchronous work, not a wait; a channel wait
// is (slow path).
func TestGoEnteredMutexWaitIsWorkChannelWaitIsAWait(t *testing.T) {
	host := CreateHost(HostOptions{Self: kindHost()})
	t.Cleanup(func() { _ = host.Close() })
	var mu sync.Mutex
	mu.Lock()
	locking := make(chan struct{})
	entered := host.goEntered(func() {
		close(locking)
		mu.Lock()
		//lint:ignore SA2001 the task parks on the held lock; that wait is the test.
		mu.Unlock() //nolint:staticcheck // SA2001: the task parks on the held lock; that wait is the test
	})
	<-locking
	early := enteredWithin(entered, 20*time.Millisecond)
	mu.Unlock()
	if early {
		t.Fatal("a mutex wait entered the task")
	}
	if !enteredWithin(entered, 5*time.Second) {
		t.Fatal("the task did not enter when it returned")
	}
	release := make(chan struct{})
	waiting := host.goEntered(func() { <-release })
	waited := enteredWithin(waiting, 5*time.Second)
	close(release)
	if !waited {
		t.Fatal("a channel wait did not enter the task")
	}
}
