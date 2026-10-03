package storage_test

// Ports packages/durable/test/storage.bench.ts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/durabletest"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

var benchmarkContext = context.Background()

var storageBenchmarkBackends = []string{"memory", "sqlite", "jsonl"}

func openPersistentStorage(backend, path string) (durable.Storage, error) {
	if backend == "sqlite" {
		return sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
	}
	return jsonlnode.OpenNodeJsonlStorage(benchmarkContext, path, jsonl.JsonlStorageOptions{})
}

func persistentPath(directory, backend string) string {
	if backend == "sqlite" {
		return filepath.Join(directory, "storage.sqlite")
	}
	return filepath.Join(directory, "storage")
}

func createFixture(tb testing.TB, backend string) durable.Storage {
	tb.Helper()
	var created durable.Storage
	var err error
	if backend == "memory" {
		created = storage.NewMemoryStorage()
	} else {
		created, err = openPersistentStorage(backend, persistentPath(tb.TempDir(), backend))
		if err != nil {
			tb.Fatal(err)
		}
	}
	tb.Cleanup(func() { _ = created.Close(benchmarkContext) })
	return created
}

func createReadFixture(tb testing.TB, backend string) (durable.Storage, durabletest.StorageBenchmarkDataset) {
	tb.Helper()
	fixture := createFixture(tb, backend)
	dataset, err := durabletest.SeedStorageBenchmark(benchmarkContext, fixture, durabletest.TIMING_SCALE)
	if err != nil {
		tb.Fatal(err)
	}
	for _, scenario := range durabletest.STORAGE_READ_BENCHMARKS {
		got, err := scenario.Run(benchmarkContext, fixture, dataset)
		if err != nil {
			tb.Fatal(err)
		}
		if want := scenario.Expected(dataset); got != want {
			tb.Fatalf("%s on %s = %d, want %d", scenario.Name, backend, got, want)
		}
	}
	return fixture, dataset
}

func createWriteFixture(tb testing.TB, backend string) durable.Storage {
	tb.Helper()
	fixture := createFixture(tb, backend)
	if err := durabletest.SeedStorageWriteBenchmark(benchmarkContext, fixture); err != nil {
		tb.Fatal(err)
	}
	return fixture
}

func BenchmarkStorageRead(b *testing.B) {
	type readFixture struct {
		storage durable.Storage
		dataset durabletest.StorageBenchmarkDataset
	}
	fixtures := map[string]readFixture{}
	for _, backend := range storageBenchmarkBackends {
		fixture, dataset := createReadFixture(b, backend)
		fixtures[backend] = readFixture{storage: fixture, dataset: dataset}
	}
	for _, scenario := range durabletest.STORAGE_READ_BENCHMARKS {
		b.Run(scenario.Name, func(b *testing.B) {
			for _, backend := range storageBenchmarkBackends {
				fixture := fixtures[backend]
				b.Run(backend, func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := scenario.Run(benchmarkContext, fixture.storage, fixture.dataset); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkStorageWrite measures each write against a freshly seeded fixture, as upstream draws one from a pool per
// sample; fixture creation is excluded from the measurement.
func BenchmarkStorageWrite(b *testing.B) {
	for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
		b.Run(scenario.Name, func(b *testing.B) {
			for _, backend := range storageBenchmarkBackends {
				b.Run(backend, func(b *testing.B) {
					validation := createWriteFixture(b, backend)
					if got, err := scenario.Run(benchmarkContext, validation); err != nil || got != scenario.Expected {
						b.Fatalf("%s on %s = %d, %v; want %d", scenario.Name, backend, got, err, scenario.Expected)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						b.StopTimer()
						fixture := createWriteFixture(b, backend)
						b.StartTimer()
						if _, err := scenario.Run(benchmarkContext, fixture); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

func copyPersistentStorage(tb testing.TB, backend, source, destination string) {
	tb.Helper()
	if backend == "sqlite" {
		content, err := os.ReadFile(source)
		if err == nil {
			err = os.WriteFile(destination, content, 0o600)
		}
		if err != nil {
			tb.Fatal(err)
		}
		return
	}
	if err := os.CopyFS(destination, os.DirFS(source)); err != nil {
		tb.Fatal(err)
	}
}

func reopenAndRead(backend, path string, firstEntryId durable.EntryId) (int64, durable.Storage, error) {
	reopened, err := openPersistentStorage(backend, path)
	if err != nil {
		return -1, nil, err
	}
	found, err := reopened.Entry(benchmarkContext, firstEntryId)
	if err != nil || found == nil {
		return -1, reopened, err
	}
	return int64(found.Entry.Id), reopened, nil
}

func BenchmarkStorageReopenAndFirstExactRead(b *testing.B) {
	for _, backend := range storageBenchmarkBackends {
		if backend == "memory" {
			continue
		}
		b.Run(backend, func(b *testing.B) {
			seedPath := persistentPath(b.TempDir(), backend)
			seed, err := openPersistentStorage(backend, seedPath)
			if err != nil {
				b.Fatal(err)
			}
			dataset, err := durabletest.SeedStorageBenchmark(benchmarkContext, seed, durabletest.TIMING_SCALE)
			if err == nil {
				err = seed.Close(benchmarkContext)
			}
			if err != nil {
				b.Fatal(err)
			}
			id, validation, err := reopenAndRead(backend, seedPath, dataset.FirstEntryId)
			if err != nil || id != int64(dataset.FirstEntryId) {
				b.Fatalf("reopen read = %d, %v; want %d", id, err, dataset.FirstEntryId)
			}
			_ = validation.Close(benchmarkContext)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				b.StopTimer()
				path := persistentPath(b.TempDir(), backend)
				copyPersistentStorage(b, backend, seedPath, path)
				b.StartTimer()
				_, reopened, err := reopenAndRead(backend, path, dataset.FirstEntryId)
				if err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				_ = reopened.Close(benchmarkContext)
				b.StartTimer()
			}
		})
	}
}

// TestStorageBenchmarkExpectations runs the validation that upstream performs before measuring, so ordinary test runs
// prove every benchmark measures the intended result on every backend.
func TestStorageBenchmarkExpectations(t *testing.T) {
	for _, backend := range storageBenchmarkBackends {
		t.Run(backend, func(t *testing.T) {
			createReadFixture(t, backend)
			for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
				if got, err := scenario.Run(benchmarkContext, createWriteFixture(t, backend)); err != nil || got != scenario.Expected {
					t.Fatalf("%s = %d, %v; want %d", scenario.Name, got, err, scenario.Expected)
				}
			}
		})
	}
}
