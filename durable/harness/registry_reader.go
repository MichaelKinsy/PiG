// Ports packages/durable/src/harness/registry-reader.ts.

package harness

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
)

// NewRegistryReader is registryReader: a RegistryReader over a Chord registry value whose composition is a registry snapshot.
// Snapshot reads the current composition (the last good one while Compose fails), as the reader's observer recorded it. Subscribe observes its compositions; the
// listener runs synchronously inside the Chord batch that composed, so it only wakes the scheduler (a Chord call there fails
// with invalid/reentrant). ctx is the observation's: cancelling it detaches every listener. The name differs from Pi's because
// Go names the interface RegistryReader. Subscribe panics with the error Observe returns, where Pi's observe throws: the value
// has no composition to start at.
func NewRegistryReader(ctx context.Context, value registries.RegistryValue[durable.RegistrySnapshot]) RegistryReader {
	reader := chordRegistryReader{ctx: ctx, value: value, cache: &snapshotCache{}}
	// The Harness reads the snapshot from its own goroutines, where Chord's value may be read only inside its host's batch: an
	// observer kept for the reader's life records each composition inside the batch that makes it. Without one (the value has no
	// composition to start at, or ctx ended) Snapshot reads the value itself.
	if observation, err := value.Observe(reader.cache, ctx); err == nil {
		reader.cache.attach(observation.Value)
	}
	return reader
}

type chordRegistryReader struct {
	ctx   context.Context
	value registries.RegistryValue[durable.RegistrySnapshot]
	cache *snapshotCache
}

func (reader chordRegistryReader) Snapshot() durable.RegistrySnapshot {
	if snapshot, ok := reader.cache.get(); ok {
		return snapshot
	}
	return reader.value.Value()
}

// snapshotCache is the latest composition a reader's observer saw. A composition that arrived before attach set the
// observation's start value is newer than it and stays.
type snapshotCache struct {
	mu       sync.Mutex
	snapshot durable.RegistrySnapshot
	attached bool
	seen     bool
	// ended is set once the observation ended, also before attach: Chord ends an observer synchronously, before Observe
	// returns, when ctx is done or its source ended.
	ended bool
}

func (cache *snapshotCache) attach(start durable.RegistrySnapshot) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.ended {
		return
	}
	if !cache.seen {
		cache.snapshot = start
	}
	cache.attached = true
}

func (cache *snapshotCache) get() (durable.RegistrySnapshot, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.snapshot, cache.attached
}

func (cache *snapshotCache) Advance(call chord.NativeWatchCall[durable.RegistrySnapshot], _ context.Context) {
	if call.Kind != chord.WatchCallValue {
		return
	}
	cache.mu.Lock()
	cache.snapshot, cache.seen = call.Value, true
	cache.mu.Unlock()
}

// End stops the cache serving: the observation ended (ctx cancelled, or the host closed).
func (cache *snapshotCache) End(chord.WatchEnd) {
	cache.mu.Lock()
	cache.attached, cache.ended = false, true
	cache.mu.Unlock()
}

var _ state.NativeObserver[durable.RegistrySnapshot] = (*snapshotCache)(nil)

func (reader chordRegistryReader) Subscribe(listener func()) func() {
	observation, err := reader.value.Observe(&compositionObserver{listener: listener}, reader.ctx)
	if err != nil {
		panic(err)
	}
	return observation.Detach
}

// compositionObserver wakes its listener for every composed value. A failed composition keeps the last snapshot: nothing to
// reconsider. It is used as a pointer: the source finds an observer to detach by comparing interfaces, which panics on a struct
// holding a func.
type compositionObserver struct{ listener func() }

func (observer *compositionObserver) Advance(call chord.NativeWatchCall[durable.RegistrySnapshot], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		observer.listener()
	}
}

func (*compositionObserver) End(chord.WatchEnd) {}

var _ state.NativeObserver[durable.RegistrySnapshot] = (*compositionObserver)(nil)
