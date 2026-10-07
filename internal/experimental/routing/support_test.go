package routing_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/experimental/routing"
	"github.com/MichaelKinsy/PiG/internal/experimental/routing/routingtest"
)

const testServerID = "00000000-0000-4000-8000-000000000001"

// waitContext bounds one test's protocol waits, as the upstream test timeout does.
func waitContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// newTestServerHost is upstream new TestServerHost() for cases that need no seeded Session.
func newTestServerHost() routing.ServerHost {
	return routingtest.NewTestServerHost().ServerHost()
}

// pollUntil is upstream expect.poll: it re-evaluates a condition on a short interval until it holds or the test's bound expires.
func pollUntil(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline:
			t.Fatalf("condition never held: %s", label)
		}
	}
}

// goroutinesIn counts goroutines whose stack contains needle. It observes parked router goroutines that expose no other signal, the Go analogue of the microtask ordering upstream's single event loop provides.
func goroutinesIn(needle string) int {
	return goroutinesMatching(func(stack string) bool { return strings.Contains(stack, needle) })
}

func goroutinesMatching(match func(stack string) bool) int {
	buffer := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buffer, true)
		if n < len(buffer) {
			buffer = buffer[:n]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	count := 0
	for stack := range strings.SplitSeq(string(buffer), "\n\n") {
		if match(stack) {
			count++
		}
	}
	return count
}

// goroutinesBlockedIn counts goroutines that are blocked on a channel receive inside a function whose stack line contains needle.
func goroutinesBlockedIn(needle string) int {
	return goroutinesMatching(func(stack string) bool {
		header, _, _ := strings.Cut(stack, "\n")
		return strings.Contains(header, "[chan receive") && strings.Contains(stack, needle)
	})
}
