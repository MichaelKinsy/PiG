package host

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/host/retry-on.ts. Wake signals (slice 8 §3, D2): `self.retryOn(...sources)`. The object
// follows each declared source while it lives, replaced by each attempt's declaration; a new value of a source wakes it
// (the object decides what a wake does: a retry while failed, remembered while it activates, nothing while live). Failures
// of a source wake nothing.

// retrySourceType is Pi's `RetrySource`: what `retryOn` takes, sources that live independently of the attempt: a registry
// value, or a constant (which never wakes). Exactly one field is set. (Pi's type and function are both spelled
// `retrySource` once unexported; the type takes this name.)
type retrySourceType struct {
	value    *registries.RegistryValueSource
	constant *state.Constant
}

// retrySource checks a `retryOn` source (declaration policy: TypeScript cannot tell a reference's member or this
// attempt's `writable` from a registry value or a constant).
func retrySource(source any, label string) (retrySourceType, error) {
	switch typed := source.(type) {
	case *registries.RegistryValueSource:
		if typed != nil {
			return retrySourceType{value: typed}, nil
		}
	case *state.Constant:
		if typed != nil {
			return retrySourceType{constant: typed}, nil
		}
	}
	return retrySourceType{}, errors.New(label + ": self.retryOn takes registry values and constants (sources that outlive the attempt)")
}

// retryObserver is the one observer a replace gives every source: a value call wakes, a failure does not.
type retryObserver struct{ wake func() }

var _ state.NativeObserver[any] = (*retryObserver)(nil)

// Advance wakes on a value call only; it runs inside the batch of the new value.
func (o *retryObserver) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	if call.Kind == chord.WatchCallValue {
		o.wake()
	}
}

// End does nothing: an ended source wakes nothing.
func (o *retryObserver) End(chord.WatchEnd) {}

// retrySubscription is a source followed with the observer that follows it.
type retrySubscription struct {
	source   *registries.RegistryValueSource
	observer *retryObserver
}

// retryWakes is the wake signals of one object: its subscriptions (one set at a time, the last attempt's declaration) and
// what its object decides a wake by. It is used inside the host's sections.
type retryWakes struct {
	// during is the attempt a wake arrived during, while it activates: if it fails, it is retried at once. Nil is Pi's
	// undefined.
	during any
	// leaving is set from its object's contributions leaving until that batch delivered: a composition its own leaving made
	// does not wake it (an object failing live would retry itself without backoff).
	leaving    bool
	subscribed []retrySubscription
}

// replace follows sources instead of the previous set; wake runs inside the batch of each new value (it only records).
func (w *retryWakes) replace(sources []retrySourceType, wake func()) {
	w.stop()
	observer := &retryObserver{wake: wake}
	for _, source := range sources {
		if source.value == nil {
			continue
		}
		source.value.Subscribe(observer)
		w.subscribed = append(w.subscribed, retrySubscription{source: source.value, observer: observer})
	}
}

// stop stops following (the object ended, or a new declaration replaces the set).
func (w *retryWakes) stop() {
	for _, subscription := range w.subscribed {
		subscription.source.Unsubscribe(subscription.observer)
	}
	w.subscribed = nil
}
