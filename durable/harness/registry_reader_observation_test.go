// Ports the rule of packages/durable/src/harness/registry-reader.ts that Pi's two cases in
// packages/durable/test/harness-registry-reader.test.ts reach only through a host whose compositions Go reads from other
// goroutines: `snapshot()` is the current composition, the last good one while `compose` fails (registry-reader.ts:12-16), and
// `context` is the observation's: aborting it detaches every listener. A Go reader may not read a Chord value outside its
// host's batch, so it serves the composition its own observer recorded; these cases pin what that must keep of Pi's rule, on a
// value that scripts the compositions, the batch's ordering and the end of an observation by its context.

package harness

// pi: packages/durable/src/harness/registry-reader.ts

// mutation-checked (one-line mutants of registry_reader.go, each failing the case named): Snapshot never served from the cache, the
// cache never marking that a composition was seen, attach overwriting a composition newer than its start value (the first case and
// TestRegistryReaderServesTheLatestComposition...'s second), Subscribe not passing the reader's context, the reader dropping its
// observer or the observer's context (TestRegistryReaderEndsWithItsContext).

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
)

// scriptedValue is a registries.RegistryValue the test drives. Observe records the observer with its context; compose and fail
// call the live observers as Chord's batch does; endDone ends the observers whose context is done, as Chord does when it is
// aborted; Value counts the reads made outside a batch.
type scriptedValue struct {
	chord.NativeWatchable[durable.RegistrySnapshot] // Watch is not used by registryReader
	mu                                              sync.Mutex
	current                                         durable.RegistrySnapshot
	start                                           durable.RegistrySnapshot
	beforeReturn                                    func(observer state.NativeObserver[durable.RegistrySnapshot], ctx context.Context)
	reads                                           int
	observations                                    []*scriptedObservation
}

type scriptedObservation struct {
	observer state.NativeObserver[durable.RegistrySnapshot]
	ctx      context.Context
	live     bool
}

func (value *scriptedValue) Value() durable.RegistrySnapshot {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.reads++
	return value.current
}

func (value *scriptedValue) Observe(observer state.NativeObserver[durable.RegistrySnapshot], ctx context.Context) (state.NativeObservation[durable.RegistrySnapshot], error) {
	observation := &scriptedObservation{observer: observer, ctx: ctx, live: true}
	value.mu.Lock()
	value.observations = append(value.observations, observation)
	beforeReturn, start := value.beforeReturn, value.start
	value.mu.Unlock()
	if beforeReturn != nil {
		beforeReturn(observer, ctx)
	}
	return state.NativeObservation[durable.RegistrySnapshot]{Value: start, Detach: func() {
		value.mu.Lock()
		observation.live = false
		value.mu.Unlock()
	}}, nil
}

func (value *scriptedValue) liveObservations() []*scriptedObservation {
	value.mu.Lock()
	defer value.mu.Unlock()
	var live []*scriptedObservation
	for _, observation := range value.observations {
		if observation.live {
			live = append(live, observation)
		}
	}
	return live
}

// compose makes snapshot the composition and delivers it to the live observers.
func (value *scriptedValue) compose(snapshot durable.RegistrySnapshot) {
	value.mu.Lock()
	value.current = snapshot
	value.mu.Unlock()
	for _, observation := range value.liveObservations() {
		observation.observer.Advance(chord.NativeWatchCall[durable.RegistrySnapshot]{Kind: chord.WatchCallValue, Value: snapshot}, observation.ctx)
	}
}

// fail delivers a failed composition: the value keeps its last good composition.
func (value *scriptedValue) fail(err error) {
	for _, observation := range value.liveObservations() {
		observation.observer.Advance(chord.NativeWatchCall[durable.RegistrySnapshot]{Kind: chord.WatchCallFailed, Error: err}, observation.ctx)
	}
}

// endDone ends every live observer whose context is done.
func (value *scriptedValue) endDone() {
	for _, observation := range value.liveObservations() {
		if observation.ctx.Err() == nil {
			continue
		}
		value.mu.Lock()
		observation.live = false
		value.mu.Unlock()
		observation.observer.End(chord.WatchEnd{Kind: chord.WatchAborted, Error: observation.ctx.Err()})
	}
}

