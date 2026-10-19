// SPDX-License-Identifier: MIT

// Package eventwire lets the subprocess handlers of one event dispatch share the encoding of an event that no handler changed.
//
// Pi hands every handler of a before_agent_start or tool_call dispatch the same event object (runner.ts emitBeforeAgentStart, emitToolCall), so a handler's edit reaches the next handler. A subprocess handler receives the event as JSON and its edits come back in its reply. A Dispatch counts the changes to the event: the dispatcher calls Changed after a handler that reads Go values and after each edit of its own, and a subprocess handler calls Applied or Changed when it applies a reply. An encoding made at the current version is the encoding of the event the next handler receives.
//
// A context or context_with_system dispatch counts a change when a handler that reads Go values ran or a reply replaced the list: the encoding of the conversation, which grows with the session, is then made once for every handler that follows an unchanged conversation.
//
// Applying a reply assigns the values it carries, so applying the reply applied last again, with no change since, leaves the event as it is: Repeated lets the handler skip it.
package eventwire

import (
	"bytes"
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Dispatch is the shared state of one dispatch's handlers. Its methods are safe for concurrent use; on a nil Dispatch they record nothing and find nothing.
type Dispatch struct {
	mu      sync.Mutex
	version uint64
	// args is the event's encoding at argsVersion.
	args        []byte
	argsVersion uint64
	hasArgs     bool
	// reply is the reply applied last, result the handler result it carried, and replyVersion the version its application left.
	reply        []byte
	result       json.RawMessage
	replyVersion uint64
	hasReply     bool
}

type dispatchContextKey struct{}

// WithDispatch returns ctx carrying a new Dispatch for one event dispatch.
func WithDispatch(ctx context.Context) (context.Context, *Dispatch) {
	dispatch := &Dispatch{}
	return context.WithValue(ctx, dispatchContextKey{}, dispatch), dispatch
}

// FromContext returns the dispatch's Dispatch, or nil.
func FromContext(ctx context.Context) *Dispatch {
	if ctx == nil {
		return nil
	}
	dispatch, _ := ctx.Value(dispatchContextKey{}).(*Dispatch)
	return dispatch
}

// Version returns the number of changes recorded so far.
func (d *Dispatch) Version() uint64 {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.version
}

// Changed records that the event may have changed.
func (d *Dispatch) Changed() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.version++
	d.mu.Unlock()
}

// Args returns the encoding of the event at the current version, when one was stored.
func (d *Dispatch) Args() ([]byte, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.hasArgs || d.argsVersion != d.version {
		return nil, false
	}
	return d.args, true
}

// StoreArgs keeps args as the encoding of the event at the current version. The caller does not modify args afterwards.
func (d *Dispatch) StoreArgs(args []byte) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.args, d.argsVersion, d.hasArgs = args, d.version, true
	d.mu.Unlock()
}

// Repeated returns the handler result of the reply applied last when reply has the same bytes and nothing changed since its application: applying reply again would leave the event as it is.
func (d *Dispatch) Repeated(reply []byte) (json.RawMessage, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.hasReply || d.replyVersion != d.version || !bytes.Equal(reply, d.reply) {
		return nil, false
	}
	return d.result, true
}

// Applied records that reply, carrying the handler result result, was applied to the event: the event may have changed, and a repeat of reply leaves it as it is now.
func (d *Dispatch) Applied(reply []byte, result json.RawMessage) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.version++
	d.reply, d.result, d.replyVersion, d.hasReply = bytes.Clone(reply), result, d.version, true
	d.mu.Unlock()
}
