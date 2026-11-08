package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports `until` of apps/p2/src/runtime/refs.ts: the first value of a JSON state member that satisfies predicate.

// JSONMember is a state member of a reference, a derive or any JSON watchable.
type JSONMember interface {
	Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error)
}

// Until blocks until predicate holds for source's value (its current one first, then each change) and returns that value.
// It returns ctx's error when ctx ends first. source is a reference's state member or a document: a derive or a writable
// is not safe to watch from a goroutine that is in no section of the host (follow those with `Self.On`). ctx is a method
// handler's or a background one: a handler parks first, so the section that admitted it is free while it waits.
func Until(ctx context.Context, source JSONMember, predicate func(delta.JsonValue) bool) (delta.JsonValue, error) {
	chordctx.Suspend(ctx)
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watch, err := source.Watch(watchCtx)
	if err != nil {
		return nil, err
	}
	defer func() { _, _ = watch.Stop() }()
	if current := watch.Value(); predicate(current) {
		return current, nil
	}
	found := make(chan delta.JsonValue, 1)
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue && predicate(call.Value) {
			select {
			case found <- call.Value:
			default:
			}
		}
		return nil
	})
	select {
	case value := <-found:
		return value, nil
	case <-watch.Closed():
		select {
		case value := <-found:
			return value, nil
		default:
		}
		return nil, watchEnded(watch.End(), ctx)
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func watchEnded(end chord.WatchEnd, ctx context.Context) error {
	if end.Error != nil {
		return end.Error
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	return context.Canceled
}
