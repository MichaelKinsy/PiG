package storage_test

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// Pi packages/durable/src/testing/storage-benchmark.ts: STORAGE_READ_BENCHMARKS (lines 278-380: run(storage, dataset) returns the number expected(dataset) names;
// names 286-374 with REPLAY_TAILS = [0, 16, 128, 1024], line 36) and STORAGE_WRITE_BENCHMARKS (lines 384-470: each write returns its expected count on a freshly seeded
// storage). Every scenario returns exactly what it expects on each backend, so a benchmark measures the operation it names and nothing else; and the scenario set is
// Pi's, in Pi's order.
// mutation-checked: a scenario that returns a wrong value (or an altered Expected) fails the value check; a dropped or renamed scenario fails the names check.
func TestStorageBenchmarkScenariosReturnWhatTheyExpectOnEveryBackend(t *testing.T) {
	wantRead := []string{
		"exact entry lookup", "entry page scan (100)", "filtered task scan (50)", "exact document address among many",
		"document replay tail (0)", "document replay tail (16)", "document replay tail (128)", "document replay tail (1024)",
		"ancient historical read before newer base", "recent historical read after newer base", "fork-depth history scan (100)", "fork-depth head lookup",
	}
	var gotRead []string
	for _, scenario := range durabletest.STORAGE_READ_BENCHMARKS {
		gotRead = append(gotRead, scenario.Name)
	}
	if !slices.Equal(gotRead, wantRead) {
		t.Fatalf("read benchmarks = %q, want %q", gotRead, wantRead)
	}
	wantWrite := []string{"commit one entry", "commit 100 entries", "commit mixed entry/task/submission/document"}
	var gotWrite []string
	for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
		gotWrite = append(gotWrite, scenario.Name)
	}
	if !slices.Equal(gotWrite, wantWrite) {
		t.Fatalf("write benchmarks = %q, want %q", gotWrite, wantWrite)
	}

	for _, backend := range storageBenchmarkBackends {
		t.Run(backend+"/read", func(t *testing.T) {
			fixture := createFixture(t, backend)
			dataset, err := durabletest.SeedStorageBenchmark(benchmarkContext, fixture, durabletest.STORAGE_MEMORY_SCALES[0])
			if err != nil {
				t.Fatal(err)
			}
			for _, scenario := range durabletest.STORAGE_READ_BENCHMARKS {
				got, err := scenario.Run(benchmarkContext, fixture, dataset)
				if err != nil {
					t.Fatalf("%s: %v", scenario.Name, err)
				}
				if want := scenario.Expected(dataset); got != want {
					t.Errorf("%s = %d, want %d", scenario.Name, got, want)
				}
			}
		})
		t.Run(backend+"/write", func(t *testing.T) {
			for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
				fixture := createWriteFixture(t, backend)
				got, err := scenario.Run(benchmarkContext, fixture)
				if err != nil {
					t.Fatalf("%s: %v", scenario.Name, err)
				}
				if got != scenario.Expected {
					t.Errorf("%s = %d, want %d", scenario.Name, got, scenario.Expected)
				}
			}
		})
	}
}
