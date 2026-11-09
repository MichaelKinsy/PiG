package sqlite_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// BenchmarkReadCacheMiss is what the read cache costs a read it cannot answer: an exact entry lookup that cycles over more
// entries than the cache holds (so each one is read from the database and then kept or dropped), with the cache and
// without. The difference between the two is the price of a miss.
func BenchmarkReadCacheMiss(b *testing.B) {
	for _, variant := range []struct {
		name  string
		bytes int64
	}{{"uncached", 0}, {"cached", 4 << 10}} {
		b.Run(variant.name, func(b *testing.B) {
			storage := must(node.OpenNodeSqliteStorage(filepath.Join(b.TempDir(), "storage.sqlite"), node.NodeSqliteStorageOptions{ReadCacheBytes: new(variant.bytes)}))
			b.Cleanup(func() { _ = storage.Close(testContext) })
			must(durabletest.CommitWrites(testContext, storage, []durable.StorageWrite{durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}}}))
			var writes []durable.StorageWrite
			for id := 1000; id < 1400; id++ {
				writes = append(writes, durable.EntryWrite{Value: durable.EntryRecord{
					Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "benchmark.entry",
					Data: map[string]any{"index": id, "text": fmt.Sprint("entry-", id, "-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")},
				}})
			}
			must(durabletest.CommitWrites(testContext, storage, writes))
			b.ReportAllocs()
			next := 0
			for b.Loop() {
				if found, err := storage.Entry(testContext, durable.EntryId(1000+next%400)); err != nil || found == nil {
					b.Fatal(found, err)
				}
				next++
			}
		})
	}
}
