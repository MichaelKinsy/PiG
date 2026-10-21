package sdk

import (
	"reflect"
	"sync"
	"testing"
)

// orderRecorder lists the names of the steps in the order they ran.
type orderRecorder struct {
	mu    sync.Mutex
	steps []string
}

func (r *orderRecorder) add(step string) {
	r.mu.Lock()
	r.steps = append(r.steps, step)
	r.mu.Unlock()
}

func (r *orderRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.steps...)
}

// parkedCounter counts the callers a gate held, and tells a test each time one starts to wait.
func parkedCounter() (hook func(), parked chan struct{}) {
	parked = make(chan struct{}, 16)
	return func() {
		select {
		case parked <- struct{}{}:
		default:
		}
	}, parked
}

// Pi starts every call of a batch in source order and each reaches its first await, usually a dialog, before the next call starts, so the dialogs
// of a batch open in call order. The first dialog of each request is written only after the first dialog of every earlier request was written.
func TestToolDialogsOpenInCallOrder(t *testing.T) {
	var order toolStartOrder
	hook, parked := parkedCounter()
	order.parked = hook
	starts := []*toolStart{order.reserve(), order.reserve(), order.reserve()}
	var rec orderRecorder
	var done sync.WaitGroup
	open := func(i int) {
		done.Go(func() {
			starts[i].beforeCall()
			rec.add(string(rune('a' + i)))
			starts[i].afterCall()
			// A second dialog of the same request does not wait again.
			starts[i].beforeCall()
			starts[i].afterCall()
			starts[i].release()
		})
	}
	// The later requests reach their dialogs first: each is held until the one before it opened its own.
	open(2)
	<-parked
	open(1)
	<-parked
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("dialogs %v opened before the first request's", got)
	}
	open(0)
	done.Wait()
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(rec.list(), want) {
		t.Fatalf("dialogs opened in order %v, want call order %v", rec.list(), want)
	}
}

// A request that ends without a dialog hands its place on, also one that failed before its handler ran.
func TestToolDialogOrderPassesAnEndedRequest(t *testing.T) {
	var order toolStartOrder
	hook, parked := parkedCounter()
	order.parked = hook
	first, second, third := order.reserve(), order.reserve(), order.reserve()
	var rec orderRecorder
	var done sync.WaitGroup
	done.Go(func() {
		third.beforeCall()
		rec.add("third")
		third.afterCall()
	})
	<-parked
	done.Go(func() {
		second.beforeCall()
		rec.add("second")
		second.afterCall()
	})
	<-parked
	// first ends with no handler and no dialog.
	first.release()
	done.Wait()
	if want := []string{"second", "third"}; !reflect.DeepEqual(rec.list(), want) {
		t.Fatalf("dialogs opened in order %v, want %v", rec.list(), want)
	}
}

// Pi's handler that awaits an exec before its first dialog has yielded the thread, so the later calls' dialogs open while its exec runs. The first host call of any kind
// passes the dialog place on; a request whose first call is not a dialog must not hold the first host calls of the requests behind it until the handler returns.
func TestToolFirstHostCallOfAnyKindPassesTheDialogPlace(t *testing.T) {
	var order toolStartOrder
	first, second, third := order.reserve(), order.reserve(), order.reserve()
	order.parked = func() {
		t.Error("a later call waited for the dialog place of a request whose exec is in flight")
		first.release() // lets the held call return, so the test ends instead of hanging
	}
	second.parked, third.parked = order.parked, order.parked
	// first writes an exec, which is non-blocking for the later calls, and then its handler keeps running.
	first.beforeCall()
	first.afterCall()
	// The later calls reach their first host call, a dialog, and open it while first's exec runs.
	second.beforeCall()
	second.afterCall()
	third.beforeCall()
	third.afterCall()
	// first's own dialog, after its exec, does not wait again.
	first.beforeCall()
	first.afterCall()
}

