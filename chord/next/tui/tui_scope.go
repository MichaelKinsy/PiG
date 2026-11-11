// Package tui ports packages/chord/src/tui: Chord's terminal client side. TuiScope (scope.ts) is a terminal
// component's Chord resources, the counterpart of the React bindings for terminal UI libraries (slice 11 §4, decision
// "Slice 11, P9"; p2's `TuiScope`): it holds references, follows watchables and registries, calls one `changed` callback
// (the app's redraw) on every change, and releases everything at Dispose.
//
// Forced Go differences from upstream (language mechanics):
//   - Methods cannot have type parameters and Go has no union of the watchable shapes Pi's `watch` takes: Watch is
//     three functions by source shape (WatchMember, WatchNative, WatchJSON), Registry a function of the token's types,
//     and Optimistic a function of the reader's and the action's.
//   - A Promise is a blocking call: the `call` of OptimisticAdd is a function the add runs on the caller's goroutine
//     (upstream's `add(action, promise)` returns the promise it was given; here it returns the call's error), the
//     action shown until the call returns.
//   - A reader of a watchable without a value returns the zero value and false where Pi's returns `undefined`.
//   - `messages` returns a BoundMessages (a translator in the host's language at each call) since `Translator` is a
//     TypeScript call signature.
package tui

