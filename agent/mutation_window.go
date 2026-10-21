// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// MayTakeFileMutationQueue is implemented by a tool whose code can register in the file mutation queue itself, after the dispatcher started it: an
// extension tool, whose extension calls withFileMutationQueue. Pi's Promise.all(calls.map(...)) runs each call's synchronous prefix in call order, and
// withFileMutationQueue registers in the queue in the order it is called (core/tools/file-mutation-queue.ts: registrationQueue), so an extension tool that
// takes the queue in its first statement registers before the edit or write call that follows it in the batch. The extension is another process, so its
// registration arrives a hop after the dispatcher started the call; the dispatcher holds every later call that registers in the queue behind the end of the
// extension tool's synchronous prefix (the call's start window).
type MayTakeFileMutationQueue interface {
	MayTakeFileMutationQueue() bool
}

// RegistersInFileMutationQueue is implemented by a tool whose ReserveMutationOrder registers the call in the file mutation queue (the edit and write tools).
type RegistersInFileMutationQueue interface {
	RegistersInFileMutationQueue() bool
}

type startWindowKey struct{}

// StartWindow is the window of one extension tool call: it is open from the call's start until the call's synchronous prefix is over. Close ends it, and
// AwaitSettled returns once every edit or write call that waited for it registered in the file mutation queue, which Pi has done before the extension's own
// later registration when the extension tool awaited something first.
type StartWindow struct {
	closed  *jsloop.Place
	settled *jsloop.Place
	// dependents counts the calls that wait for the window and have not registered.
	dependents atomic.Int32
}

func newStartWindow() *StartWindow {
	return &StartWindow{closed: jsloop.After(nil), settled: jsloop.After(nil)}
}

// Close ends the window. It is idempotent.
func (w *StartWindow) Close() { w.closed.Release() }

// AwaitSettled returns once every call that waited for the window registered (at once when none did, once the dispatcher has prepared its batch).
func (w *StartWindow) AwaitSettled() { jsloop.After(w.settled).Wait() }

type startWindowOf struct {
	toolCallID string
	window     *StartWindow
}

// withStartWindow carries to the call toolCallID the window its tool closes when the call's synchronous prefix is over.
func withStartWindow(ctx context.Context, toolCallID string, window *StartWindow) context.Context {
	return context.WithValue(ctx, startWindowKey{}, startWindowOf{toolCallID: toolCallID, window: window})
}

// ToolStartWindowFromContext returns the window of the call toolCallID, for a tool that MayTakeFileMutationQueue. The tool closes it when its synchronous prefix is
// over, which is Pi's first await of the tool's execute: an extension's host call that returns a Promise, a report that its handler awaits, or its answer. It
// returns nil for a call that holds no window. Close is idempotent, and the dispatcher closes the window when the call ends, so a tool that never closes it holds
// nobody back for long.
func ToolStartWindowFromContext(ctx context.Context, toolCallID string) *StartWindow {
	window, ok := ctx.Value(startWindowKey{}).(startWindowOf)
	if !ok || window.toolCallID != toolCallID {
		return nil
	}
	return window.window
}

type registrationTurnKey struct{}

// registrationTurn is a call's place in the order the batch's calls register in the file mutation queue.
type registrationTurn struct {
	toolCallID string
	// windows are the start windows of the tools before the call that may take the queue; the call registers after they closed. Empty for a call behind
	// no extension tool, which registers in the dispatcher's loop.
	windows []*StartWindow
	// place orders this call against the other calls that register late.
	place *jsloop.Place
	// orderer registers the call (a consumer), nil for an extension tool that only waits its turn.
	orderer QueueOrderable
	args    json.RawMessage
}

func withRegistrationTurn(ctx context.Context, turn registrationTurn) context.Context {
	return context.WithValue(ctx, registrationTurnKey{}, turn)
}

// registrationOrder tracks, while the dispatcher prepares a batch's calls in source order, the calls that register in the queue after an extension tool.
type registrationOrder struct {
	// windows are the start windows of the extension tools seen so far.
	windows []*StartWindow
	// turns orders the calls that register late.
	turns jsloop.Lane
	// late is set once a call registers late: an extension tool after it must start after that registration.
	late bool
}

// extensionTool records the call of an extension tool and returns its context: it carries the window the tool closes, and, after a call that registers
// late, the turn the call takes before its tool starts, so the registration of an earlier edit or write precedes whatever the extension tool registers.
func (o *registrationOrder) extensionTool(ctx context.Context, toolCallID string) context.Context {
	window := newStartWindow()
	o.windows = append(o.windows, window)
	ctx = withStartWindow(ctx, toolCallID, window)
	if o.late {
		ctx = withRegistrationTurn(ctx, registrationTurn{toolCallID: toolCallID, place: o.turns.Reserve()})
	}
	return ctx
}

// registersLate reports whether a call registers after the extension tools before it, and returns the context that carries its turn. It is false for a
// call with no extension tool before it, which the dispatcher registers in its own loop.
func (o *registrationOrder) registersLate(ctx context.Context, toolCallID string, orderer QueueOrderable, args json.RawMessage) (context.Context, bool) {
	if len(o.windows) == 0 {
		return ctx, false
	}
	o.late = true
	for _, window := range o.windows {
		window.dependents.Add(1)
	}
	return withRegistrationTurn(ctx, registrationTurn{
		toolCallID: toolCallID,
		windows:    o.windows[:len(o.windows):len(o.windows)],
		place:      o.turns.Reserve(),
		orderer:    orderer,
		args:       args,
	}), true
}

// prepared tells the windows that no more calls will wait for them: the windows no call waits for are settled.
func (o *registrationOrder) prepared() {
	for _, window := range o.windows {
		if window.dependents.Load() == 0 {
			window.settled.Release()
		}
	}
}

// enterRegistrationOrder runs on the call's goroutine before its tool starts. An edit or write registers in the queue now, after the start windows of the
// extension tools before it closed and after the late registrations before it, and its ticket reaches the tool through the context. An extension tool that
// follows such a call starts after the registration. A call with no turn returns its context unchanged.
func enterRegistrationOrder(ctx context.Context, toolCallID string) context.Context {
	turn, ok := ctx.Value(registrationTurnKey{}).(registrationTurn)
	if !ok || turn.toolCallID != toolCallID {
		return ctx
	}
	defer turn.place.Release()
	defer func() {
		// The registration is made, or the call ends without one: the windows it waited for are one dependent closer to settled.
		for _, window := range turn.windows {
			if window.dependents.Add(-1) == 0 {
				window.settled.Release()
			}
		}
	}()
	for _, window := range turn.windows {
		jsloop.After(window.closed).Wait()
	}
	turn.place.Wait()
	if turn.orderer == nil || ctx.Err() != nil {
		return ctx
	}
	if ticket, ok := turn.orderer.ReserveMutationOrder(turn.args); ok {
		return WithMutationTicket(ctx, ticket)
	}
	return ctx
}
