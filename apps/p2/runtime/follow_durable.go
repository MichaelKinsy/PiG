package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// Go-only: Chord's `self.follow((observer, ctx) => harness().durable.observeX(observer, ctx))` over a Durable
// observation. Durable's observer takes typed values, Chord's takes JSON: this adapts the one to the other.

// followedObserver forwards a Durable observer's calls, with their values as JSON, to a followed source.
type followedObserver[T any] struct {
	observer host.FollowObserver[any]
}

// Advance forwards one change of the observed view.
func (f followedObserver[T]) Advance(ctx context.Context, call chord.WatchCall[T]) {
	value, err := durable.ToJsonValue(call.Value)
	if err != nil {
		f.observer.End(chord.WatchEnd{Kind: chord.WatchFailed, Error: err})
		return
	}
	f.observer.Advance(chord.WatchCall[any]{Kind: call.Kind, Value: value, Ops: call.Ops, Retired: call.Retired, Error: call.Error}, ctx)
}

// End forwards the end of the observation.
func (f followedObserver[T]) End(end chord.WatchEnd) { f.observer.End(end) }

// FollowDurable is the attachment of a followed source over a Durable observation (`Harness.ObserveTaskGraph`,
// `ObserveConversation`): observe attaches the observer it is given; the source's value is the observation's, as JSON.
func FollowDurable[T any](observe func(ctx context.Context, observer durable.Observer[T]) (durable.Observation[T], error)) host.FollowAttach[delta.JsonValue] {
	return func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
		observation, err := observe(ctx, followedObserver[T]{observer})
		if err != nil {
			return nil, err
		}
		value, err := durable.ToJsonValue(observation.Value())
		if err != nil {
			observation.Detach()
			return nil, err
		}
		return &host.FollowAttachment[delta.JsonValue]{Value: value, Detach: observation.Detach}, nil
	}
}
