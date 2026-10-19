package host

// Hosts in one process run on separate loops, where Pi's one event loop runs every host's notifications in turn
// (publish.ts keeps one module-level queue). A host's notifications are delivered by a section of its own loop, and a
// hold on another host's publications neither delays them nor lets that host's goroutine deliver them: a notification
// that ran on the goroutine of a drain it did not belong to touched the host's connections and refs while the host's
// own loop was running them (two senders on one Connection: a data race on its flow-control counters).

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// withPublicationsHeld holds host's publications, in a section of its loop on another goroutine, while during runs.
func withPublicationsHeld(host *HostImpl, during func()) {
	held, release, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ended)
		host.run(func() {
			holdPublications(&host.loop, func() struct{} {
				close(held)
				<-release
				return struct{}{}
			})
		})
	}()
	<-held
	defer func() {
		close(release)
		<-ended
	}()
	during()
}

var publishLoopsStore = Local("t.publish-loops", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
	{"n", tUnknown()},
	{"add", Method(tNumber(), nil)},
}})

// publishLoopsFixture is a host a serving a live object (state n, method add) and an unrelated host b.
func publishLoopsFixture(t *testing.T) (a, b *HostImpl, ref Ref) {
	t.Helper()
	a = requestsHost(t, HostOptions{Name: "a"})
	b = requestsHost(t, HostOptions{Name: "b"})
	requestsServe(t, a.Serve, publishLoopsStore, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("n", n, "add", methodFunc(func(amount any, ctx Ctx) (any, error) {
			return nil, n.ChangeValue(ctx, func(value any) any { return value.(float64) + amount.(float64) })
		})), nil
	}, ServeOptions{})
	ref = reviewGet(t, a, publishLoopsStore, map[string]any{}, nil)
	reviewReady(t, ref)
	return a, b, ref
}

// requireDeliveredOnLoop fails unless the notification a made while b held its publications was delivered by now, in a
// section of a's loop.
func requireDeliveredOnLoop(t *testing.T, delivered <-chan bool) {
	t.Helper()
	select {
	case onLoop := <-delivered:
		if !onLoop {
			t.Error("the notification was delivered outside its host's loop")
		}
	default:
		t.Error("the notification waited for another host's hold")
	}
}

// publish.ts enqueue: a task enqueued on a loop is delivered by that loop, at once, whatever another loop holds.
func TestPublicationsOfAHostAreDeliveredOnItsLoopWhileAnotherHostHoldsItsPublications(t *testing.T) {
	a, b, _ := publishLoopsFixture(t)
	delivered := make(chan bool, 1)
	withPublicationsHeld(b, func() {
		a.run(func() { enqueue(&a.loop, func() { delivered <- a.onLoop() }) })
		requireDeliveredOnLoop(t, delivered)
	})
}

// A state write is a cell publication: its observers hear of it in the write's own section.
func TestStateEventsOfAHostsObjectAreDeliveredOnItsLoopWhileAnotherHostHoldsItsPublications(t *testing.T) {
	a, b, ref := publishLoopsFixture(t)
	delivered := make(chan bool, 4)
	first := make(chan struct{})
	snapshot := true
	stop := requestsReadable(t, ref, "n").Events(func(StateEvent[any]) {
		if snapshot {
			snapshot = false
			close(first)
			return
		}
		delivered <- a.onLoop()
	})
	t.Cleanup(stop)
	<-first
	withPublicationsHeld(b, func() {
		mustCall(t, ref, "add", float64(1), nil)
		requireDeliveredOnLoop(t, delivered)
	})
}

// A reference's status notification (RefImpl.statusChanged).
func TestStatusOfAHostsRefIsDeliveredOnItsLoopWhileAnotherHostHoldsItsPublications(t *testing.T) {
	a, b, ref := publishLoopsFixture(t)
	delivered := make(chan bool, 4)
	ref.OnStatus(func(RefStatus, error) { delivered <- a.onLoop() })
	withPublicationsHeld(b, func() {
		ref.Release()
		requireDeliveredOnLoop(t, delivered)
	})
}

// A reference's shape push (RefImpl.shapeChanged), the notification a connection sends a unit for.
func TestShapeOfAHostsRefIsDeliveredOnItsLoopWhileAnotherHostHoldsItsPublications(t *testing.T) {
	a, b, ref := publishLoopsFixture(t)
	delivered := make(chan bool, 4)
	ref.OnShape(func(Shape) { delivered <- a.onLoop() })
	withPublicationsHeld(b, func() {
		a.run(func() { ref.(*RefImpl).shapeChanged() })
		requireDeliveredOnLoop(t, delivered)
	})
}

// publish.ts is one queue: a write that a notification makes to another host's state is queued behind the rest of the
// notification being delivered, as one made to its own host's state is, not delivered inside it.
func TestAStateWriteFromAnotherHostsNotificationIsDeliveredAfterTheNotificationBeingDelivered(t *testing.T) {
	a := requestsHost(t, HostOptions{Name: "a"})
	b := requestsHost(t, HostOptions{Name: "b"})
	cells := map[*HostImpl]*Cell[any]{}
	var mu sync.Mutex
	for _, host := range []*HostImpl{a, b} {
		requestsServe(t, host.Serve, publishLoopsStore, func(_ any, _ Principal, self Self) (Implementation, error) {
			n, err := self.State("n", tNumber(), float64(0), nil)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			cells[host] = n
			mu.Unlock()
			return chordjson.ObjectOf("n", n, "add", methodFunc(func(amount any, ctx Ctx) (any, error) {
				return nil, n.ChangeValue(ctx, func(value any) any { return value.(float64) + amount.(float64) })
			})), nil
		}, ServeOptions{})
	}
	refA := reviewGet(t, a, publishLoopsStore, map[string]any{}, nil)
	refB := reviewGet(t, b, publishLoopsStore, map[string]any{}, nil)
	reviewReady(t, refA, refB)
	mu.Lock()
	cellA, cellB := cells[a], cells[b]
	mu.Unlock()
	var order []string
	var writeErr error
	note := func(entry string) {
		mu.Lock()
		order = append(order, entry)
		mu.Unlock()
	}
	changed := func(note func()) func(any) {
		return func(value any) {
			if value == float64(1) {
				note()
			}
		}
	}
	t.Cleanup(cellA.Subscribe(changed(func() {
		note("a1")
		_, writeErr = callMember(refB, "add", float64(1), nil)
	})))
	t.Cleanup(cellA.Subscribe(changed(func() { note("a2") })))
	t.Cleanup(cellB.Subscribe(changed(func() { note("b") })))
	mustCall(t, refA, "add", float64(1), nil)
	mu.Lock()
	defer mu.Unlock()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if want := []string{"a1", "a2", "b"}; !slices.Equal(order, want) {
		t.Fatalf("delivery order %v, want %v", order, want)
	}
}
