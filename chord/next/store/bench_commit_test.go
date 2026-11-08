package store

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// BenchmarkCommit is the commit step: one document created per commit, on `:memory:` and on a file, one committer at a
// time ("serial": no group ever forms) and many at once ("parallel": the commits queued behind a running step share the
// next one, group commit S18). benchstat it before and after a change of the commit step.
func BenchmarkCommit(b *testing.B) {
	for _, backend := range []string{"memory", "file"} {
		open := func(b *testing.B) *ChordStore {
			path := ":memory:"
			if backend == "file" {
				path = filepath.Join(b.TempDir(), "bench.sqlite")
			}
			db, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
			if err != nil {
				b.Fatal(err)
			}
			opened, err := OpenStore(SqliteBackend(db), background, StoreOptions{})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = opened.Close(background) })
			return opened.(*ChordStore)
		}
		create := func(n int64) []StagedWrite {
			key := fmt.Sprintf("k%d", n)
			address := storeAddress
			address.Key = &key
			return []StagedWrite{CreateDocument(address, storeBase, storeBaseText, nil)}
		}
		_, fence := recording()
		fence.Adopt = func(int64, []int64) error { return nil }
		b.Run(backend+"/serial", func(b *testing.B) {
			store := open(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				if _, err := store.Commit(create(int64(i)), fence, background); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(backend+"/parallel", func(b *testing.B) {
			store := open(b)
			var next atomic.Int64
			b.SetParallelism(8)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					if _, err := store.Commit(create(next.Add(1)), fence, background); err != nil {
						b.Error(err)
						return
					}
				}
			})
		})
	}
}
