package chord

// Ports packages/chord/test/replicated-state-delivery.test.ts at Pi 0104fef1. A deferred promise is a buffered channel;
// an async listener returns a Completion that settles when the deferred does; vi.waitFor is waitFor on the record's
// change notifications.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

type deliveryRecord struct {
	mu      sync.Mutex
	items   []string
	changed chan struct{}
}

func newDeliveryRecord() *deliveryRecord { return &deliveryRecord{changed: make(chan struct{}, 1)} }

func (record *deliveryRecord) add(item string) {
	record.mu.Lock()
	record.items = append(record.items, item)
	record.mu.Unlock()
	select {
	case record.changed <- struct{}{}:
	default:
	}
}

func (record *deliveryRecord) all() []string {
	record.mu.Lock()
	defer record.mu.Unlock()
	return slices.Clone(record.items)
}

func (record *deliveryRecord) waitFor(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !slices.Equal(record.all(), want) {
		select {
		case <-record.changed:
		case <-deadline.C:
			t.Fatalf("record %v, want %v", record.all(), want)
		}
	}
}

// settleAfter returns a Completion that settles with gate's outcome after then runs.
func settleAfter(gate <-chan error, then func()) Completion {
	done := make(chan error, 1)
	go func() {
		err := <-gate
		if then != nil {
			then()
		}
		done <- err
	}()
	return done
}

type deliveryFixture struct {
	state   ReplicatedState[*delta.JsonObject]
	publish func(value float64, ctx context.Context)
}

func newDeliveryFixture(t *testing.T, kind string, onError func(error)) deliveryFixture {
	t.Helper()
	if kind == "mutable" {
		mutable := NewReplicatedState(delta.JsonObjectOf("value", float64(0)))
		return deliveryFixture{state: mutable, publish: func(value float64, ctx context.Context) {
			if err := mutable.Replace(ctx, delta.JsonObjectOf("value", value)); err != nil {
				t.Error(err)
			}
		}}
	}
	if onError == nil {
		onError = func(error) {}
	}
	attachment := &fixtureAttachment{}
	state, err := AttachReplicatedStateSource[*delta.JsonObject](fixtureSource{attachment}, ReplicatedStateSourceOptions{OnError: onError})
	if err != nil {
		t.Fatal(err)
	}
	return deliveryFixture{state: state, publish: func(value float64, ctx context.Context) {
		attachment.receive(ReplicatedStateSourceFrame[*delta.JsonObject]{Cursor: int(value), Value: delta.JsonObjectOf("value", value), Ops: setOp(value), Context: ctx})
	}}
}

type fixtureSource struct{ attachment *fixtureAttachment }

func (source fixtureSource) Attach() (ReplicatedStateSourceAttachment[*delta.JsonObject], error) {
	return source.attachment, nil
}

type fixtureAttachment struct {
	receive func(ReplicatedStateSourceFrame[*delta.JsonObject])
}

func (*fixtureAttachment) Snapshot() ReplicatedStateSnapshot[*delta.JsonObject] {
	return ReplicatedStateSnapshot[*delta.JsonObject]{Value: delta.JsonObjectOf("value", float64(0))}
}

func (attachment *fixtureAttachment) Activate(listener func(ReplicatedStateSourceFrame[*delta.JsonObject])) error {
	attachment.receive = listener
	return nil
}

func (*fixtureAttachment) Dispose() {}

func number(value *delta.JsonObject) float64 { return value.Value("value").(float64) }

