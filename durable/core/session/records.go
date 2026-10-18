// Package session is the Durable core's Session layer: the Session line, the
// transaction that stages one atomic commit (conversations, entries, tasks,
// submissions and documents), the document cache, and the task scheduler.
//
// It ports pi-durable's session/session.ts, session/transaction.ts,
// session/forks.ts and harness/scheduler.ts as a synchronous state machine: a
// commit callback runs to completion, a read the loaded state cannot answer
// returns a *NeedRead error and the callback restarts after the host answers,
// and no goroutine, clock, I/O or package-level mutable state exists.
//
// Records are ordered JSON objects (package jv) so a rewritten record keeps the
// field order and unknown fields Pi's object spreads keep.
package session

import (
	"errors"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// ID is the number of a conversation, entry, task, submission or document.
type ID = int64

// Seq is the sequence of one storage commit.
type Seq = int64

// RootConversationID is the reserved ID of the root conversation.
const RootConversationID ID = 1

func idv(id ID) float64 { return float64(id) }

func getStr(o *jv.Object, k string) (string, bool) {
	v, _ := o.Get(k)
	s, ok := v.(string)
	return s, ok
}

func getInt(o *jv.Object, k string) (ID, bool) {
	v, _ := o.Get(k)
	f, ok := v.(float64)
	if !ok || f != float64(ID(f)) {
		return 0, false
	}
	return ID(f), true
}

func getNum(o *jv.Object, k string) (float64, bool) {
	v, _ := o.Get(k)
	f, ok := v.(float64)
	return f, ok
}

func getBool(o *jv.Object, k string) bool {
	v, _ := o.Get(k)
	b, _ := v.(bool)
	return b
}

func getObj(o *jv.Object, k string) *jv.Object {
	v, _ := o.Get(k)
	r, _ := v.(*jv.Object)
	return r
}

func mustInt(o *jv.Object, k string) ID {
	n, _ := getInt(o, k)
	return n
}

// obj builds an object from alternating keys and values.
func obj(kv ...any) *jv.Object {
	o := jv.NewObjectCap(len(kv) / 2)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

// spread returns a shallow copy of o, as {...o} does.
func spread(o *jv.Object) *jv.Object { return o.Shallow() }

// with returns {...o, k: v}: an existing key keeps its position.
func with(o *jv.Object, kv ...any) *jv.Object {
	c := o.Shallow()
	for i := 0; i+1 < len(kv); i += 2 {
		c.Set(kv[i].(string), kv[i+1])
	}
	return c
}

// abortReasonOf is a task record's abortReason: "restart", or "" when absent.
func abortReasonOf(rec *jv.Object) string {
	reason, _ := getStr(rec, "abortReason")
	return reason
}

// withAbortMark is the record with an abort mark: restart for an abandonment; without a reason, a request, which replaces
// restart (scheduler.ts withAbortMark). abortReason is last, as the spread of the rest puts it.
func withAbortMark(rec *jv.Object, reason string) *jv.Object {
	c := rec.Shallow()
	c.Delete("abortReason")
	// The restart count starts over: it now counts cut-offs of the abort handler.
	c.Delete("restarts")
	c.Set("abortRequested", true)
	if reason != "" {
		c.Set("abortReason", reason)
	}
	return c
}

// without returns {...o} minus the named keys.
func without(o *jv.Object, keys ...string) *jv.Object {
	c := o.Shallow()
	for _, k := range keys {
		c.Delete(k)
	}
	return c
}

// Error values of the Session layer, named as Pi names them.
var (
	// ErrReadAfterWrite is Pi's ReadAfterWrite.
	// Read-only: assigned once at initialization.
	ErrReadAfterWrite = errors.New("ReadAfterWrite")
	// ErrSealed reports use of a transaction after it settled.
	// Read-only: assigned once at initialization.
	ErrSealed = errors.New("Transaction has settled")
	// ErrSessionFailed is Pi's SessionFailed: the Session failed after a storage error, so nothing runs or commits any more.
	// Read-only: assigned once at initialization.
	ErrSessionFailed = errors.New("Session failed after a storage error; close and reopen it")
	// ErrTypeError marks an error Pi throws as a TypeError.
	// Read-only: assigned once at initialization.
	ErrTypeError = errors.New("TypeError")
	// ErrStorageRequest is Pi's StorageRequestError: a Storage read the caller's request got wrong (an unknown conversation, an invalid cursor or scan
	// order, a document that keeps no history). It fails only that read, not the Session.
	// Read-only: assigned once at initialization.
	ErrStorageRequest = errors.New("StorageRequestError")
	// ErrClosed reports a closed Session.
	// Read-only: assigned once at initialization.
	ErrClosed = errors.New("Session is closed")
)

// NewTypeError is a TypeError Pi throws with message; it matches ErrTypeError with errors.Is.
func NewTypeError(message string) error { return &namedError{message, ErrTypeError} }

// namedError carries Pi's message and matches a sentinel with errors.Is.
type namedError struct {
	msg  string
	kind error
}

func (e *namedError) Error() string        { return e.msg }
func (e *namedError) Is(target error) bool { return target == e.kind }

// NewStorageRequestError is Pi's `new StorageRequestError(message)`.
func NewStorageRequestError(msg string) error { return &namedError{msg, ErrStorageRequest} }

// readAfterWrite is Pi's ReadAfterWrite for a method.
func readAfterWrite(method string) error {
	return &namedError{sprintf("Tx.%s() cannot read tables after the first table write", method), ErrReadAfterWrite}
}

// TaskStatus values of a task's state.
const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusWaiting    = "waiting"
	StatusSleeping   = "sleeping"
	StatusCompleting = "completing"
	StatusTerminal   = "terminal"
)

// taskStatus returns the status of a task record.
func taskStatus(task *jv.Object) string {
	s, _ := getStr(getObj(task, "state"), "status")
	return s
}

// withState returns {...record, state}; a terminal or completing state also
// drops the task's memos and restart count (scheduler.ts withState).
func withState(record *jv.Object, state *jv.Object) *jv.Object {
	c := record.Shallow()
	if s, _ := getStr(state, "status"); s == StatusTerminal || s == StatusCompleting {
		c.Delete("memos")
		c.Delete("restarts")
	}
	c.Set("state", state)
	return c
}

// progressed is the record without its restart count, as after a state the task committed (scheduler.ts progressed).
func progressed(record *jv.Object) *jv.Object {
	if !record.Has("restarts") {
		return record
	}
	return without(record, "restarts")
}

// restartsOf is a task record's restart count; 0 when absent.
func restartsOf(rec *jv.Object) float64 {
	n, _ := getNum(rec, "restarts")
	return n
}

// FailedError is what a call on a failed Session returns: Pi's SessionFailed, with the Storage error that failed the
// Session as its cause. Its message is fixed; the cause does not cross the host wire.
type FailedError struct{ Cause error }

func (e *FailedError) Error() string { return ErrSessionFailed.Error() }

// Is matches ErrSessionFailed.
func (e *FailedError) Is(target error) bool { return target == ErrSessionFailed }

// Unwrap returns the cause.
func (e *FailedError) Unwrap() error { return e.Cause }