func (value *scriptedValue) readCount() int {
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.reads
}

// compositionOf is the snapshot of a registry holding one extension per name.
func compositionOf(t *testing.T, names ...string) durable.RegistrySnapshot {
	t.Helper()
	registry := CreateRegistry()
	for _, name := range names {
		mustInstall(t, registry, new(durable.Extension{Name: name}))
	}
	return registry.Snapshot()
}

func holds(snapshot durable.RegistrySnapshot, name string) bool {
	return snapshot.Extension(name) != nil
}

func TestRegistryReaderServesTheLatestCompositionItsObserverRecorded(t *testing.T) {
	t.Run("reads the last good composition while compose fails, without reading the value outside its batch", func(t *testing.T) {
		value := &scriptedValue{current: compositionOf(t), start: compositionOf(t)}
		reader := NewRegistryReader(testContext, value)
		if got := len(value.liveObservations()); got != 1 {
			t.Fatalf("live observations after the reader was made = %d, want its one observer", got)
		}
		if holds(reader.Snapshot(), "app.one") {
			t.Fatal("the snapshot holds an extension before it is composed")
		}
		value.compose(compositionOf(t, "app.one"))
		if !holds(reader.Snapshot(), "app.one") {
			t.Fatal("the snapshot after a composition is not that composition")
		}
		value.fail(errors.New("compose failed"))
		if !holds(reader.Snapshot(), "app.one") {
			t.Fatal("a failed composition dropped the last good snapshot")
		}
		value.compose(compositionOf(t, "app.one", "app.two"))
		if snapshot := reader.Snapshot(); !holds(snapshot, "app.one") || !holds(snapshot, "app.two") {
			t.Fatal("the snapshot after the next composition is not that composition")
		}
		if reads := value.readCount(); reads != 0 {
			t.Fatalf("the reader read the value %d times outside the batch that composes, want 0: it serves what its observer recorded", reads)
		}
	})

	t.Run("keeps a composition that arrived before Observe returned the value it started at", func(t *testing.T) {
		newer := compositionOf(t, "app.newer")
		value := &scriptedValue{current: newer, start: compositionOf(t, "app.older")}
		value.beforeReturn = func(observer state.NativeObserver[durable.RegistrySnapshot], ctx context.Context) {
			observer.Advance(chord.NativeWatchCall[durable.RegistrySnapshot]{Kind: chord.WatchCallValue, Value: newer}, ctx)
		}
		reader := NewRegistryReader(testContext, value)
		snapshot := reader.Snapshot()
		if !holds(snapshot, "app.newer") || holds(snapshot, "app.older") {
			t.Fatal("the reader replaced a composition that arrived before its observation's start value with that older start value")
		}
		if reads := value.readCount(); reads != 0 {
			t.Fatalf("the reader read the value %d times, want 0", reads)
		}
	})
}

func TestRegistryReaderEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()
	older, newer := compositionOf(t, "app.older"), compositionOf(t, "app.newer")
	value := &scriptedValue{current: older, start: older}
	reader := NewRegistryReader(ctx, value)
	woken := 0
	reader.Subscribe(func() { woken++ })
	value.compose(older)
	if woken != 1 {
		t.Fatalf("woken by a composition before the context ended = %d, want 1", woken)
	}
	cancel()
	value.endDone()
	value.compose(newer)
	if woken != 1 {
		t.Fatalf("a listener was woken %d times: aborting the observation's context must detach every listener", woken)
	}
	if got := len(value.liveObservations()); got != 0 {
		t.Fatalf("%d observations outlived the reader's context", got)
	}
	// The observation ended: nothing records compositions any more, so the reader reads the value itself, which is newer.
	if snapshot := reader.Snapshot(); !holds(snapshot, "app.newer") {
		t.Fatal("after its context ended the reader serves a stale composition, not the value's current one")
	}
	if reads := value.readCount(); reads != 1 {
		t.Fatalf("value reads after the context ended = %d, want 1", reads)
	}
}
