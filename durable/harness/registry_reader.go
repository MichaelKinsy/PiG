// Ports packages/durable/src/harness/registry-reader.ts.

package harness

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
)

// NewRegistryReader is registryReader: a RegistryReader over a Chord registry value whose composition is a registry snapshot.
// Snapshot reads the current composition (the last good one while Compose fails). Subscribe observes its compositions; the
// listener runs synchronously inside the Chord batch that composed, so it only wakes the scheduler (a Chord call there fails
// with invalid/reentrant). ctx is the observation's: cancelling it detaches every listener. The name differs from Pi's because
// Go names the interface RegistryReader. Subscribe panics with the error Observe returns, where Pi's observe throws: the value
// has no composition to start at.
func NewRegistryReader(ctx context.Context, value registries.RegistryValue[durable.RegistrySnapshot]) RegistryReader {
	return chordRegistryReader{ctx: ctx, value: value}
}

type chordRegistryReader struct {
	ctx   context.Context
	value registries.RegistryValue[durable.RegistrySnapshot]
}

func (reader chordRegistryReader) Snapshot() durable.RegistrySnapshot { return reader.value.Value() }

func (reader chordRegistryReader) Subscribe(listener func()) func() {
	observation, err := reader.value.Observe(compositionObserver{listener: listener}, reader.ctx)
	if err != nil {
		panic(err)
	}
	return observation.Detach
}

// compositionObserver wakes its listener for every composed value. A failed composition keeps the last snapshot: nothing to
// reconsider.
type compositionObserver struct{ listener func() }

func (observer compositionObserver) Advance(call chord.NativeWatchCall[durable.RegistrySnapshot], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		observer.listener()
	}
}

func (compositionObserver) End(chord.WatchEnd) {}

var _ state.NativeObserver[durable.RegistrySnapshot] = compositionObserver{}
