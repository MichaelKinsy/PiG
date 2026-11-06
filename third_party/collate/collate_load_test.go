package collate

import (
	"sync"
	"testing"
)

// BenchmarkLoadTables is the cost the first comparison of a process pays: inflating tables.bin and filling the arrays.
func BenchmarkLoadTables(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		decodeTables()
	}
}

// The first callers race to load the tables; every one must see them complete (run with -race).
func TestFirstUseIsSafeForConcurrentCallers(t *testing.T) {
	tablesLoaded = sync.Once{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			c := New()
			if c.CompareString("a", "B") >= 0 || c.CompareString("é", "f") >= 0 || c.CompareString("\u4e00", "\u4e00") != 0 {
				t.Error("unexpected order")
			}
		})
	}
	wg.Wait()
}

// A process that creates collators and never compares pays nothing: the tables load at the first comparison or key, not in New.
func TestTablesLoadAtFirstUseNotAtNew(t *testing.T) {
	tablesLoaded = sync.Once{}
	clear(asciiValues[:])
	c := New()
	if asciiValues['a'] != 0 {
		t.Fatal("New loaded the tables")
	}
	c.CompareString("a", "b")
	if asciiValues['a'] == 0 {
		t.Fatal("CompareString did not load the tables")
	}
	tablesLoaded = sync.Once{}
	clear(asciiValues[:])
	c.AppendKey(nil, "a")
	if asciiValues['a'] == 0 {
		t.Fatal("AppendKey did not load the tables")
	}
}
