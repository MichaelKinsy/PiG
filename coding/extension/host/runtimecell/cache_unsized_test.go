package runtimecell

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeCacheEntryFiles fills one cache entry with files files of 8 bytes each and returns the cache root.
func writeCacheEntryFiles(t *testing.T, files int) string {
	t.Helper()
	cacheRoot := t.TempDir()
	entry := filepath.Join(cacheRoot, "ext", "build", "target")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if err := os.WriteFile(filepath.Join(entry, fmt.Sprintf("f%d", i)), []byte("01234567"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cacheRoot
}

// An unsized collection classifies each entry from its metadata files and never reads the size of the files inside it, so its cost does not grow with what an entry holds. A sized one stats every file, and MaxSize needs the sizes even when Unsized is set.
func TestUnsizedCacheCollectionDoesNotReadEntrySizes(t *testing.T) {
	const few, many = 10, 1000
	cost := func(cacheRoot string, options CacheLifecycleOptions) float64 {
		options.CacheRoot, options.DryRun = cacheRoot, true
		return testing.AllocsPerRun(3, func() {
			if _, err := PruneCaches(options); err != nil {
				t.Fatal(err)
			}
		})
	}
	small, large := writeCacheEntryFiles(t, few), writeCacheEntryFiles(t, many)
	if grown := cost(large, CacheLifecycleOptions{Unsized: true}) - cost(small, CacheLifecycleOptions{Unsized: true}); grown > 20 {
		t.Errorf("an unsized collection allocated %.0f more for %d more cached files", grown, many-few)
	}
	if grown := cost(large, CacheLifecycleOptions{}) - cost(small, CacheLifecycleOptions{}); grown < many-few {
		t.Errorf("a sized collection allocated only %.0f more for %d more cached files; the measurement cannot see a size walk", grown, many-few)
	}

	report, err := PruneCaches(CacheLifecycleOptions{CacheRoot: large, DryRun: true, Unsized: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.TotalBytes != 0 || report.Entries[0].Bytes != 0 {
		t.Errorf("unsized report = %+v, want one entry and no byte counts", report)
	}
	limit := int64(1 << 30)
	report, err = PruneCaches(CacheLifecycleOptions{CacheRoot: large, DryRun: true, Unsized: true, MaxSize: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalBytes != 8*many {
		t.Errorf("MaxSize with Unsized: total = %d bytes, want %d", report.TotalBytes, 8*many)
	}
}
