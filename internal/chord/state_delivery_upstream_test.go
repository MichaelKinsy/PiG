package chord

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/test/state-delivery.test.ts. A Promise-returning listener is a listener that returns a Completion; "await gate" is a goroutine that waits on the gate before it settles the Completion.

type deliveryKind string

const (
	mutableKind  deliveryKind = "mutable"
	attachedKind deliveryKind = "attached"
	replicaKind  deliveryKind = "replica"
)

type locked[T any] struct {
	mu    sync.Mutex
	items []T
}

func (l *locked[T]) add(item T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, item)
}

func (l *locked[T]) get() []T {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]T(nil), l.items...)
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// gate is a deferred: its Completion settles when resolve or reject runs.
type gate struct{ completion chan error }

func newGate() *gate { return &gate{completion: make(chan error, 1)} }

func (g *gate) resolve()            { g.completion <- nil }
func (g *gate) reject(err error)    { g.completion <- err }
func (g *gate) promise() Completion { return g.completion }

// after returns a Completion that runs step once the gate settles, then settles with the gate's outcome.
func (g *gate) after(step func()) Completion {
	done := make(chan error, 1)
	go func() {
		err := <-g.completion
		step()
		done <- err
	}()
	return done
}

type frame struct {
	value    float64
	ctx      context.Context
	delivery ReplicatedStateDelivery
	raw      JsonValue
}

type fixture struct {
	subscribe func(listener func(frame) Completion) func()
	publish   func(value float64, ctx context.Context)
	current   func() JsonValue
	core      *stateCore // nil for a replica, which has no exact listeners
}

func asValueListener(listener func(frame) Completion) valueListener {
	return func(value JsonValue, ctx context.Context, d ReplicatedStateDelivery) (Completion, error) {
		return listener(frame{value: value.(*chordjson.Object).Value("value").(float64), ctx: ctx, delivery: d, raw: value}), nil
	}
}

func newFixture(t *testing.T, kind deliveryKind, onError func(error)) fixture {
	t.Helper()
	switch kind {
	case mutableKind:
		state := newDocState(t, docState{"value": 0.0})
		return fixture{
			subscribe: func(l func(frame) Completion) func() { return state.core.subscribe(asValueListener(l)) },
			publish: func(value float64, ctx context.Context) {
				if err := state.Replace(ctx, docState{"value": value}); err != nil {
					t.Error(err)
				}
			},
			current: func() JsonValue { return stored(state) },
			core:    state.core,
		}
	case attachedKind:
		source := newTestSource(valueDoc(0), 0)
		state := attach(t, source, ReplicatedStateSourceOptions{OnError: onError})
		return fixture{
			subscribe: func(l func(frame) Completion) func() { return state.core.subscribe(asValueListener(l)) },
			publish: func(value float64, ctx context.Context) {
				source.commitAt(valueDoc(value), setValueOps(value), ctx, int(value))
			},
			current: func() JsonValue { _, v := state.core.snapshot(); return v },
			core:    state.core,
		}
	default:
		replica := newReplica(onError)
		if err := replica.hydrate(context.Background(), 0, []Op{{"r", valueDoc(0)}}, nil); err != nil {
			t.Fatal(err)
		}
		return fixture{
			subscribe: func(l func(frame) Completion) func() { return replica.subscribe(asValueListener(l)) },
			publish: func(value float64, ctx context.Context) {
				if err := replica.update(ctx, int(value), setValueOps(value), nil); err != nil {
					t.Error(err)
				}
			},
			current: func() JsonValue { v, _ := replica.snapshotValue(); return v },
		}
	}
}

func (f fixture) exact() *locked[int] {
	sequences := &locked[int]{}
	if f.core != nil {
		f.core.subscribeOps(func(_ context.Context, _ []Op, sequence int) { sequences.add(sequence) })
	}
	return sequences
}

func floats(from, count int) []float64 {
	out := make([]float64, count)
	for at := range out {
		out[at] = float64(from + at)
	}
	return out
}

