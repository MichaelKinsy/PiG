package storage_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
)

// BenchmarkStorageWriteWarm runs each write scenario repeatedly on one seeded fixture, so the statement cache, the page
// cache and the allocator are warm. BenchmarkStorageWrite draws a fresh fixture per sample and measures cold prepares.
func BenchmarkStorageWriteWarm(b *testing.B) {
	for _, scenario := range durabletest.STORAGE_WRITE_BENCHMARKS {
		b.Run(scenario.Name, func(b *testing.B) {
			for _, backend := range storageBenchmarkBackends {
				b.Run(backend, func(b *testing.B) {
					fixture := createWriteFixture(b, backend)
					for range 200 {
						if _, err := scenario.Run(benchmarkContext, fixture); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if _, err := scenario.Run(benchmarkContext, fixture); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