func TestReplicatedStatePublicDelivery(t *testing.T) {
	background := chordctx.Background()
	for _, kind := range []string{"mutable", "attached"} {
		t.Run(kind, func(t *testing.T) {
			t.Run("awaits hydration and each update independently of other subscribers", func(t *testing.T) {
				fixture := newDeliveryFixture(t, kind, nil)
				hydration, update := make(chan error, 1), make(chan error, 1)
				events := newDeliveryRecord()
				var fast []float64
				fixture.state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
					current := number(value)
					events.add(fmt.Sprintf("start:%v", current))
					end := func() { events.add(fmt.Sprintf("end:%v", current)) }
					switch current {
					case 0:
						return settleAfter(hydration, end)
					case 1:
						return settleAfter(update, end)
					}
					end()
					return nil
				})
				fixture.state.Subscribe(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) {
					fast = append(fast, number(value))
				})
				fixture.publish(1, background)
				fixture.publish(2, background)
				if got := events.all(); !slices.Equal(got, []string{"start:0"}) {
					t.Fatalf("events %v", got)
				}
				if !slices.Equal(fast, []float64{0, 1, 2}) || number(fixture.state.Value()) != 2 {
					t.Fatalf("fast %v, value %v", fast, fixture.state.Value())
				}
				hydration <- nil
				events.waitFor(t, "start:0", "end:0", "start:1")
				update <- nil
				events.waitFor(t, "start:0", "end:0", "start:1", "end:1", "start:2", "end:2")
			})

			for _, count := range []int{100, 101, 102, 201, 202} {
				t.Run(fmt.Sprintf("bounds %d pending deliveries", count), func(t *testing.T) {
					fixture := newDeliveryFixture(t, kind, nil)
					hydration := make(chan error, 1)
					received := newDeliveryRecord()
					fixture.state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
						received.add(fmt.Sprint(number(value)))
						if number(value) == 0 {
							return settleAfter(hydration, nil)
						}
						return nil
					})
					for value := 1; value <= count; value++ {
						fixture.publish(float64(value), background)
					}
					if got := received.all(); !slices.Equal(got, []string{"0"}) {
						t.Fatalf("received %v before the hydration settled", got)
					}
					hydration <- nil
					first := (count-1)/100*100 + 1
					want := []string{"0"}
					for value := first; value <= count; value++ {
						want = append(want, fmt.Sprint(value))
					}
					received.waitFor(t, want...)
				})
			}

			t.Run("excludes a running update from overflow and retains exact value/context/delivery", func(t *testing.T) {
				fixture := newDeliveryFixture(t, kind, nil)
				gate := make(chan error, 1)
				ctx, cancel := chordctx.WithCancel(background)
				defer cancel(nil)
				type entry struct {
					value    *delta.JsonObject
					ctx      context.Context
					delivery ReplicatedStateDelivery
				}
				var mu sync.Mutex
				var received []entry
				values := newDeliveryRecord()
				fixture.state.SubscribeAsync(func(value *delta.JsonObject, c context.Context, delivery ReplicatedStateDelivery) Completion {
					mu.Lock()
					received = append(received, entry{value, c, delivery})
					mu.Unlock()
					values.add(fmt.Sprint(number(value)))
					if number(value) == 1 {
						return settleAfter(gate, nil)
					}
					return nil
				})
				for value := 1; value < 102; value++ {
					fixture.publish(float64(value), background)
				}
				fixture.publish(102, ctx)
				adopted := fixture.state.Value()
				fixture.publish(103, background)
				if got := values.all(); !slices.Equal(got, []string{"0", "1"}) {
					t.Fatalf("received %v", got)
				}
				gate <- nil
				values.waitFor(t, "0", "1", "102", "103")
				mu.Lock()
				defer mu.Unlock()
				if received[2].value != adopted || received[2].ctx != ctx || received[2].delivery != (ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: 102}) {
					t.Fatalf("the retained delivery is %+v", received[2])
				}
			})

			t.Run("serializes reentrant hydration and update callbacks", func(t *testing.T) {
				fixture := newDeliveryFixture(t, kind, nil)
				var events []string
				fixture.state.Subscribe(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) {
					current := number(value)
					events = append(events, fmt.Sprintf("start:%v", current))
					if current < 2 {
						fixture.publish(current+1, background)
					}
					events = append(events, fmt.Sprintf("end:%v", current))
				})
				if want := []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"}; !slices.Equal(events, want) {
					t.Fatalf("events %v, want %v", events, want)
				}
			})

			t.Run("treats two subscriptions of the same callback independently", func(t *testing.T) {
				fixture := newDeliveryFixture(t, kind, nil)
				gate := make(chan error, 1)
				received := newDeliveryRecord()
				listener := func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
					received.add(fmt.Sprint(number(value)))
					if number(value) == 0 {
						return gate
					}
					return nil
				}
				stopFirst := fixture.state.SubscribeAsync(listener)
				stopSecond := fixture.state.SubscribeAsync(listener)
				fixture.publish(1, background)
				stopFirst()
				stopFirst()
				// One settlement releases one of the two waiting subscriptions: a deferred settles every awaiter, a
				// channel value only one, so the second is settled too.
				gate <- nil
				gate <- nil
				received.waitFor(t, "0", "0", "1")
				stopSecond()
			})

			t.Run("unsubscribe drops queued callbacks without joining or aborting the running callback", func(t *testing.T) {
				fixture := newDeliveryFixture(t, kind, nil)
				gate := make(chan error, 1)
				ctx, cancel := chordctx.WithCancel(background)
				defer cancel(nil)
				received := newDeliveryRecord()
				completed := make(chan struct{})
				stop := fixture.state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
					received.add(fmt.Sprint(number(value)))
					if number(value) == 1 {
						return settleAfter(gate, func() { close(completed) })
					}
					return nil
				})
				fixture.publish(1, ctx)
				fixture.publish(2, ctx)
				stop()
				fixture.publish(3, ctx)
				if ctx.Err() != nil {
					t.Fatal("unsubscribe aborted the delivery context")
				}
				select {
				case <-completed:
					t.Fatal("unsubscribe joined the running callback")
				default:
				}
				gate <- nil
				<-completed
				if got := received.all(); !slices.Equal(got, []string{"0", "1"}) {
					t.Fatalf("received %v", got)
				}
			})
		})
	}
}