func TestPublicDelivery(t *testing.T) {
	background := context.Background()
	for _, kind := range []deliveryKind{mutableKind, attachedKind, replicaKind} {
		t.Run(string(kind), func(t *testing.T) {
			t.Run("awaits hydration and each update independently of other subscribers and exact listeners", func(t *testing.T) {
				f := newFixture(t, kind, nil)
				hydration, update := newGate(), newGate()
				events, fast := &locked[string]{}, &locked[float64]{}
				exact := f.exact()
				f.subscribe(func(d frame) Completion {
					events.add(fmt.Sprintf("start:%v", d.value))
					end := func() { events.add(fmt.Sprintf("end:%v", d.value)) }
					switch d.value {
					case 0:
						return hydration.after(end)
					case 1:
						return update.after(end)
					}
					end()
					return nil
				})
				f.subscribe(func(d frame) Completion { fast.add(d.value); return nil })
				f.publish(1, background)
				f.publish(2, background)
				if got := events.get(); !reflect.DeepEqual(got, []string{"start:0"}) {
					t.Fatalf("events = %v", got)
				}
				if got := fast.get(); !reflect.DeepEqual(got, []float64{0, 1, 2}) {
					t.Fatalf("fast = %v", got)
				}
				if got := f.current().(*chordjson.Object).Value("value"); got != 2.0 {
					t.Fatalf("value = %v", got)
				}
				if kind != replicaKind && !reflect.DeepEqual(exact.get(), []int{1, 2}) {
					t.Fatalf("exact = %v", exact.get())
				}
				hydration.resolve()
				waitFor(t, "second delivery to start", func() bool { return reflect.DeepEqual(events.get(), []string{"start:0", "end:0", "start:1"}) })
				update.resolve()
				waitFor(t, "all deliveries", func() bool {
					return reflect.DeepEqual(events.get(), []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"})
				})
			})

			for _, count := range []int{100, 101, 102, 201, 202} {
				t.Run(fmt.Sprintf("bounds %d pending deliveries without changing exact publication", count), func(t *testing.T) {
					f := newFixture(t, kind, nil)
					hydration := newGate()
					received := &locked[float64]{}
					exact := f.exact()
					f.subscribe(func(d frame) Completion {
						received.add(d.value)
						if d.value == 0 {
							return hydration.promise()
						}
						return nil
					})
					for value := 1; value <= count; value++ {
						f.publish(float64(value), background)
					}
					if !reflect.DeepEqual(received.get(), []float64{0}) {
						t.Fatalf("received = %v", received.get())
					}
					if kind != replicaKind && !reflect.DeepEqual(exact.get(), func() []int {
						out := make([]int, count)
						for at := range out {
							out[at] = at + 1
						}
						return out
					}()) {
						t.Fatalf("exact publication changed: %v", exact.get())
					}
					hydration.resolve()
					first := (count-1)/100*100 + 1
					want := append([]float64{0}, floats(first, count-first+1)...)
					waitFor(t, "bounded deliveries", func() bool { return reflect.DeepEqual(received.get(), want) })
				})
			}

			t.Run("excludes a running update from overflow and retains exact value/context/delivery", func(t *testing.T) {
				f := newFixture(t, kind, nil)
				g := newGate()
				type contextKey struct{}
				marker := context.WithValue(background, contextKey{}, "newest")
				received := &locked[frame]{}
				f.subscribe(func(d frame) Completion {
					received.add(d)
					if d.value == 1 {
						return g.promise()
					}
					return nil
				})
				for value := 1; value < 102; value++ {
					f.publish(float64(value), background)
				}
				f.publish(102, marker)
				adopted := f.current()
				f.publish(103, background)
				values := func() []float64 {
					var out []float64
					for _, d := range received.get() {
						out = append(out, d.value)
					}
					return out
				}
				if !reflect.DeepEqual(values(), []float64{0, 1}) {
					t.Fatalf("received = %v", values())
				}
				g.resolve()
				waitFor(t, "newest deliveries", func() bool { return reflect.DeepEqual(values(), []float64{0, 1, 102, 103}) })
				third := received.get()[2]
				if container(third.raw) != container(adopted) || third.ctx != marker || third.delivery != (ReplicatedStateDelivery{Kind: DeliveryUpdate, Sequence: 102}) {
					t.Fatalf("retained delivery = %+v", third)
				}
			})

			t.Run("serializes reentrant hydration and update callbacks", func(t *testing.T) {
				f := newFixture(t, kind, nil)
				events := &locked[string]{}
				f.subscribe(func(d frame) Completion {
					events.add(fmt.Sprintf("start:%v", d.value))
					if d.value < 2 {
						f.publish(d.value+1, background)
					}
					events.add(fmt.Sprintf("end:%v", d.value))
					return nil
				})
				want := []string{"start:0", "end:0", "start:1", "end:1", "start:2", "end:2"}
				if got := events.get(); !reflect.DeepEqual(got, want) {
					t.Fatalf("events = %v", got)
				}
			})

			t.Run("treats two subscriptions of the same callback independently", func(t *testing.T) {
				f := newFixture(t, kind, nil)
				g := newGate()
				received := &locked[float64]{}
				first := true
				listener := func(d frame) Completion {
					received.add(d.value)
					if d.value == 0 && first {
						first = false
						return g.promise()
					}
					return nil
				}
				stopFirst := f.subscribe(listener)
				stopSecond := f.subscribe(listener)
				f.publish(1, background)
				stopFirst()
				stopFirst()
				g.resolve()
				waitFor(t, "second subscription deliveries", func() bool { return reflect.DeepEqual(received.get(), []float64{0, 0, 1}) })
				stopSecond()
			})

			t.Run("unsubscribe drops queued callbacks without joining or aborting the running callback", func(t *testing.T) {
				f := newFixture(t, kind, nil)
				g := newGate()
				ctx, cancel := context.WithCancel(background)
				defer cancel()
				received := &locked[float64]{}
				var completed bool
				var mu sync.Mutex
				stop := f.subscribe(func(d frame) Completion {
					received.add(d.value)
					if d.value == 1 {
						return g.after(func() { mu.Lock(); completed = true; mu.Unlock() })
					}
					return nil
				})
				f.publish(1, ctx)
				f.publish(2, ctx)
				stop()
				f.publish(3, ctx)
				mu.Lock()
				done := completed
				mu.Unlock()
				if ctx.Err() != nil || done {
					t.Fatalf("aborted=%v completed=%v", ctx.Err(), done)
				}
				g.resolve()
				waitFor(t, "running callback to complete", func() bool { mu.Lock(); defer mu.Unlock(); return completed })
				if got := received.get(); !reflect.DeepEqual(got, []float64{0, 1}) {
					t.Fatalf("received = %v", got)
				}
			})
		})
	}

	for _, kind := range []deliveryKind{attachedKind, replicaKind} {
		t.Run(string(kind)+" listener errors", func(t *testing.T) {
			t.Run("isolates a synchronous hydration failure without removing the subscription", func(t *testing.T) {
				errs := &locked[error]{}
				f := newFixture(t, kind, func(err error) { errs.add(err) })
				failure := errors.New("sync hydration")
				received := &locked[float64]{}
				f.core2Subscribe(func(d frame) Completion {
					received.add(d.value)
					if d.value == 0 {
						panic(failure)
					}
					return nil
				})
				f.publish(1, background)
				if got := errs.get(); len(got) != 1 || !errors.Is(got[0], failure) {
					t.Fatalf("errors = %v", got)
				}
				if got := received.get(); !reflect.DeepEqual(got, []float64{0, 1}) {
					t.Fatalf("received = %v", got)
				}
			})

			t.Run("observes hydration rejection, synchronous throw, and update rejection while continuing delivery", func(t *testing.T) {
				errs := &locked[error]{}
				f := newFixture(t, kind, func(err error) { errs.add(err) })
				g := newGate()
				received, fast := &locked[float64]{}, &locked[float64]{}
				f.subscribe(func(d frame) Completion {
					received.add(d.value)
					switch d.value {
					case 0:
						return g.promise()
					case 1:
						panic(errors.New("sync update"))
					case 2:
						rejected := make(chan error, 1)
						rejected <- errors.New("async update")
						return rejected
					}
					return nil
				})
				f.subscribe(func(d frame) Completion { fast.add(d.value); return nil })
				for value := 1; value <= 3; value++ {
					f.publish(float64(value), background)
				}
				g.reject(errors.New("async hydration"))
				waitFor(t, "all deliveries", func() bool { return reflect.DeepEqual(received.get(), []float64{0, 1, 2, 3}) })
				waitFor(t, "all failures", func() bool { return len(errs.get()) == 3 })
				messages := []string{}
				for _, err := range errs.get() {
					messages = append(messages, err.Error())
				}
				if !reflect.DeepEqual(messages, []string{"async hydration", "sync update", "async update"}) {
					t.Fatalf("errors = %v", messages)
				}
				if got := fast.get(); !reflect.DeepEqual(got, []float64{0, 1, 2, 3}) {
					t.Fatalf("fast = %v", got)
				}
			})

			t.Run("still observes a callback rejection after unsubscribe", func(t *testing.T) {
				errs := &locked[error]{}
				f := newFixture(t, kind, func(err error) { errs.add(err) })
				g := newGate()
				received := &locked[float64]{}
				stop := f.subscribe(func(d frame) Completion { received.add(d.value); return g.promise() })
				f.publish(1, background)
				stop()
				failure := errors.New("stopped callback")
				g.reject(failure)
				waitFor(t, "rejection report", func() bool { return len(errs.get()) == 1 })
				if got := errs.get(); !errors.Is(got[0], failure) {
					t.Fatalf("errors = %v", got)
				}
				if got := received.get(); !reflect.DeepEqual(got, []float64{0}) {
					t.Fatalf("received = %v", got)
				}
			})
		})
	}

	t.Run("mutable state reports rejected callbacks through its default error reporter", func(t *testing.T) {
		reports := captureUncaught(t)
		f := newFixture(t, mutableKind, nil)
		failure := errors.New("async mutable hydration")
		received := &locked[float64]{}
		f.subscribe(func(d frame) Completion {
			received.add(d.value)
			if d.value == 0 {
				rejected := make(chan error, 1)
				rejected <- failure
				return rejected
			}
			return nil
		})
		f.publish(1, background)
		// The report and the next delivery are independent once the rejection settles (upstream asserts both only after they settle), so wait for both.
		waitFor(t, "default reporter and the next delivery", func() bool { return len(reports()) == 1 && len(received.get()) == 2 })
		if got := received.get(); !reflect.DeepEqual(got, []float64{0, 1}) || !errors.Is(reports()[0], failure) {
			t.Fatalf("received=%v reports=%v", got, reports())
		}
	})

	t.Run("replica disconnect drops obsolete pending work but waits for the running callback before rehydration", func(t *testing.T) {
		replica := newReplica(func(error) {})
		g := newGate()
		received := &locked[ReplicatedStateDelivery]{}
		replica.subscribe(func(_ JsonValue, _ context.Context, d ReplicatedStateDelivery) (Completion, error) {
			received.add(d)
			if d.Sequence == 0 {
				return g.promise(), nil
			}
			return nil, nil
		})
		for _, step := range []func() error{
			func() error { return replica.hydrate(background, 0, []Op{{"r", valueDoc(0)}}, nil) },
			func() error { return replica.update(background, 1, setValueOps(1), nil) },
			func() error { replica.clear(); return nil },
			func() error { return replica.hydrate(background, 50, []Op{{"r", valueDoc(50)}}, nil) },
			func() error { return replica.update(background, 51, setValueOps(51), nil) },
		} {
			if err := step(); err != nil {
				t.Fatal(err)
			}
		}
		if got := received.get(); !reflect.DeepEqual(got, []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}}) {
			t.Fatalf("received = %v", got)
		}
		g.resolve()
		want := []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}, {Kind: DeliveryHydrate, Sequence: 50}, {Kind: DeliveryUpdate, Sequence: 51}}
		waitFor(t, "rehydration", func() bool { return reflect.DeepEqual(received.get(), want) })
	})

	t.Run("cold replicas hydrate all listeners before their reentrant updates", func(t *testing.T) {
		replica := newReplica(func(error) {})
		var second []ReplicatedStateDelivery
		replica.subscribe(func(_ JsonValue, _ context.Context, d ReplicatedStateDelivery) (Completion, error) {
			if d.Kind == DeliveryHydrate {
				if err := replica.update(background, 1, setValueOps(1), nil); err != nil {
					t.Error(err)
				}
			}
			return nil, nil
		})
		replica.subscribe(func(_ JsonValue, _ context.Context, d ReplicatedStateDelivery) (Completion, error) {
			second = append(second, d)
			return nil, nil
		})
		if err := replica.hydrate(background, 0, []Op{{"r", valueDoc(0)}}, nil); err != nil {
			t.Fatal(err)
		}
		want := []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}, {Kind: DeliveryUpdate, Sequence: 1}}
		if !reflect.DeepEqual(second, want) {
			t.Fatalf("second = %v", second)
		}
	})
}

