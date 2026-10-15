package durable

import "testing"

// TestErrorNames mirrors packages/durable/src/errors.ts: each class sets `name` to its class name.
func TestErrorNames(t *testing.T) {
	for want, got := range map[string]string{
		"StorageRequestError": NewStorageRequestError("m").Name(),
		"SessionFailed":       NewSessionFailed(nil).Name(),
		"ConversationBusy":    NewConversationBusy(7).Name(),
		"ReadAfterWrite":      NewReadAfterWrite("doc").Name(),
	} {
		if got != want {
			t.Errorf("name = %q, want %q", got, want)
		}
	}
}
