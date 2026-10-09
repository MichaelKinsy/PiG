// SPDX-License-Identifier: MIT

// Package turn is the agent turn: the built-in pi.generation and pi.tool tasks (spec §8.3-8.6) as session
// TaskMachines. Generation phases prepare, request, retry, poll and tools; tool phases call and execute; sequential and
// parallel rounds; the usage ledger; streaming partials committed with pi-durable main's fixed-interval throttle
// (generation.ts streamResponse) and tool output with the size-paced Progress (output.ts); retries, backoff and deferred
// polls as durable timers; recovery of an interrupted turn at any commit (CONTRACT section 3.8); the model request as a
// spliced model_context effect (context plane), and the wire plane behind the wireplane build tag.
//
// Owner: dcore-loop. Depends on session (Runtime), history, rec, jv, abi.
package turn

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/session"
)

// errNotImplemented is returned until dcore-loop lands the phases. Read-only.
var errNotImplemented = errors.New("turn: not implemented")

// Machines returns the built-in task machines in registration order.
func Machines() []session.TaskMachine { return []session.TaskMachine{&Generation{}, &Tool{}} }

// Generation is pi.generation version 1.
type Generation struct{}

func (*Generation) Kind() string { return "pi.generation" }
func (*Generation) Version() int { return 1 }
func (*Generation) Invoke(rt *session.Runtime, task *rec.Task) error {
	return errNotImplemented
}
func (*Generation) Deliver(rt *session.Runtime, task *rec.Task, ev *abi.Event) error {
	return errNotImplemented
}
func (*Generation) Abort(rt *session.Runtime, task *rec.Task) error { return errNotImplemented }

// Tool is pi.tool version 1.
type Tool struct{}

func (*Tool) Kind() string                                     { return "pi.tool" }
func (*Tool) Version() int                                     { return 1 }
func (*Tool) Invoke(rt *session.Runtime, task *rec.Task) error { return errNotImplemented }
func (*Tool) Deliver(rt *session.Runtime, task *rec.Task, ev *abi.Event) error {
	return errNotImplemented
}
func (*Tool) Abort(rt *session.Runtime, task *rec.Task) error { return errNotImplemented }
