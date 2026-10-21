package sdk

import (
	"path/filepath"
	"sync"
	"testing"
)

// BenchmarkWithFileMutationQueue is the queue's cost around an empty mutation: one caller alone on a file, and four callers contending for one file, as parallel edit calls do.
func BenchmarkWithFileMutationQueue(b *testing.B) {
	file := filepath.Join(b.TempDir(), "a.txt")
	nothing := func() (struct{}, error) { return struct{}{}, nil }
	b.Run("uncontended", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = WithFileMutationQueue(file, nothing)
		}
	})
	b.Run("four-callers", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() { _, _ = WithFileMutationQueue(file, nothing) })
			}
			wg.Wait()
		}
	})
}
