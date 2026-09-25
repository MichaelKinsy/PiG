package session_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/agent/harness/session/testing/benchmark"
	"github.com/MichaelKinsy/PiG/agent/harness/session/testing/conformance"
)

func seededMemoryStorage(tb testing.TB, dataset benchmark.StorageBenchmarkDataset) *session.MemoryStorage {
	tb.Helper()
	storage := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: fixedNow})
	if err := benchmark.SeedStorageBenchmark(background, storage, dataset); err != nil {
		tb.Fatal(err)
	}
	return storage
}

func newBenchmarkRepo() (*session.MemorySessionRepo, conformance.RepoAdapter) {
	repo := session.NewMemorySessionRepo(&session.MemorySessionRepoOptions{Now: fixedNow})
	return repo, memoryRepoAdapter(repo)
}

// The smallest datasets run the shared scenarios once and check their results,
// so the benchmark workloads stay exercised by ordinary go test runs.
func TestMemoryBenchmarkScenariosProduceTheirExpectedResults(t *testing.T) {
	dataset := benchmark.StorageBenchmarkDatasets[0]
	storage := seededMemoryStorage(t, dataset)
	for _, scenario := range benchmark.StorageReadBenchmarkScenarios {
		if got, err := scenario.Run(background, storage, dataset); err != nil || got != scenario.ExpectedResult(dataset) {
			t.Fatalf("%s = %d, %v", scenario.Name, got, err)
		}
	}
	mustNoErr(t, storage.Close(background))
	for _, scenario := range benchmark.StorageWriteBenchmarkScenarios {
		fresh := session.NewMemoryStorage(nil)
		if scenario.Prepare != nil {
			mustNoErr(t, scenario.Prepare(background, fresh))
		}
		if got, err := scenario.Run(background, fresh); err != nil || got != scenario.WriteCount {
			t.Fatalf("%s = %d, %v", scenario.Name, got, err)
		}
	}
	catalog := benchmark.SessionRepoCatalogBenchmarkDatasets[0]
	repo, adapter := newBenchmarkRepo()
	if _, err := benchmark.SeedSessionRepoCatalogBenchmark(background, adapter, catalog); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range benchmark.SessionRepoCatalogReadBenchmarkScenarios {
		if got, err := scenario.Run(background, adapter); err != nil || got != scenario.ExpectedResult(catalog) {
			t.Fatalf("%s = %d, %v", scenario.Name, got, err)
		}
	}
	mustNoErr(t, repo.Close(background))
	for _, scenario := range benchmark.SessionRepoCatalogWriteBenchmarkScenarios {
		repo, adapter := newBenchmarkRepo()
		run, err := scenario.Prepare(background, adapter)
		mustNoErr(t, err)
		if got, err := run(); err != nil || got != scenario.ExpectedResult {
			t.Fatalf("%s = %d, %v", scenario.Name, got, err)
		}
		mustNoErr(t, repo.Close(background))
	}
	forkDataset := benchmark.SessionRepoForkBenchmarkDatasets[0]
	for _, scenario := range benchmark.SessionRepoForkWriteBenchmarkScenarios {
		repo, adapter := newBenchmarkRepo()
		source, err := benchmark.SeedSessionRepoForkBenchmark(background, adapter, forkDataset)
		mustNoErr(t, err)
		if got, err := scenario.Run(background, adapter, source, forkDataset); err != nil || got != scenario.ExpectedResult(forkDataset) {
			t.Fatalf("%s = %d, %v", scenario.Name, got, err)
		}
		mustNoErr(t, repo.Close(background))
	}
}

func BenchmarkMemoryStorageReads(b *testing.B) {
	for _, dataset := range benchmark.StorageBenchmarkDatasets {
		storage := seededMemoryStorage(b, dataset)
		for _, scenario := range benchmark.StorageReadBenchmarkScenarios {
			b.Run(dataset.Name+"/"+scenario.Name, func(b *testing.B) {
				for b.Loop() {
					if _, err := scenario.Run(background, storage, dataset); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
		mustNoErr(b, storage.Close(background))
	}
}

func BenchmarkMemoryStorageWrites(b *testing.B) {
	for _, scenario := range benchmark.StorageWriteBenchmarkScenarios {
		b.Run(scenario.Name, func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				storage := session.NewMemoryStorage(nil)
				if scenario.Prepare != nil {
					mustNoErr(b, scenario.Prepare(background, storage))
				}
				b.StartTimer()
				if _, err := scenario.Run(background, storage); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkMemorySessionRepoForks(b *testing.B) {
	for _, dataset := range benchmark.SessionRepoForkBenchmarkDatasets {
		for _, scenario := range benchmark.SessionRepoForkWriteBenchmarkScenarios {
			b.Run(dataset.Name+"/"+scenario.Name, func(b *testing.B) {
				for b.Loop() {
					b.StopTimer()
					_, adapter := newBenchmarkRepo()
					source, err := benchmark.SeedSessionRepoForkBenchmark(background, adapter, dataset)
					mustNoErr(b, err)
					b.StartTimer()
					if _, err := scenario.Run(background, adapter, source, dataset); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkMemorySessionRepoCatalog(b *testing.B) {
	for _, dataset := range benchmark.SessionRepoCatalogBenchmarkDatasets[:2] {
		_, adapter := newBenchmarkRepo()
		if _, err := benchmark.SeedSessionRepoCatalogBenchmark(background, adapter, dataset); err != nil {
			b.Fatal(err)
		}
		for _, scenario := range benchmark.SessionRepoCatalogReadBenchmarkScenarios {
			b.Run(dataset.Name+"/"+scenario.Name, func(b *testing.B) {
				for b.Loop() {
					if _, err := scenario.Run(background, adapter); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
