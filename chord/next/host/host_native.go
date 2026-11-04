package host

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/host/native.ts. Natives (§13): the handle `self.open` returns, a value opened during
// activation and closed with a reason when its attempt ends; `within`, which bounds a close or a drain by a timeout; and the
// drain's admission of running work.

// NativeCloseKind is why a native closes.
type NativeCloseKind string

const (
	// NativeCloseEnd: its object ended or its attempt failed.
	NativeCloseEnd NativeCloseKind = "end"
	// NativeCloseClose: the host closes.
	NativeCloseClose NativeCloseKind = "close"
	// NativeCloseReload: a reload (slice 7).
	NativeCloseReload NativeCloseKind = "reload"
)

// NativeCloseReason is why a native closes: its object ended or its attempt failed (`end`), the host closes (`close`), a
// reload (slice 7).
type NativeCloseReason struct{ Kind NativeCloseKind }

// Native is a native's handle.
type Native[T any] interface {
	// Value is the opened value: it returns `not_ready` until its open resolved and `released` once its close started (Pi's
	// getter throws them).
	Value() (T, error)
}

// NativeOptions is what `self.open` takes besides the open and the close.
type NativeOptions struct {
	// CloseTimeout is how long its close may take; nil is the host's `closeTimeout`.
	CloseTimeout *time.Duration
}

// closeFailed is a close that failed, as reported: Pi's `Extract<HostError, { kind: "close_failed" }>`.
type closeFailed = HostError

// nativeOwner is where a native belongs: its object's label and its position in declaration order (from 1) for errors and
// reports, the object's context (every close runs under it, never under the aborted opener's), where it reports, and the
// loop its host runs on.
//
// Go form of Pi's single event loop: the opener and the close of a native are app code that Pi awaits. They run on the
// goroutine that calls open or close, or on one of the loop's owned tasks (a close), outside a section; what they do to the
// host they do through the loop's Run. open and close block and are called outside a section, as an `await` yields the loop.
type nativeOwner struct {
	object   string
	position int
	context  context.Context
	report   func(HostError)
	loop     sectionLoop
}

// nativeSlot is what an attempt does with each of its natives, whatever their value's type.
type nativeSlot interface {
	// open runs the opener and keeps its value; the error is the opener's rejection.
	open(ctx context.Context) error
	// close closes the value if it is open; a failed close is returned (nil: undefined).
	close(reason NativeCloseReason) *closeFailed
}

// nativeStateKind is where a native stands. `abandoned`: closed while its open was pending, so the late value is closed at
// once.
type nativeStateKind uint8

const (
	nativeUnopened nativeStateKind = iota
	nativeOpening
	nativeOpen
	nativeAbandoned
	nativeClosed
)

type nativeState[T any] struct {
	kind   nativeStateKind
	value  T
	reason NativeCloseReason
}

// nativeHandle is one native of one attempt: opened at most once, closed exactly once. Its state is locked: its value is
// read by the app's code, which runs off the loop.
type nativeHandle[T any] struct {
	opener       func(ctx context.Context) (T, error)
	closer       func(value T, reason NativeCloseReason, ctx context.Context) error
	closeTimeout time.Duration
	owner        nativeOwner
	mu           sync.Mutex
	state        nativeState[T]
}

var (
	_ Native[any] = (*nativeHandle[any])(nil)
	_ nativeSlot  = (*nativeHandle[any])(nil)
)

// newNativeHandle makes a native of an opener and a closer; the opener's and closer's errors are Pi's rejections and
// throws.
func newNativeHandle[T any](
	open func(ctx context.Context) (T, error),
	closer func(value T, reason NativeCloseReason, ctx context.Context) error,
	closeTimeout time.Duration,
	owner nativeOwner,
) *nativeHandle[T] {
	return &nativeHandle[T]{opener: open, closer: closer, closeTimeout: closeTimeout, owner: owner}
}

// Value is the opened value: `not_ready` until its open resolved, `released` once its close started.
func (n *nativeHandle[T]) Value() (T, error) {
	n.mu.Lock()
	state := n.state
	n.mu.Unlock()
	if state.kind == nativeOpen {
		return state.value, nil
	}
	var zero T
	name := n.name()
	if state.kind == nativeUnopened || state.kind == nativeOpening {
		return zero, data.NewChordError(data.ChordErrorInit{Code: data.CodeNotReady, Message: name + ": not open yet"}, nil)
	}
	return zero, data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: name + ": closed"}, nil)
}

// open opens the value; one that arrives after its attempt was abandoned is closed at once, a failure reported. It
// returns the opener's error. A native closed before its open began opens nothing and returns `released`: Pi checks that its
// attempt was abandoned and starts the open in one section, so no close comes between; here the open starts on an owned task
// (awaitTask), after the section that checked, and the attempt may have closed its natives meanwhile.
func (n *nativeHandle[T]) open(ctx context.Context) error {
	n.mu.Lock()
	if n.state.kind == nativeClosed {
		n.mu.Unlock()
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: n.name() + ": closed"}, nil)
	}
	n.state = nativeState[T]{kind: nativeOpening}
	n.mu.Unlock()
	var value T
	if err := nativeTry(func() (err error) {
		value, err = n.opener(ctx)
		return err
	}); err != nil {
		return err
	}
	n.opened(value)
	return nil
}

