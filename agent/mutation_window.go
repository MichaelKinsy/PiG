// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"encoding/json"

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

type startWindow struct {
	toolCallID string
	place      *jsloop.Place
}

// withStartWindow carries to the call toolCallID the window its tool closes when the call's synchronous prefix is over.
func withStartWindow(ctx context.Context, toolCallID string, place *jsloop.Place) context.Context {
	return context.WithValue(ctx, startWindowKey{}, startWindow{toolCallID: toolCallID, place: place})
}

// ToolStartWindowFromContext returns the function a tool that MayTakeFileMutationQueue calls when its synchronous prefix is over, which is Pi's first await of
// the tool's execute: an extension's host call that returns a Promise, a report that its handler awaits, or its answer. It returns nil for a call that holds
// no window. The function is idempotent, and the dispatcher calls it when the call ends, so a tool that never calls it holds nobody back for long.
func ToolStartWindowFromContext(ctx context.Context, toolCallID string) func() {
	window, ok := ctx.Value(startWindowKey{}).(startWindow)
	if !ok || window.toolCallID != toolCallID {
		return nil
	}
	return window.place.Release
}

type registrationTurnKey struct{}

// registrationTurn is a call's place in the order the batch's calls register in the file mutation queue.
type registrationTurn struct {
	toolCallID string
	// windows are the start windows of the tools before the call that may take the queue; the call registers after they closed. Empty for a call behind
	// no extension tool, which registers in the dispatcher's loop.
	windows []*jsloop.Place
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
	windows []*jsloop.Place
	// turns orders the calls that register late.
	turns jsloop.Lane
	// late is set once a call registers late: an extension tool after it must start after that registration.
	late bool
}

// extensionTool records the call of an extension tool and returns its context: it carries the window the tool closes, and, after a call that registers
// late, the turn the call takes before its tool starts, so the registration of an earlier edit or write precedes whatever the extension tool registers.
func (o *registrationOrder) extensionTool(ctx context.Context, toolCallID string) context.Context {
	window := jsloop.After(nil)
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
	return withRegistrationTurn(ctx, registrationTurn{
		toolCallID: toolCallID,
		windows:    o.windows[:len(o.windows):len(o.windows)],
		place:      o.turns.Reserve(),
		orderer:    orderer,
		args:       args,
	}), true
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
	for _, window := range turn.windows {
		jsloop.After(window).Wait()
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
