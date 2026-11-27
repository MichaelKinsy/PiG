package ai

import (
	"bytes"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/benchalloc"
)

var resolveSink int

// BenchmarkResolveCatalogRecord* measure the read path of the built-in catalog's records: one row, two rows of different chunks, every row in order,
// every row from several goroutines, and every row after a collection (a chunk the collector would reclaim expands again).
func BenchmarkResolveCatalogRecordSameRow(b *testing.B) {
	b.ReportAllocs()
	record := GeneratedModels[700].Record
	for b.Loop() {
		resolveSink += len(ResolveCatalogRecord(record))
	}
}

func BenchmarkResolveCatalogRecordAlternateChunks(b *testing.B) {
	b.ReportAllocs()
	first, second := GeneratedModels[10].Record, GeneratedModels[900].Record
	for b.Loop() {
		resolveSink += len(ResolveCatalogRecord(first)) + len(ResolveCatalogRecord(second))
	}
}

func BenchmarkResolveCatalogRecordSequentialAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for i := range GeneratedModels {
			resolveSink += len(ResolveCatalogRecord(GeneratedModels[i].Record))
		}
	}
}

func BenchmarkResolveCatalogRecordParallel(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i = (i + 37) % len(GeneratedModels)
			resolveSink += len(ResolveCatalogRecord(GeneratedModels[i].Record))
		}
	})
}

func BenchmarkResolveCatalogRecordAllAfterGC(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		runtime.GC()
		for i := range GeneratedModels {
			resolveSink += len(ResolveCatalogRecord(GeneratedModels[i].Record))
		}
	}
}

// BenchmarkCatalogModelJSONAll encodes every built-in model as the RPC model object, on the live catalog: the production path reads each record through the baseline cache of its built-in row, which only a model of the shipped catalog takes. Its allocs/op is allocations per 1,000 catalog entries, measured over the loop by runtime.ReadMemStats (internal/benchalloc), so a refresh that adds entries does not move the row while a record that costs more to encode does.
func BenchmarkCatalogModelJSONAll(b *testing.B) {
	models := make([]*Model, len(GeneratedModels))
	for i := range GeneratedModels {
		models[i] = GeneratedModels[i].ToModel()
	}
	b.Run("per-1000-entries", func(b *testing.B) {
		benchalloc.Per1000Entries(b, len(models), func() {
			for _, model := range models {
				data, _, _ := CatalogModelJSON(model)
				resolveSink += len(data)
			}
		})
	})
}

// Many goroutines read rows of every chunk in interleaved order, with a collection every 500 reads, and each record equals the one read before they started.
func TestResolveCatalogRecordConcurrentInterleavedChunks(t *testing.T) {
	want := make([]string, len(GeneratedModels))
	for i := range GeneratedModels {
		want[i] = string([]byte(ResolveCatalogRecord(GeneratedModels[i].Record)))
	}
	var wg sync.WaitGroup
	for g := range 32 {
		wg.Go(func() {
			for n := range 3000 {
				i := (g*131 + n*67) % len(GeneratedModels)
				if n%500 == 0 {
					runtime.GC()
				}
				if got := ResolveCatalogRecord(GeneratedModels[i].Record); got != want[i] {
					t.Errorf("row %d: record differs from the one read before the goroutines started", i)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestInflateCatalogChunkPoolReuseAndErrorRecovery(t *testing.T) {
	frame, err := catalogRecordFiles.ReadFile("catalog_records/00.gz")
	if err != nil {
		t.Fatalf("read chunk 00: %v", err)
	}
	want, err := inflateCatalogChunk(frame)
	if err != nil {
		t.Fatalf("first inflate: %v", err)
	}
	if _, err := inflateCatalogChunk([]byte("not-a-gzip-frame")); err == nil {
		t.Fatal("expected error for corrupt gzip frame")
	}
	got, err := inflateCatalogChunk(frame)
	if err != nil {
		t.Fatalf("second inflate after error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("inflated bytes changed after pool reuse: got %d bytes, want %d", len(got), len(want))
	}
}
