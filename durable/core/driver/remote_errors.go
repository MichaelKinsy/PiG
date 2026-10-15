package driver

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/durable"
)

// As lets errors.As find the typed Pi error a remote error names: Pi's Session throws ReadAfterWrite, StorageRequestError, SessionFailed and
// ConversationBusy instances and callers match them with instanceof (errors.ts), so a caller of the driver matches the same
// Go types the in-process Session returns. ConversationBusy carries its id in the structured conversationId field; ReadAfterWrite is message-only in Pi (errors.ts keeps no method property), so its method comes from the message.
func (e *CoreError) As(target any) bool {
	switch t := target.(type) {
	case **durable.ReadAfterWrite:
		if e.Name != "ReadAfterWrite" {
			return false
		}
		method, _, _ := strings.Cut(strings.TrimPrefix(e.Message, "Tx."), "()")
		*t = durable.NewReadAfterWrite(method)
		return true
	case **durable.StorageRequestError:
		if e.Name != "StorageRequestError" {
			return false
		}
		*t = durable.NewStorageRequestError(e.Message)
		return true
	case **durable.SessionFailed:
		// The error wire body carries the name and message only, so the first error `cause` of the core's SessionFailed does not cross it.
		if e.Name != "SessionFailed" {
			return false
		}
		*t = durable.NewSessionFailed(nil)
		return true
	case **durable.ConversationBusy:
		if e.Name != "ConversationBusy" {
			return false
		}
		if e.ConversationID != nil {
			*t = durable.NewConversationBusy(durable.ConversationId(*e.ConversationID))
			return true
		}
		// Without the structured field, the message is the fallback (as the TS host's reviveError reads it).
		var id durable.ConversationId
		if _, err := fmt.Sscanf(e.Message, "Conversation %d is busy", &id); err != nil {
			return false
		}
		*t = durable.NewConversationBusy(id)
		return true
	}
	return false
}