// Dialogs answered back to back resume their handlers in the order of the answers: the response of a handler whose answer the reader read first
// is written first, though the handler that was answered second reaches its response first. A handler that continues to another host call has
// reached its next await and no longer holds the later ones.
func TestToolResponsesFollowTheOrderTheAnswersWereRead(t *testing.T) {
	var order toolStartOrder
	hook, parked := parkedCounter()
	order.parked = hook
	first, second, third := order.reserve(), order.reserve(), order.reserve()
	// The reader reads the answers of second, third and first, in that order.
	second.responseSlot().settle()
	third.responseSlot().settle()
	first.responseSlot().settle()
	var rec orderRecorder
	var done sync.WaitGroup
	respond := func(name string, start *toolStart) {
		done.Go(func() { start.respond(func() { rec.add(name) }) })
	}
	respond("first", first)
	<-parked
	respond("third", third)
	<-parked
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("responses %v were written before the handler answered first", got)
	}
	respond("second", second)
	done.Wait()
	if want := []string{"second", "third", "first"}; !reflect.DeepEqual(rec.list(), want) {
		t.Fatalf("responses written in order %v, want the order of the answers %v", rec.list(), want)
	}

	// A handler that went on to another host call lets the ones behind it respond.
	var order2 toolStartOrder
	hook2, parked2 := parkedCounter()
	order2.parked = hook2
	a, b := order2.reserve(), order2.reserve()
	a.responseSlot().settle()
	b.responseSlot().settle()
	var rec2 orderRecorder
	done.Go(func() { b.respond(func() { rec2.add("b") }) })
	<-parked2
	a.beforeCall()
	done.Wait()
	if want := []string{"b"}; !reflect.DeepEqual(rec2.list(), want) {
		t.Fatalf("responses %v, want b to respond once a reached its next host call", rec2.list())
	}
	// A handler that settled no slot responds at once.
	c := order2.reserve()
	c.respond(func() { rec2.add("c") })
	if want := []string{"b", "c"}; !reflect.DeepEqual(rec2.list(), want) {
		t.Fatalf("responses %v, want c to respond without waiting", rec2.list())
	}
}

// A dialog a retained Context opens after its request ended gets no response slot: a slot nothing closes would hold every handler that settles after it.
func TestToolRetainedContextDialogAfterTheRequestEndedLeavesNoSlot(t *testing.T) {
	var order toolStartOrder
	ended, later := order.reserve(), order.reserve()
	ended.release()
	if slot := ended.responseSlot(); slot != nil {
		t.Fatal("a request that ended made a response slot")
	}
	// Its first dialog does not wait for a place it passed, whatever the requests behind it do.
	ended.beforeCall()
	ended.afterCall()
	later.responseSlot().settle()
	done := make(chan struct{})
	go func() {
		later.respond(func() {})
		close(done)
	}()
	<-done
}

// The orders are one batch's: a request of another batch, or one that names no batch, is in none of them, so a long request of one batch never holds a dialog of another
// (a subagent's tool calls reach the same connection while the tool that runs the subagent has not returned).
func TestToolBatchOrdersAreIndependentAcrossBatches(t *testing.T) {
	var orders toolBatchOrders
	long := orders.reserve("batch-a")  // never makes a host call and does not end
	other := orders.reserve("batch-b") // arrived after it, and is in another batch
	bare := orders.reserve("")
	if bare != nil {
		t.Fatal("a request that names no batch took a place")
	}
	other.beforeCall() // would block forever behind long if the batches shared an order
	other.afterCall()
	bare.beforeCall()
	bare.respond(func() {})

	// Within a batch the later request waits.
	second := orders.reserve("batch-a")
	hook, parked := parkedCounter()
	orders.mu.Lock()
	orders.batches["batch-a"].order.parked = hook
	orders.mu.Unlock()
	second.parked = hook
	opened := make(chan struct{})
	go func() {
		second.beforeCall()
		close(opened)
	}()
	<-parked
	long.release()
	<-opened

	// A batch is forgotten when its last request ended; a later request of it starts a new order.
	second.release()
	other.release()
	orders.mu.Lock()
	left := len(orders.batches)
	orders.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d batches left after every request ended", left)
	}
}