func TestAttachedReplicatedStateListenerErrors(t *testing.T) {
	background := chordctx.Background()

	t.Run("isolates a synchronous hydration failure without removing the subscription", func(t *testing.T) {
		var reported []error
		fixture := newDeliveryFixture(t, "attached", func(err error) { reported = append(reported, err) })
		var received []float64
		failure := errors.New("sync hydration")
		fixture.state.Subscribe(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) {
			received = append(received, number(value))
			if number(value) == 0 {
				panic(failure)
			}
		})
		fixture.publish(1, background)
		if len(reported) != 1 || reported[0] != failure || !slices.Equal(received, []float64{0, 1}) {
			t.Fatalf("reported %v, received %v", reported, received)
		}
	})

	t.Run("observes hydration rejection, synchronous throw, and update rejection while continuing delivery", func(t *testing.T) {
		errorsSeen := newDeliveryRecord()
		fixture := newDeliveryFixture(t, "attached", func(err error) { errorsSeen.add(err.Error()) })
		gate := make(chan error, 1)
		received := newDeliveryRecord()
		var fast []float64
		fixture.state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
			received.add(fmt.Sprint(number(value)))
			switch number(value) {
			case 0:
				return gate
			case 1:
				panic(errors.New("sync update"))
			case 2:
				rejected := make(chan error, 1)
				rejected <- errors.New("async update")
				return rejected
			}
			return nil
		})
		fixture.state.Subscribe(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) {
			fast = append(fast, number(value))
		})
		for value := 1; value <= 3; value++ {
			fixture.publish(float64(value), background)
		}
		gate <- errors.New("async hydration")
		received.waitFor(t, "0", "1", "2", "3")
		errorsSeen.waitFor(t, "async hydration", "sync update", "async update")
		if !slices.Equal(fast, []float64{0, 1, 2, 3}) {
			t.Fatalf("fast %v", fast)
		}
	})

	t.Run("still observes a callback rejection after unsubscribe", func(t *testing.T) {
		errorsSeen := newDeliveryRecord()
		fixture := newDeliveryFixture(t, "attached", func(err error) { errorsSeen.add(err.Error()) })
		gate := make(chan error, 1)
		received := newDeliveryRecord()
		stop := fixture.state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
			received.add(fmt.Sprint(number(value)))
			return gate
		})
		fixture.publish(1, background)
		stop()
		gate <- errors.New("stopped callback")
		errorsSeen.waitFor(t, "stopped callback")
		if got := received.all(); !slices.Equal(got, []string{"0"}) {
			t.Fatalf("received %v", got)
		}
	})
}

// A rejected callback of a mutable state goes to the asynchronous default reporter; Replace neither returns nor waits
// for it.
func TestMutableReplicatedStateReportsRejectedCallbacksThroughItsDefaultReporter(t *testing.T) {
	reports := newDeliveryRecord()
	previous := SetUncaughtErrorReporter(func(err error) { reports.add(err.Error()) })
	t.Cleanup(func() { SetUncaughtErrorReporter(previous) })
	state := NewReplicatedState(delta.JsonObjectOf("value", float64(0)))
	received := newDeliveryRecord()
	state.SubscribeAsync(func(value *delta.JsonObject, _ context.Context, _ ReplicatedStateDelivery) Completion {
		received.add(fmt.Sprint(number(value)))
		if number(value) == 0 {
			rejected := make(chan error, 1)
			rejected <- errors.New("async mutable hydration")
			return rejected
		}
		return nil
	})
	if err := state.Replace(chordctx.Background(), delta.JsonObjectOf("value", float64(1))); err != nil {
		t.Fatalf("Replace returned %v", err)
	}
	received.waitFor(t, "0", "1")
	reports.waitFor(t, "async mutable hydration")
	if strings.Contains(strings.Join(reports.all(), ""), "Replicated state listeners failed") {
		t.Fatal("the failure was aggregated")
	}
}
