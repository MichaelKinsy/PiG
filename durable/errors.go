package durable

import (
	"fmt"
)

// Ports packages/durable/src/errors.ts

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

// StorageRejected reports that storage rejected a batch before any durable effect; the owning Session may continue
// safely.
type StorageRejected struct {
	Message string
	Cause   error
}

// NewStorageRejected returns a StorageRejected error; cause may be nil.
func NewStorageRejected(message string, cause error) *StorageRejected {
	e := &StorageRejected{Message: message, Cause: cause}
	return e
}

// Name is the `name` property, "StorageRejected".
func (*StorageRejected) Name() string { return "StorageRejected" }

func (e *StorageRejected) Error() string { return e.Message }

// Unwrap returns the cause.
func (e *StorageRejected) Unwrap() error { return e.Cause }

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