import (
	"bytes"
	"context"
	"runtime"
	"sync"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Ports packages/chord/src/tui/scope.ts.

// OptimisticAdd adds an optimistic action shown while call runs; it returns call's result (see Optimistic). Pi's
// `<R>(action, call: Promise<R>) => Promise<R>` is generic per call; a Go function value cannot be, so R is a
// parameter of the type.
type OptimisticAdd[A, R any] func(action A, call func() (R, error)) (R, error)

// TuiScope is a component's scope: what it holds and follows, until Dispose.
type TuiScope struct {
	host    host.Host
	changed func()
	ctx     context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	releases []func()

	// Deliveries of `changed` in flight, by the goroutine running each (a count, as a `changed` may nest), and the goroutines
	// in Dispose. Pi's scope is one thread, so `changed` never runs while or after `dispose` does, and a `changed` may call
	// `dispose`, which stops later deliveries; here Dispose waits for the deliveries running on other goroutines and not for
	// its own (a `changed` that disposes its own scope returns normally).
	delivery  sync.Mutex
	ended     *sync.Cond
	active    map[uint64]int
	disposing map[uint64]bool
}

// NewTuiScope is a scope on host whose every change calls changed, until Dispose or ctx ends.
func NewTuiScope(ctx context.Context, h host.Host, changed func()) *TuiScope {
	ctx, cancel := context.WithCancel(ctx)
	s := &TuiScope{host: h, changed: changed, ctx: ctx, cancel: cancel, active: map[uint64]int{}, disposing: map[uint64]bool{}}
	s.ended = sync.NewCond(&s.delivery)
	return s
}

// disposed says Dispose ran.
func (s *TuiScope) disposed() bool { return s.ctx.Err() != nil }

// redraw calls changed unless the scope is disposed.
func (s *TuiScope) redraw() {
	id := goroutineID()
	s.delivery.Lock()
	if s.disposed() {
		s.delivery.Unlock()
		return
	}
	s.active[id]++
	s.delivery.Unlock()
	defer func() {
		s.delivery.Lock()
		if s.active[id]--; s.active[id] == 0 {
			delete(s.active, id)
		}
		s.ended.Broadcast()
		s.delivery.Unlock()
	}()
	s.changed()
}

// goroutineID is the running goroutine's number (the first line of its stack: "goroutine N [").
func goroutineID() uint64 {
	var buf [32]byte
	line := buf[:runtime.Stack(buf[:], false)]
	line = bytes.TrimPrefix(line, []byte("goroutine "))
	var id uint64
	for _, c := range line {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

func (s *TuiScope) release(release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, release)
}

// Hold is a reference to typ with params, held until Dispose; its state changes call changed. It returns the error of
// invalid params.
func (s *TuiScope) Hold(typ *contracts.Contract, params any) (*host.Reference, error) {
	held, err := s.host.Hold(s.ctx, typ, params, host.GetOptions{})
	if err != nil {
		return nil, err
	}
	s.release(held.Release)
	WatchNative(s, held.State())
	return held, nil
}

// follow follows a source until Dispose: its changes call changed; the reader of its current value is the result.
func follow[T any](s *TuiScope, value func() (T, error), start func(ctx context.Context) (started func(listener func()), stop func() (chord.WatchEnd, error), err error)) func() (T, bool) {
	go func() {
		started, stop, err := start(s.ctx)
		if err != nil {
			// Disposed (or the source ended) before the watch started: nothing to follow.
			return
		}
		// The watch ends in Dispose itself, not through its context's cancellation (language mechanics): a Go context's AfterFunc
		// runs on a goroutine of its own, so the watch would still end while the host closes.
		s.release(func() { _, _ = stop() })
		if s.disposed() {
			_, _ = stop()
			return
		}
		// A holder that released its own reference ended the watch already: nothing to follow (Pi's watch.start throws "Watch is
		// stopped" there, an unhandled rejection that ends a Node process; Go has no unhandled rejection).
		if !chord.StartUnlessEnded(func() { started(func() { s.redraw() }) }) {
			return
		}
		// The value it acquired may be newer than what was drawn (a reference bound since): draw it.
		s.redraw()
	}()
	return func() (current T, ok bool) {
		defer func() {
			// Nothing throws in a redraw: a source that cannot give its value reads as none.
			// upstream: packages/chord/src/tui/scope.ts:TuiScope.watch (the reader's try/catch returns undefined)
			if recover() != nil {
				var zero T
				current, ok = zero, false
			}
		}()
		current, err := value()
		return current, err == nil
	}
}

// WatchNative follows source (an in-process value: a reference's state, a registry's value) until Dispose; returns a
// reader of its current value (false while it has none: nothing throws in a redraw).
func WatchNative[T any](s *TuiScope, source chord.NativeWatchable[T]) func() (T, bool) {
	return follow(s, func() (T, error) { return source.Value(), nil }, func(ctx context.Context) (func(func()), func() (chord.WatchEnd, error), error) {
		watch, err := source.Watch(ctx)
		if err != nil {
			return nil, nil, err
		}
		return func(listener func()) {
			watch.Start(func(context.Context, chord.NativeWatchCall[T]) error {
				if !s.disposed() {
					listener()
				}
				return nil
			})
		}, watch.Stop, nil
	})
}

// WatchJSON follows source (a JSON watchable: a member's watchable, a derive) until Dispose; see WatchNative.
func WatchJSON[T any](s *TuiScope, source chord.Watchable[T]) func() (T, bool) {
	return follow(s, func() (T, error) { return source.Value(), nil }, func(ctx context.Context) (func(func()), func() (chord.WatchEnd, error), error) {
		watch, err := source.Watch(ctx)
		if err != nil {
			return nil, nil, err
		}
		return func(listener func()) {
			watch.Start(func(context.Context, chord.WatchCall[T]) error {
				if !s.disposed() {
					listener()
				}
				return nil
			})
		}, watch.Stop, nil
	})
}

// WatchMember follows a reference's member until Dispose; see WatchNative.
func WatchMember(s *TuiScope, member *host.ReferenceMember) func() (delta.JsonValue, bool) {
	return follow(s, member.Value, func(ctx context.Context) (func(func()), func() (chord.WatchEnd, error), error) {
		watch, err := member.Watch(ctx)
		if err != nil {
			return nil, nil, err
		}
		return func(listener func()) {
			watch.Start(func(context.Context, chord.WatchCall[delta.JsonValue]) error {
				if !s.disposed() {
					listener()
				}
				return nil
			})
		}, watch.Stop, nil
	})
}

// Registry is registry R's composed value, followed until Dispose; a reader, false until it is usable.
func Registry[C, V any](s *TuiScope, token registries.Registry[C, V]) func() (V, bool) {
	var mu sync.Mutex
	read := func() (V, bool) { var zero V; return zero, false }
	go func() {
		value, err := s.host.Registry(s.ctx, registries.Registry[any, any](token), host.GetOptions{})
		if err != nil {
			return
		}
		typed := WatchNative(s, chord.NativeWatchable[any](value))
		mu.Lock()
		read = func() (V, bool) {
			current, ok := typed()
			if !ok {
				var zero V
				return zero, false
			}
			v, ok := current.(V)
			return v, ok
		}
		mu.Unlock()
		s.redraw()
	}()
	return func() (V, bool) {
		mu.Lock()
		defer mu.Unlock()
		return read()
	}
}

// Optimistic is read with optimistic values, as React's `useOptimistic`: add(action, call) runs call and, until every
// call added so settled, the reader shows reduce(current, action) applied over read's value for each action in flight, in
// order; then read's again. pending says a call is in flight. Each change calls changed. A call over a link settles after
// the changes its answering host committed before answering (decision "A call's answer follows its state"), so the value
// shown next is the real one. A failed call is its caller's to handle; its action goes as a succeeded one's does.
func Optimistic[T, A, R any](s *TuiScope, read func() T, reduce func(current T, action A) T) (shown func() T, add OptimisticAdd[A, R], pending func() bool) {
	var mu sync.Mutex
	// Kept until every call settled, as React keeps the updates of transitions still in flight.
	var actions []A
	inFlight := 0
	type memo struct {
		base    T
		actions []A
		value   T
	}
	var last *memo
	settled := func() {
		mu.Lock()
		inFlight--
		if inFlight == 0 {
			actions = nil
		}
		mu.Unlock()
		s.redraw()
	}
	add = func(action A, call func() (R, error)) (R, error) {
		mu.Lock()
		actions = append(append([]A(nil), actions...), action)
		inFlight++
		mu.Unlock()
		s.redraw()
		defer settled()
		return call()
	}
	// The value shown is computed again only when read's value or the actions changed: a redraw reading it often gets the
	// same value (by identity) until then.
	shown = func() T {
		base := read()
		mu.Lock()
		defer mu.Unlock()
		if inFlight == 0 {
			return base
		}
		if last == nil || !surfaces.SameValue(last.base, base) || !sameActions(last.actions, actions) {
			value := base
			for _, action := range actions {
				value = reduce(value, action)
			}
			last = &memo{base: base, actions: actions, value: value}
		}
		return last.value
	}
	return shown, add, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return inFlight > 0
	}
}

// OptimisticLast is Optimistic without a reducer: the last action in flight is the value.
func OptimisticLast[T, R any](s *TuiScope, read func() T) (func() T, OptimisticAdd[T, R], func() bool) {
	return Optimistic[T, T, R](s, read, func(_ T, action T) T { return action })
}

// sameActions is JavaScript's identity of two action lists: the same array of the same length. Lists are replaced, never
// appended to in place, so a different backing array or length is a change.
func sameActions[A any](a, b []A) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// PageState is the host's page state at key, kept for the terminal's life, not the scope's (a new key starts at initial);
// its changes call changed until Dispose.
func PageState[T any](s *TuiScope, key string, initial T) surfaces.PageState[T] {
	state := surfaces.PageStateOf(s.host, key, initial)
	// A closure of its own: two scopes may share one changed.
	s.release(state.Subscribe(func() { s.redraw() }))
	return state
}

// BoundMessages is a plugin's messages in the host's language as it is at each call (the first catalog before the host
// follows one).
type BoundMessages struct {
	messages *surfaces.Messages
	language surfaces.PageState[*surfaces.Language]
}

// T renders the message key with its arguments in the host's language now.
func (b *BoundMessages) T(key string, args ...any) (string, error) {
	return b.messages.In(b.language.Read()).T(key, args...)
}

// Render renders a message as data in the host's language now.
func (b *BoundMessages) Render(message surfaces.MessageData) string {
	return b.messages.In(b.language.Read()).Render(message)
}

// Messages is messages in the host's language as it is at each call; a change of the locale or a translation calls
// changed until Dispose.
func (s *TuiScope) Messages(messages *surfaces.Messages) *BoundMessages {
	language := surfaces.HostLanguage(s.host)
	s.release(language.Subscribe(func() { s.redraw() }))
	return &BoundMessages{messages: messages, language: language}
}

// othersDelivering says a delivery runs on a goroutine other than id that Dispose on id must wait for: any, unless id is itself
// delivering and the other is in Dispose too (two `changed` that dispose their scope would wait for each other). A caller outside
// a delivery waits for every other delivery, one in Dispose included. It runs with delivery held.
func (s *TuiScope) othersDelivering(id uint64) bool {
	for other := range s.active {
		if other != id && (s.active[id] <= 0 || !s.disposing[other]) {
			return true
		}
	}
	return false
}

// Dispose releases every reference and ends every watch; changed is not called after it. It blocks until a `changed` running on
// another goroutine returned (a `changed` of this scope, on the calling goroutine, may call it: it only stops later deliveries).
func (s *TuiScope) Dispose() {
	s.cancel()
	// A delivery on another goroutine, already past the check, ends before Dispose goes on; none starts after it.
	id := goroutineID()
	s.delivery.Lock()
	s.disposing[id] = true
	for s.othersDelivering(id) {
		s.ended.Wait()
	}
	delete(s.disposing, id)
	s.delivery.Unlock()
	s.mu.Lock()
	releases := s.releases
	s.releases = nil
	s.mu.Unlock()
	for _, release := range releases {
		release()
	}
}