// close closes the value if it is open (a pending open's value is closed when it arrives); a failure is returned.
func (n *nativeHandle[T]) close(reason NativeCloseReason) *closeFailed {
	n.mu.Lock()
	state := n.state
	if state.kind == nativeOpening {
		n.state = nativeState[T]{kind: nativeAbandoned, reason: reason}
	} else {
		n.state = nativeState[T]{kind: nativeClosed}
	}
	n.mu.Unlock()
	if state.kind == nativeOpen {
		return n.closeValue(state.value, reason)
	}
	return nil
}

// opened keeps the value an open resolved with, or closes it at once when the native was closed meanwhile.
func (n *nativeHandle[T]) opened(value T) {
	n.mu.Lock()
	state := n.state
	if state.kind != nativeAbandoned {
		n.state = nativeState[T]{kind: nativeOpen, value: value}
		n.mu.Unlock()
		return
	}
	n.state = nativeState[T]{kind: nativeClosed}
	n.mu.Unlock()
	if failure := n.closeValue(value, state.reason); failure != nil {
		n.owner.loop.Run(func() { n.owner.report(*failure) })
	}
}

// closeValue runs the close under the object's context, aborted at the close timeout, and waits for it up to then; longer
// is reported. The close runs as an owned task of the loop: it goes on after the timeout (as Pi's promise does), told to
// stop by its aborted context, which a close is expected to honour; a failure is returned.
func (n *nativeHandle[T]) closeValue(value T, reason NativeCloseReason) *closeFailed {
	owner := n.owner
	at := HostError{Object: owner.object, Native: Position{Kind: PositionNative, Position: owner.position}}
	// The object's context, aborted at the close timeout: Pi's `withAbortSignal(timeout.signal, context)`. A close that settled
	// needs no more abort; one that timed out was aborted with its cause already.
	closeCtx, abort := chordctx.WithCancel(owner.context)
	defer abort(nil)
	closing := newNativeWork()
	owner.loop.Go(func() {
		closing.settle(nativeTry(func() error { return n.closer(value, reason, closeCtx) }))
	})
	result, err := within(owner.loop, closing, n.closeTimeout)
	if err != nil {
		failure := at
		failure.Kind, failure.Error = ErrCloseFailed, err
		return &failure
	}
	if result == withinTimeout {
		abort(data.NewChordError(data.ChordErrorInit{Code: data.CodeAborted, Message: n.name() + ": close timed out"}, nil))
		timedOut := at
		timedOut.Kind = ErrCloseTimeout
		owner.loop.Run(func() { owner.report(timedOut) })
	}
	return nil
}

func (n *nativeHandle[T]) name() string {
	return fmt.Sprintf("%s native #%d", n.owner.object, n.owner.position)
}

// nativeTry runs work, returning its error, or a panic recovered as one: the throw of a plugin's callback.
func nativeTry(work func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if thrown, ok := recovered.(error); ok {
				err = thrown
				return
			}
			err = errors.New(fmt.Sprint(recovered))
		}
	}()
	return work()
}

// nativeWork is a Promise that `within` and `drained` wait on: work that settles once, with an error or none (Pi's
// rejection).
type nativeWork struct {
	once sync.Once
	done chan struct{}
	err  error
}

func newNativeWork() *nativeWork { return &nativeWork{done: make(chan struct{})} }

// settle ends the work with err (nil: it resolved); only the first settle counts.
func (w *nativeWork) settle(err error) {
	w.once.Do(func() {
		w.err = err
		close(w.done)
	})
}

// withinResult is whether the work settled in time.
type withinResult string

const (
	withinSettled withinResult = "settled"
	withinTimeout withinResult = "timeout"
)

// raceTimer waits for settled (which reports whether the work it waits on is done, giving up when its timedOut channel
// closes) against a timer of ms on loop: the Promise.race of Pi's `within`.
func raceTimer(loop eventLoop, ms time.Duration, settled func(timedOut <-chan struct{}) bool) withinResult {
	timedOut := make(chan struct{})
	timer := loop.AfterFunc(ms, func() { close(timedOut) })
	defer timer.Stop()
	if settled(timedOut) {
		return withinSettled
	}
	return withinTimeout
}

// within reports whether promise settled within ms (it returns its error when it rejects first); promise itself runs on.
func within(loop eventLoop, promise *nativeWork, ms time.Duration) (withinResult, error) {
	result := raceTimer(loop, ms, func(timedOut <-chan struct{}) bool {
		select {
		case <-promise.done:
			return true
		case <-timedOut:
			// A promise settled before the timer ran wins, as its reaction runs before the timer's callback.
			select {
			case <-promise.done:
				return true
			default:
				return false
			}
		}
	})
	if result == withinSettled {
		return result, promise.err
	}
	return result, nil
}

// drained reports whether the pieces of work in drain settled within ms (at once when there is none): Pi's
// `Promise.allSettled`, which never rejects.
func drained(loop eventLoop, drain []*nativeWork, ms time.Duration) withinResult {
	if len(drain) == 0 {
		return withinSettled
	}
	return raceTimer(loop, ms, func(timedOut <-chan struct{}) bool {
		for _, promise := range drain {
			select {
			case <-promise.done:
			case <-timedOut:
				select {
				case <-promise.done:
				default:
					return false
				}
			}
		}
		return true
	})
}
