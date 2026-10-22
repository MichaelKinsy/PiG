package changeobservers

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

// Observers run in registration order, a removed one is not told again, and a notification never allocates, so an owner can report a change from a lock it holds.
func TestObserversRunInRegistrationOrderUntilRemoved(t *testing.T) {
	var observers Observers
	var order []string
	stopFirst := observers.Add(func() { order = append(order, "first") })
	stopSecond := observers.Add(func() { order = append(order, "second") })
	observers.Add(func() { order = append(order, "third") })
	observers.Notify()
	if want := []string{"first", "second", "third"}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	order = nil
	stopSecond()
	stopSecond()
	observers.Notify()
	if want := []string{"first", "third"}; !slices.Equal(order, want) {
		t.Fatalf("order after removing the second = %v, want %v", order, want)
	}
	stopFirst()
	if allocs := testing.AllocsPerRun(1000, observers.Notify); allocs != 0 {
		t.Fatalf("Notify allocated %v times", allocs)
	}
}

// A registration while other goroutines notify is safe: run with -race.
func TestObserversRegisterWhileNotifying(t *testing.T) {
	var observers Observers
	var calls atomic.Int64
	stop := make(chan struct{})
	var notifiers sync.WaitGroup
	for range 4 {
		notifiers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					observers.Notify()
				}
			}
		})
	}
	for range 200 {
		remove := observers.Add(func() { calls.Add(1) })
		remove()
	}
	close(stop)
	notifiers.Wait()
}
