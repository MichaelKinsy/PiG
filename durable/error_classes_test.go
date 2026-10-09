package durable_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// packages/durable errors.ts:4-28 and env/index.ts:45-75: ReadAfterWrite, StorageRejected, ConversationBusy, FileError and ExecutionError are Error
// classes whose constructors set `name`; `cause` comes from the options where the class takes one (Unwrap, as D103 maps it).
func TestDurableErrorClassesCarryNameAndCause(t *testing.T) {
	cause := errors.New("underlying")
	cases := []struct {
		err           interface{ Error() string }
		name, message string
	}{
		{durable.NewReadAfterWrite("get"), "ReadAfterWrite", "Tx.get() cannot read tables after the first table write"},
		{durable.NewStorageRejected("rejected", cause), "StorageRejected", "rejected"},
		{durable.NewConversationBusy(7), "ConversationBusy", "Conversation 7 is busy"},
		{durableenv.NewFileError(durableenv.FileErrorNotFound, "missing", "/p", cause), "FileError", "missing"},
		{durableenv.NewExecutionError(durableenv.ExecutionErrorTimeout, "slow", cause), "ExecutionError", "slow"},
	}
	for _, c := range cases {
		named := c.err.(interface{ Name() string })
		if named.Name() != c.name || c.err.Error() != c.message {
			t.Errorf("%T: name=%q message=%q, want %q %q", c.err, named.Name(), c.err.Error(), c.name, c.message)
		}
	}
	for _, err := range []error{cases[1].err.(error), cases[3].err.(error), cases[4].err.(error)} {
		if !errors.Is(err, cause) {
			t.Errorf("%T does not unwrap to its cause", err)
		}
	}
}
