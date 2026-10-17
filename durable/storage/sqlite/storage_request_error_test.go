package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// pi: packages/durable/src/storage/sqlite/storage.ts:72,312,341,378 and storage/memory.ts:126,498,684 and storage/scan.ts:21-32 (42a3497d):
// a read that names an unknown conversation, carries a cursor the backend did not write, or asks for an order outside the scan orders
// throws StorageRequestError, the one Storage error that fails only that read. The storage Pi oracles compare only commit errors, so each
// built-in backend's read sites are pinned here.
func TestBuiltinStoragesRejectInvalidReadsWithStorageRequestError(t *testing.T) {
	sqliteStorage := raMust(node.OpenNodeSqliteStorage(filepath.Join(t.TempDir(), "storage.sqlite"), node.NodeSqliteStorageOptions{}))
	for name, store := range map[string]durable.Storage{"sqlite": sqliteStorage, "memory": storage.NewMemoryStorage()} {
		t.Run(name, func(t *testing.T) {
			raCreateRoot(t, store)
			bogus := durable.ScanOrder("sideways")
			for _, c := range []struct {
				what, message string
				read          func() error
			}{
				{"head marker of an unknown conversation", "Unknown conversation: 99", func() error {
					_, err := store.FindLatestHeadMarker(raCtx, 99, nil)
					return err
				}},
				{"entries of an unknown conversation", "Unknown conversation: 99", func() error {
					_, err := store.ScanEntries(raCtx, durable.EntryQuery{ConversationId: 99}, 2, nil)
					return err
				}},
				{"a cursor the backend did not write", "Invalid storage cursor", func() error {
					_, err := store.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 2, durable.Cursor{"bogus": true})
					return err
				}},
				{"an order outside the scan orders", "Invalid scan order: sideways", func() error {
					_, err := store.ScanEntries(raCtx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID, Order: &bogus}, 2, nil)
					return err
				}},
			} {
				err := c.read()
				request, ok := errors.AsType[*durable.StorageRequestError](err)
				if !ok || request.Message != c.message {
					t.Errorf("%s: %T %v, want StorageRequestError %q", c.what, err, err, c.message)
				}
			}
		})
	}
}
