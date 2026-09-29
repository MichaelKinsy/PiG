package subprocess

import (
	"sync"
	"testing"
	"time"
)

// A blocked n=1 delivery must not be overtaken by the n=0 that ends the flight.
func TestCommandSuspendedCountsAreDeliveredInOrder(t *testing.T) {
	h := &Host{}
	var mu sync.Mutex
	var got []int
	entered := make(chan struct{})
	release := make(chan struct{})
	h.SetCommandSuspendHandler(func(n int) {
		if n == 1 {
			close(entered)
			<-release
		}
		mu.Lock()
		got = append(got, n)
		mu.Unlock()
	})
	f := &commandFlight{}
	h.commandFlightMu.Lock()
	h.commandFlights = map[*commandFlight]struct{}{f: {}}
	h.commandFlightMu.Unlock()

	done := make(chan struct{})
	go func() { h.setCommandSuspended(f, true); close(done) }()
	<-entered
	ended := make(chan struct{})
	go func() { h.endCommandFlight(f); close(ended) }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-done
	<-ended
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Fatalf("deliveries = %v, want [1 0]", got)
	}
}
