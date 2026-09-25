// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package agent

import (
	"context"
	"encoding/json"
)

// MutationTicket is a reserved position in a shared per-path mutation queue
// (internal/codingagent/tools.FileMutationQueue), obtained synchronously, in
// tool-call source order, before the parallel dispatcher spawns this call's
// goroutine. Wait blocks until it is this ticket's turn; Release must run
// exactly once, after the tool's mutation completes, regardless of outcome.
type MutationTicket struct {
	Wait    func()
	Release func()
}

// QueueOrderable is an optional interface a tool may implement so the
// parallel dispatcher reserves its place in a shared mutation queue in
// tool-call order, before any goroutine starts, rather than leaving admission
// order to goroutine-arrival order.
//
// Upstream dispatches parallel tool calls via
// Promise.all(calls.map(tool => executeToolCall(tool))): Array.prototype.map
// runs its callback synchronously per element, in array order, so every
// call's synchronous prefix — including withFileMutationQueue's
// registration step (file-mutation-queue.ts:32-45), which chains the new
// call onto whatever is currently in the per-path map — completes for call N
// before call N+1's synchronous prefix begins, even though the calls then
// run concurrently (agent-loop.ts executeToolCallsParallel). Go's per-call
// goroutines (tool_execution.go executeToolCallsParallel) give no equivalent
// guarantee: nothing stops goroutine N+1 from reaching the queue before
// goroutine N. ReserveMutationOrder lets the dispatcher perform that
// registration step itself, synchronously, in its own call-ordered loop,
// closing that gap.
//
// ReserveMutationOrder returns ok=false when the call will not reach the
// queue at all (a tool implementing this must return exactly the same
// ok/no-ok decision its Execute would reach, or a reservation could be left
// registered but never released). The returned params are the same
// (possibly ArgumentPreparer-normalized) arguments Execute receives.
type QueueOrderable interface {
	ReserveMutationOrder(params json.RawMessage) (*MutationTicket, bool)
}

type mutationTicketKey struct{}

// WithMutationTicket returns ctx carrying a reservation for the tool call it
// is passed into. Execute implementations that also implement QueueOrderable
// must check MutationTicketFromContext and honor an existing reservation
// instead of registering a second time.
func WithMutationTicket(ctx context.Context, ticket *MutationTicket) context.Context {
	return context.WithValue(ctx, mutationTicketKey{}, ticket)
}

// MutationTicketFromContext returns the ticket the dispatcher reserved for
// this call, if any.
func MutationTicketFromContext(ctx context.Context) (*MutationTicket, bool) {
	ticket, ok := ctx.Value(mutationTicketKey{}).(*MutationTicket)
	return ticket, ok
}
