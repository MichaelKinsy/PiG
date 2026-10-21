package jsloop

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// signal notes that a goroutine parked, without ever blocking the gate's lock when the note is already pending.
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// orderRecorder collects the names of the actions in the order they ran.
type orderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *orderRecorder) action(name string) func() {
	return func() {
		r.mu.Lock()
		r.order = append(r.order, name)
		r.mu.Unlock()
	}
}

func (r *orderRecorder) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// settleGateBehaviors is the behavior table of SettleGate. The root module and the SDK module each carry a verbatim copy of this
// file and of settle_gate.go (TestSDKCopy), so one table pins both implementations.
var settleGateBehaviors = []struct {
	name string
	run  func(t *testing.T)
}{
	{"actions run in settle order whatever order their goroutines reach Run in", func(t *testing.T) {
		var gate SettleGate
		parked := make(chan struct{}, 8)
		gate.SetParkedHook(func() { signal(parked) })
		first, second, third := gate.NewSlot(), gate.NewSlot(), gate.NewSlot()
		third.Settle()
		first.Settle()
		// second is never settled by a reader: it settles on reaching Run, behind both.
		var rec orderRecorder
		var done sync.WaitGroup
		done.Go(func() { second.Run(rec.action("second")) })
		awaitSignal(t, parked, "second waits for the slots that settled before it")
		done.Go(func() { first.Run(rec.action("first")) })
		done.Go(func() { third.Run(rec.action("third")) })
		done.Wait()
		if want := []string{"third", "first", "second"}; !reflect.DeepEqual(rec.names(), want) {
			t.Fatalf("actions ran in order %v, want the settle order %v", rec.names(), want)
		}
	}},
	{"every start order of the goroutines keeps the settle order", func(t *testing.T) {
		const n = 6
		for shift := range n {
			for _, reverse := range []bool{false, true} {
				var gate SettleGate
				slots := make([]*SettleSlot, n)
				for i := range slots {
					slots[i] = gate.NewSlot()
					slots[i].Settle()
				}
				var rec orderRecorder
				var done sync.WaitGroup
				for k := range n {
					i := (k + shift) % n
					if reverse {
						i = n - 1 - i
					}
					done.Go(func() { slots[i].Run(rec.action(fmt.Sprint(i))) })
				}
				done.Wait()
				want := make([]string, n)
				for i := range want {
					want[i] = fmt.Sprint(i)
				}
				if !reflect.DeepEqual(rec.names(), want) {
					t.Fatalf("shift %d reverse %v: actions ran in order %v, want %v", shift, reverse, rec.names(), want)
				}
			}
		}
	}},
	{"a settled slot closed without running releases the slots behind it", func(t *testing.T) {
		var gate SettleGate
		abandoned, waiting := gate.NewSlot(), gate.NewSlot()
		abandoned.Settle()
		parked := make(chan struct{}, 1)
		gate.SetParkedHook(func() { signal(parked) })
		ran := make(chan struct{})
		go waiting.Run(func() { close(ran) })
		awaitSignal(t, parked, "the slot waits behind the settled one")
		abandoned.Close()
		awaitSignal(t, ran, "the slot behind an abandoned one runs")
	}},
	{"settling twice keeps the first place", func(t *testing.T) {
		var gate SettleGate
		first, second := gate.NewSlot(), gate.NewSlot()
		first.Settle()
		second.Settle()
		first.Settle()
		var rec orderRecorder
		var done sync.WaitGroup
		done.Go(func() { second.Run(rec.action("second")) })
		first.Run(rec.action("first"))
		done.Wait()
		if want := []string{"first", "second"}; !reflect.DeepEqual(rec.names(), want) {
			t.Fatalf("actions ran in order %v, want %v", rec.names(), want)
		}
	}},
	{"a slot that never settled and reaches Run alone runs at once", func(t *testing.T) {
		var gate SettleGate
		var rec orderRecorder
		gate.NewSlot().Run(rec.action("only"))
		if want := []string{"only"}; !reflect.DeepEqual(rec.names(), want) {
			t.Fatalf("actions ran %v, want %v", rec.names(), want)
		}
	}},
	{"Close after Run and Run after Close do nothing to the slots behind", func(t *testing.T) {
		var gate SettleGate
		first, second := gate.NewSlot(), gate.NewSlot()
		var rec orderRecorder
		first.Settle()
		second.Settle()
		first.Run(rec.action("first"))
		first.Close()
		second.Run(rec.action("second"))
		closed := gate.NewSlot()
		closed.Close()
		closed.Run(rec.action("closed"))
		if want := []string{"first", "second", "closed"}; !reflect.DeepEqual(rec.names(), want) {
			t.Fatalf("actions ran in order %v, want %v", rec.names(), want)
		}
	}},
	{"an action that panics still hands the turn on", func(t *testing.T) {
		var gate SettleGate
		first, second := gate.NewSlot(), gate.NewSlot()
		first.Settle()
		second.Settle()
		ran := make(chan struct{})
		go second.Run(func() { close(ran) })
		func() {
			defer func() { _ = recover() }()
			first.Run(func() { panic("action failed") })
		}()
		awaitSignal(t, ran, "the slot behind a panicking action runs")
	}},
	{"gates do not order each other", func(t *testing.T) {
		var a, b SettleGate
		held := a.NewSlot()
		held.Settle()
		var rec orderRecorder
		b.NewSlot().Run(rec.action("other gate"))
		held.Close()
		if want := []string{"other gate"}; !reflect.DeepEqual(rec.names(), want) {
			t.Fatalf("actions ran %v, want %v", rec.names(), want)
		}
	}},
	{"every start order of the goroutines keeps the settle order of the starts", func(t *testing.T) {
		const n = 6
		for shift := range n {
			for _, reverse := range []bool{false, true} {
				var gate SettleGate
				slots := make([]*SettleSlot, n)
				for i := range slots {
					slots[i] = gate.NewSlot()
					slots[i].Settle()
				}
				var rec orderRecorder
				var done sync.WaitGroup
				for k := range n {
					i := (k + shift) % n
					if reverse {
						i = n - 1 - i
					}
					done.Go(func() {
						began := slots[i].Begin()
						rec.action(fmt.Sprint(i))()
						began()
					})
				}
				done.Wait()
				want := make([]string, n)
				for i := range want {
					want[i] = fmt.Sprint(i)
				}
				if !reflect.DeepEqual(rec.names(), want) {
					t.Fatalf("shift %d reverse %v: slots began in order %v, want %v", shift, reverse, rec.names(), want)
				}
			}
		}
	}},
	{"a slot's start waits for the earlier starts, not for the earlier actions", func(t *testing.T) {
		var gate SettleGate
		first, second := gate.NewSlot(), gate.NewSlot()
		first.Settle()
		second.Settle()
		secondBegan := make(chan struct{})
		go func() { second.Begin()(); close(secondBegan) }()
		began := first.Begin()
		select {
		case <-secondBegan:
			t.Fatal("the second slot began before the first reported its start")
		default:
		}
		began()
		awaitSignal(t, secondBegan, "the second slot begins once the first started")
		// The first slot's action has not run yet, and the second slot's start did not wait for it.
		ran := make(chan struct{})
		first.Run(func() { close(ran) })
		awaitSignal(t, ran, "the first slot's action")
	}},
	{"a settled slot closed without beginning releases the starts behind it", func(t *testing.T) {
		var gate SettleGate
		abandoned, waiting := gate.NewSlot(), gate.NewSlot()
		abandoned.Settle()
		waiting.Settle()
		began := make(chan struct{})
		go func() { waiting.Begin()(); close(began) }()
		select {
		case <-began:
			t.Fatal("a slot began before the slot that settled first started")
		default:
		}
		abandoned.Close()
		awaitSignal(t, began, "the slot behind an abandoned one begins")
	}},
	{"a slot that no reader settled settles when it begins, behind the settled ones", func(t *testing.T) {
		var gate SettleGate
		settled, unsettled := gate.NewSlot(), gate.NewSlot()
		settled.Settle()
		began := make(chan struct{})
		go func() { unsettled.Begin()(); close(began) }()
		select {
		case <-began:
			t.Fatal("an unsettled slot began before the settled slot")
		default:
		}
		settled.Begin()()
		awaitSignal(t, began, "the unsettled slot begins after the settled one")
	}},
	{"began is idempotent and Begin after Close does not wait", func(t *testing.T) {
		var gate SettleGate
		first, second := gate.NewSlot(), gate.NewSlot()
		first.Settle()
		second.Settle()
		began := first.Begin()
		began()
		began()
		second.Begin()()
		closed := gate.NewSlot()
		closed.Close()
		closed.Begin()()
	}},
	{"a slot knows its gate", func(t *testing.T) {
		var gate SettleGate
		if gate.NewSlot().Gate() != &gate {
			t.Fatal("slot.Gate() is not the gate that made the slot")
		}
	}},
}

func TestSettleGateBehaviors(t *testing.T) {
	for _, tc := range settleGateBehaviors {
		t.Run(tc.name, tc.run)
	}
}
