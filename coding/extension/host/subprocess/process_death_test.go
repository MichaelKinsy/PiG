//go:build unix && !pig_strip_node_extensions

package subprocess

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The end of a process reaches the host more than once: as the closed connection of each extension that ran in it and, for a packed
// process, as the reaped process its watcher sees. Which arrives first is the machine's choice. The host reports one end once for each
// extension, the same notices in the same order whichever it saw first (docs/runtime-cell-reload.md: a socket close and a process exit
// claim the same failed member only once). These tests put the two in each order through the host's death gate.

// deathOrder is an order in which a host sees the end of a packed process.
type deathOrder struct {
	name string
	// first is how many of n members' closed connections, in load order, the host sees before the reaped process. It sees the others'
	// after the watcher has acted.
	first func(n int) int
}

func deathOrders() []deathOrder {
	return []deathOrder{
		{name: "every connection, then the process", first: func(n int) int { return n }},
		{name: "one connection, the process, then the others", first: func(int) int { return 1 }},
		{name: "the process, then every connection", first: func(int) int { return 0 }},
	}
}

// deathObservers is a test's end of a host's death gate. Armed, it counts the observers of a process's end that have acted, and it holds
// each until its turn in the order, if there is one.
type deathObservers struct {
	// armed is set when the process is about to be killed: a connection a reload ended before passes unseen.
	armed atomic.Bool
	// ordered is set with an order; first then names the members whose closed connection the host sees before the reaped process.
	ordered bool
	first   map[string]bool
	mu      sync.Mutex
	// pending counts the members of first whose loop has not acted; firstActed closes when none is left, watcherActed when the watcher has acted.
	pending      int
	firstActed   chan struct{}
	watcherActed chan struct{}
	closeFirst   sync.Once
	closeWatcher sync.Once
	// acted receives each observer that has acted.
	acted chan deathObserver
}

// newDeathObservers sets the gate of w's host, before the host starts a goroutine.
func newDeathObservers(t *testing.T, w *waiterHost, order *deathOrder) *deathObservers {
	t.Helper()
	o := &deathObservers{firstActed: make(chan struct{}), watcherActed: make(chan struct{}), acted: make(chan deathObserver, 64)}
	if order != nil {
		o.ordered = true
		o.first = map[string]bool{}
		for _, cfg := range w.configs[:order.first(len(w.configs))] {
			o.first[cfg.Name] = true
		}
		o.pending = len(o.first)
	}
	if o.pending == 0 {
		o.closeFirst.Do(func() { close(o.firstActed) })
	}
	w.host.deathGate = o.gate
	// A test that failed leaves no observer held.
	t.Cleanup(func() {
		o.closeFirst.Do(func() { close(o.firstActed) })
		o.closeWatcher.Do(func() { close(o.watcherActed) })
	})
	return o
}

func (o *deathObservers) gate(seenBy deathObserver, name string) func() {
	if !o.armed.Load() {
		return func() {}
	}
	early := seenBy == deathSeenByConnection && o.first[name]
	switch {
	case !o.ordered, early:
	case seenBy == deathSeenByWatcher:
		<-o.firstActed
	default:
		<-o.watcherActed
	}
	return func() {
		switch {
		case seenBy == deathSeenByWatcher:
			o.closeWatcher.Do(func() { close(o.watcherActed) })
		case early:
			o.mu.Lock()
			o.pending--
			last := o.pending == 0
			o.mu.Unlock()
			if last {
				o.closeFirst.Do(func() { close(o.firstActed) })
			}
		}
		o.acted <- seenBy
	}
}

// await waits until the observers of the process's end have acted: so many connection loops and watchers. A watcher that restarts a
// cell has acted when the cell runs again.
func (o *deathObservers) await(t *testing.T, connections, watchers int) {
	t.Helper()
	budget := time.NewTimer(testbudget.Wait(t))
	defer budget.Stop()
	for connections > 0 || watchers > 0 {
		select {
		case seenBy := <-o.acted:
			if seenBy == deathSeenByWatcher {
				watchers--
			} else {
				connections--
			}
		case <-budget.C:
			t.Fatalf("the end of the process was not handled within the wait budget (%s): %d connection loops and %d watchers have yet to act", testbudget.Env, connections, watchers)
		}
	}
	if connections < 0 || watchers < 0 {
		t.Errorf("more observers acted on the end of the process than it has: %d connection loops and %d watchers too many", -connections, -watchers)
	}
	o.armed.Store(false)
}

// deathFixtures are the waiter fixtures, a Go extension alone in its process, and a cell of each other SDK whose factories share a
// process of their language.
func deathFixtures() []waiterFixture {
	return append(waiterFixtures(),
		// A Go factory alone in its process is a cell of one: it stops as a cell does.
		waiterFixture{name: "go isolated", death: deathStopsCell, packed: true, configs: func(t *testing.T, _ int) []ExtConfig {
			cfg := packedFactoryConfig("death-go-lone", writePackedFactoryModule(t, "example.com/death/lone", "death-go-lone", "death_go_lone_tool"), "example.com/death/lone", "death-go-lone")
			cfg.Isolation = "strict"
			return []ExtConfig{cfg}
		}},
		waiterFixture{name: "python packed", death: deathStopsCell, packed: true, configs: func(t *testing.T, _ int) []ExtConfig {
			return []ExtConfig{
				packedPythonFactoryConfig("death-py-b", writePackedPythonFactoryModule(t, "death_py_b", "death-py-b", "tool_b"), "death_py_b", "death-py-b"),
				packedPythonFactoryConfig("death-py-a", writePackedPythonFactoryModule(t, "death_py_a", "death-py-a", "tool_a"), "death_py_a", "death-py-a"),
			}
		}},
		waiterFixture{name: "rust packed", death: deathStopsCell, packed: true, configs: func(t *testing.T, _ int) []ExtConfig {
			return []ExtConfig{
				packedRustFactoryConfig("death-rust-b", writePackedRustFactoryCrate(t, "death_rust_b", "death-rust-b", "tool_b"), "death_rust_b", "death-rust-b"),
				packedRustFactoryConfig("death-rust-a", writePackedRustFactoryCrate(t, "death_rust_a", "death-rust-a", "tool_a"), "death_rust_a", "death-rust-a"),
			}
		}},
	)
}

// TestProcessDeathIsReportedOnceInEitherOrder: a killed process is reported once for each extension that ran in it, in load order,
// whichever of its closed connections and its exit the host sees first. A Node extension alone in its process has one observer, its
// connection loop, which waits for the process itself.
func TestProcessDeathIsReportedOnceInEitherOrder(t *testing.T) {
	for _, fixture := range deathFixtures() {
		t.Run(fixture.name, func(t *testing.T) {
			if !fixture.packed {
				if got, want := processDeathReports(t, fixture, 0, nil); !slices.Equal(got, want) {
					t.Errorf("the death of the process is reported as\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
				}
				return
			}
			for _, order := range deathOrders() {
				t.Run(order.name, func(t *testing.T) {
					if got, want := processDeathReports(t, fixture, 0, &order); !slices.Equal(got, want) {
						t.Errorf("the death of the process is reported as\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
					}
				})
			}
		})
	}
}
