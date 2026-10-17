package storage_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// storage/memory.ts:471-488 entry(): both overloads call assertOpen() first, so a closed store throws for the global lookup and for the lookup through a
// conversation's ancestry alike, even when the entry or the conversation does not exist.
func TestClosedStorageRefusesBothEntryLookups(t *testing.T) {
	memory := storage.NewMemoryStorage()
	if err := memory.Close(raCtx); err != nil {
		t.Fatal(err)
	}
	const want = "MemoryStorage is closed"
	if got, err := memory.Entry(raCtx, 1); err == nil || err.Error() != want {
		t.Errorf("Entry on a closed storage = %+v, %v, want the error %q", got, err, want)
	}
	if got, err := memory.VisibleEntry(raCtx, durable.ROOT_CONVERSATION_ID, 1); err == nil || err.Error() != want {
		t.Errorf("VisibleEntry on a closed storage = %+v, %v, want the error %q", got, err, want)
	}
}
