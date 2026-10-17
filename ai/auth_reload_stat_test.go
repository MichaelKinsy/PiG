package ai

import (
	"context"
	"sync"
	"testing"
)

// watchRevisionStats wraps fileRevisionOf for the test: every stat of path calls inStat with the path, and the returned function reports the stat count.
func watchRevisionStats(t *testing.T, path string, inStat func()) func() int {
	t.Helper()
	stats := 0
	previous := fileRevisionOf
	fileRevisionOf = func(p string) (fileRevision, bool) {
		if p == path {
			stats++
			inStat()
		}
		return previous(p)
	}
	t.Cleanup(func() { fileRevisionOf = previous })
	return func() int { return stats }
}

// assertUnlocked fails the test when mu is held at the call. It runs on the reading goroutine itself, so a stat under the mutex is caught without a race.
func assertUnlocked(t *testing.T, mu *sync.Mutex, what string) {
	t.Helper()
	if !mu.TryLock() {
		t.Errorf("%s stat ran while its read-state mutex was held", what)
		return
	}
	mu.Unlock()
}

// Every credential read stats auth.json (Pi auth-storage.ts readLatestData). The stat is a syscall; under the read-state mutex it serialised every concurrent reader of the availability pass behind each other's stats.
func TestAuthStorageReadStatsOutsideTheReadLock(t *testing.T) {
	path := seedAuthFile(t, `{"anthropic":{"type":"api_key","key":"k"}}`)
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	stats := watchRevisionStats(t, path, func() { assertUnlocked(t, &store.read.mu, "auth.json") })
	for range 3 {
		credential, err := store.Read(ctx, "anthropic")
		if err != nil || credential == nil || credential.Key != "k" {
			t.Fatalf("Read = %+v, %v", credential, err)
		}
	}
	if _, err := store.List(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stats(); got != 4 {
		t.Fatalf("auth.json stats = %d, want one per read (4)", got)
	}
}

// BenchmarkAuthStorageReadHit reads an unchanged auth.json from every P at once, as the availability pass's provider goroutines do. Run with -cpu 1,8: ns/op must not grow with -cpu.
func BenchmarkAuthStorageReadHit(b *testing.B) {
	storage := newBenchAuthStorage(b)
	ctx := context.Background()
	if _, err := storage.Read(ctx, "anthropic"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := storage.Read(ctx, "anthropic"); err != nil {
				b.Error(err)
				return
			}
		}
	})
}
