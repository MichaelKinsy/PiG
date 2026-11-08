package runtime

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports apps/p2/src/runtime/refs.ts: references as p2's UIs (the TUI, the web) hold them: following a value with its
// current one first, waiting until a reference is live, and releasing a held reference a little later so that coming
// back to it is instant. liveIn, knownRegistry and followRegistry are in registry_follow.go.
//
// Async contract. `follow` returns at once; its watch is a background task upstream (an unawaited promise): here it is
// a goroutine owned by the returned stop, which cancels it and waits for it to finish setting the watch up, so no
// goroutine outlives the stop. The listener runs on the watch's own delivery goroutine, serialized, as the watch
// delivers it. `whenLive` is an awaited promise: a blocking call that returns the error the promise rejects with.

// HeldRef is what every held reference has: its state (loading, live, failed, ended) and its release.
type HeldRef interface {
	State() chord.NativeWatchable[*host.RefState]
	Release()
}

// follow calls fn with the current value at once, then, once the watch is up, with the watch's value when it is not the
// one fn has, and with each change, until the returned stop. watch starts one watch of the source and returns its value
// and a function installing the listener (it never delivers inline).
func follow[T any](current func() T, watch func(ctx context.Context) (value T, start func(deliver func(T)), err error), fn func(T)) (stop func()) {
	//portlint:allow ctxbackground the follow owns this context for its life: the returned stop cancels it
	ctx, cancel := context.WithCancel(context.Background())
	// The value it has now, at once (a bound member, a registry's value), as a UI renders it before the watch is up.
	last := current()
	fn(last)
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		value, start, err := watch(ctx)
		if err != nil || ctx.Err() != nil {
			return
		}
		// A watch starts at its value and brings later changes only.
		if !state.Identical(value, last) {
			fn(value)
		}
		// The watch ends with its source (a holder released the reference it follows, or stop cancelled ctx) between its
		// making and this start: there is nothing left to follow.
		chord.StartUnlessEnded(func() {
			start(func(change T) {
				if ctx.Err() == nil {
					fn(change)
				}
			})
		})
	}()
	return func() {
		cancel()
		done.Wait()
	}
}

// Follow calls fn with source's value (at once, then again when its watch is up and holds another one), then with each
// change, until the returned stop. A source that ends (its reference released) only stops calling. The source is a
// native watchable (a reference's state, a registry's value, a bound member).
func Follow[T any](source chord.NativeWatchable[T], fn func(T)) (stop func()) {
	return follow(source.Value, func(ctx context.Context) (T, func(func(T)), error) {
		handle, err := source.Watch(ctx)
		if err != nil {
			var zero T
			return zero, nil, err
		}
		return handle.Value(), func(deliver func(T)) {
			handle.Start(func(_ context.Context, call chord.NativeWatchCall[T]) error {
				if call.Kind == chord.WatchCallValue {
					deliver(call.Value)
				}
				return nil
			})
		}, nil
	}, fn)
}

// FollowJSON is Follow for a JSON watchable (a state member of a held reference).
func FollowJSON[T any](source chord.Watchable[T], fn func(T)) (stop func()) {
	return follow(source.Value, func(ctx context.Context) (T, func(func(T)), error) {
		handle, err := source.Watch(ctx)
		if err != nil {
			var zero T
			return zero, nil, err
		}
		return handle.Value(), func(deliver func(T)) {
			handle.Start(func(_ context.Context, call chord.WatchCall[T]) error {
				if call.Kind == chord.WatchCallValue {
					deliver(call.Value)
				}
				return nil
			})
		}, nil
	}, fn)
}

// StatusOf is the reference's state kind now.
func StatusOf(ref HeldRef) host.RefStateKind { return ref.State().Value().Kind }

// WhenLive blocks until ref is live, and returns the error when it ends or fails broken (asking again cannot help). A
// transient failure keeps waiting: the reference retries. It returns ctx's error when ctx ends first.
func WhenLive(ctx context.Context, ref HeldRef) error {
	settled := make(chan error, 1)
	var once sync.Once
	settle := func(err error) { once.Do(func() { settled <- err }) }
	stop := Follow(ref.State(), func(current *host.RefState) {
		switch {
		case current.Kind == host.RefStateLive:
			settle(nil)
		case current.Kind == host.RefStateEnded:
			settle(current.Error)
		case current.Kind == host.RefStateFailed && current.Cause.Kind == data.FailureBroken:
			settle(current.Cause.Error)
		}
	})
	defer stop()
	select {
	case err := <-settled:
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// ErrorOf is the error of a failed or ended reference, if any.
func ErrorOf(ref HeldRef) error {
	current := ref.State().Value()
	switch {
	case current.Kind == host.RefStateEnded && current.Error != nil:
		return current.Error
	case current.Kind == host.RefStateFailed && current.Cause.Error != nil:
		return current.Cause.Error
	}
	return nil
}

// Lingering releases held references later: a chat stays held for a while after its screen is left, so that switching
// back is instant, at most cap of them at once (the oldest goes first).
type Lingering struct {
	delays map[string]time.Duration
	cap    int

	mu      sync.Mutex
	order   []HeldRef
	waiting map[HeldRef]*time.Timer
}

// NewLingering returns a Lingering with the delay of each reference type; a type without one is released at once.
func NewLingering(delays map[string]time.Duration, cap int) *Lingering {
	return &Lingering{delays: delays, cap: cap, waiting: map[HeldRef]*time.Timer{}}
}

// Release releases ref (of the type named typeName) after its type's delay; at once for a type without one.
func (l *Lingering) Release(ref HeldRef, typeName string) {
	delay, ok := l.delays[typeName]
	if !ok {
		ref.Release()
		return
	}
	l.mu.Lock()
	// A lingering reference never keeps a process alive: the timer is a time.AfterFunc, which owns no goroutine until it
	// fires, and Flush stops what is left.
	// Releasing a reference that already waits replaces its timer and keeps its place (a Map's `set`); the earlier timer
	// stays and releases it when it fires, as upstream's does.
	if _, queued := l.waiting[ref]; !queued {
		l.order = append(l.order, ref)
	}
	l.waiting[ref] = time.AfterFunc(delay, func() { l.now(ref) })
	var oldest HeldRef
	if len(l.waiting) > l.cap {
		oldest = l.order[0]
	}
	l.mu.Unlock()
	if oldest != nil {
		l.now(oldest)
	}
}

// Flush releases every waiting reference now (a host closing).
func (l *Lingering) Flush() {
	l.mu.Lock()
	refs := append([]HeldRef(nil), l.order...)
	l.mu.Unlock()
	for _, ref := range refs {
		l.now(ref)
	}
}

func (l *Lingering) now(ref HeldRef) {
	l.mu.Lock()
	timer, waiting := l.waiting[ref]
	if waiting {
		timer.Stop()
		delete(l.waiting, ref)
		for i, queued := range l.order {
			if queued == ref {
				l.order = append(l.order[:i], l.order[i+1:]...)
				break
			}
		}
	}
	l.mu.Unlock()
	if waiting {
		ref.Release()
	}
}

// UILinger is p2's delays: a chat lingers two minutes, at most four of them (`UI_LINGER`).
var UILinger = NewLingering(map[string]time.Duration{"p2.chat": 2 * time.Minute}, 4)
