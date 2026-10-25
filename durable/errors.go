package durable

import (
	"fmt"
)

// Ports packages/durable/src/errors.ts

// ErrorMessage is the message of a thrown (panicked) value, without panicking itself (errors.ts errorMessage): formatting
// a value can panic, as a String method that panics does (String() throws for Object.create(null) upstream), and code
// that formats an error must not fail for the value it formats. The fallback is upstream's
// Object.prototype.toString.call tag of an object.
func ErrorMessage(thrown any) (message string) {
	defer func() {
		// upstream: packages/durable/src/errors.ts:errorMessage (its catch drops String()'s exception and returns the value's tag)
		if recover() != nil {
			message = "[object Object]"
		}
	}()
	switch value := thrown.(type) {
	case error:
		return value.Error()
	case fmt.Stringer:
		return value.String()
	}
	return fmt.Sprint(thrown)
}

// ReadAfterWrite reports that a transaction read a table after its first table write. Read every required row before
// writing.
type ReadAfterWrite struct {
	Method string
}

// NewReadAfterWrite returns the error for the Tx method that read after a write.
func NewReadAfterWrite(method string) *ReadAfterWrite {
	e := &ReadAfterWrite{Method: method}
	return e
}

// Name is the `name` property, "ReadAfterWrite".
func (*ReadAfterWrite) Name() string { return "ReadAfterWrite" }

func (e *ReadAfterWrite) Error() string {
	return fmt.Sprintf("Tx.%s() cannot read tables after the first table write", e.Method)
}

// StorageRequestError reports that a Storage read rejected an invalid request, such as an unknown conversation or a
// cursor from another scan, with no durable effect. Unlike any other error a Storage returns, it fails only that read,
// not the Session; from a commit it is fatal like any other.
type StorageRequestError struct {
	Message string
}

// NewStorageRequestError returns the error for a rejected read request.
func NewStorageRequestError(message string) *StorageRequestError {
	return &StorageRequestError{Message: message}
}

// Name is the `name` property, "StorageRequestError".
func (*StorageRequestError) Name() string { return "StorageRequestError" }

func (e *StorageRequestError) Error() string { return e.Message }

// Unwrap returns nil: a StorageRequestError has no cause.
func (*StorageRequestError) Unwrap() error { return nil }

// SessionFailed reports that the Session failed, so nothing runs or commits any more. It fails when a Storage method
// fails, a commit cannot be adopted, or the Harness's own bookkeeping fails (a scheduler commit, an internal commit
// listener, a registry whose snapshot fails). Cause is that first error. Reopen the Session; reopening recovers from
// what was committed.
type SessionFailed struct {
	Cause error
}

// NewSessionFailed returns the error for a Session that failed with cause.
func NewSessionFailed(cause error) *SessionFailed { return &SessionFailed{Cause: cause} }

// Name is the `name` property, "SessionFailed".
func (*SessionFailed) Name() string { return "SessionFailed" }

func (*SessionFailed) Error() string {
	return "Session failed after a storage error; close and reopen it"
}

// Unwrap returns the cause.
func (e *SessionFailed) Unwrap() error { return e.Cause }

// ReadOnlySession reports that the Harness was opened read-only (HarnessOptions.ReadOnly): a commit with writes, or a call
// that would run tasks, was refused. Nothing was written, and the Harness stays usable for reads.
type ReadOnlySession struct {
	What string
}

// NewReadOnlySession returns the error for the refused operation what, such as "a commit with writes".
func NewReadOnlySession(what string) *ReadOnlySession { return &ReadOnlySession{What: what} }

// Name is the `name` property, "ReadOnlySession".
func (*ReadOnlySession) Name() string { return "ReadOnlySession" }

func (e *ReadOnlySession) Error() string { return "Read-only Harness: " + e.What + " refused" }

// ConversationBusy reports that a submission reached a busy conversation and was not admitted.
type ConversationBusy struct {
	ConversationId ConversationId
}

// NewConversationBusy returns the error for a busy conversation.
func NewConversationBusy(conversationId ConversationId) *ConversationBusy {
	e := &ConversationBusy{ConversationId: conversationId}
	return e
}

// Name is the `name` property, "ConversationBusy".
func (*ConversationBusy) Name() string { return "ConversationBusy" }

func (e *ConversationBusy) Error() string {
	return fmt.Sprintf("Conversation %d is busy", e.ConversationId)
}