// core2Subscribe subscribes a listener that may panic synchronously.
func (f fixture) core2Subscribe(listener func(frame) Completion) func() {
	return f.subscribe(listener)
}

// state.ts StateSubscriber.push: "A cold replica can queue updates reentrantly before this subscriber's first hydration starts", so overflow keeps the unstarted hydration ahead of the newest update, while a started subscriber keeps only the newest. The public paths drain a subscriber as soon as they queue for it, so the branch is driven on the subscriber directly.
func TestOverflowBeforeFirstHydrationKeepsTheHydration(t *testing.T) {
	background := context.Background()
	frame := func(kind string, sequence int) stateDelivery {
		return stateDelivery{value: float64(sequence), ctx: background, delivery: ReplicatedStateDelivery{Kind: kind, Sequence: sequence}}
	}
	var received []ReplicatedStateDelivery
	record := func(_ JsonValue, _ context.Context, d ReplicatedStateDelivery) (Completion, error) {
		received = append(received, d)
		return nil, nil
	}

	cold := newSubscriber(record, func(error) {})
	cold.push(frame(DeliveryHydrate, 0))
	for sequence := 1; sequence <= maxPendingDeliveries; sequence++ {
		cold.push(frame(DeliveryUpdate, sequence))
	}
	cold.drain()
	want := []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}, {Kind: DeliveryUpdate, Sequence: maxPendingDeliveries}}
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("cold subscriber received %v, want %v", received, want)
	}

	received = nil
	gate := make(chan error)
	started := newSubscriber(func(value JsonValue, ctx context.Context, d ReplicatedStateDelivery) (Completion, error) {
		_, _ = record(value, ctx, d)
		if d.Kind == DeliveryHydrate {
			return gate, nil
		}
		return nil, nil
	}, func(error) {})
	started.push(frame(DeliveryHydrate, 0))
	started.drain()
	for sequence := 1; sequence <= maxPendingDeliveries+1; sequence++ {
		started.push(frame(DeliveryUpdate, sequence))
	}
	gate <- nil
	deadline := time.Now().Add(5 * time.Second)
	for {
		started.mu.Lock()
		idle := !started.running
		started.mu.Unlock()
		if idle || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	want = []ReplicatedStateDelivery{{Kind: DeliveryHydrate, Sequence: 0}, {Kind: DeliveryUpdate, Sequence: maxPendingDeliveries + 1}}
	started.mu.Lock()
	defer started.mu.Unlock()
	if !reflect.DeepEqual(received, want) {
		t.Fatalf("started subscriber received %v, want %v", received, want)
	}
}
