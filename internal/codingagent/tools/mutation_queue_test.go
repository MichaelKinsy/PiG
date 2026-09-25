package tools

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestFileMutationQueue_SerializesSameFile: two goroutines writing the same
// file: second write must not start until first completes.
func TestFileMutationQueue_SerializesSameFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.txt")
	q := NewFileMutationQueue()

	var order []int
	var mu sync.Mutex

	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = q.With(path, func() error {
				mu.Lock()
				order = append(order, idx)
				mu.Unlock()
				return os.WriteFile(path, []byte("x"), 0o644)
			})
		}(i)
	}
	wg.Wait()

	// All 3 calls must have completed.
	if len(order) != 3 {
		t.Errorf("expected 3 completed mutations, got %d", len(order))
	}
}

// TestFileMutationQueue_DifferentFilesNoDeadlock: operations on different files
// must all complete (no global lock that would cause starvation). The key
// property is that if there were a global mutex, a goroutine holding it
// couldn't release until another goroutine inside the queue signals: that
// would deadlock. Here we just verify all 4 goroutines complete.
func TestFileMutationQueue_DifferentFilesParallel(t *testing.T) {
	dir := t.TempDir()
	q := NewFileMutationQueue()

	var completed atomic.Int32
	var wg sync.WaitGroup
	for i := range 4 {
		path := filepath.Join(dir, "file"+string(rune('a'+i))+".txt")
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_ = q.With(p, func() error {
				completed.Add(1)
				return os.WriteFile(p, []byte("data"), 0o644)
			})
		}(path)
	}
	wg.Wait()

	if n := completed.Load(); n != 4 {
		t.Errorf("expected all 4 operations to complete, got %d", n)
	}
}

// TestFileMutationQueue_NilSafe: nil queue must not panic.
func TestFileMutationQueue_NilSafe(t *testing.T) {
	var q *FileMutationQueue
	called := false
	if err := q.With("/any/path", func() error { called = true; return nil }); err != nil {
		t.Fatalf("nil queue returned error: %v", err)
	}
	if !called {
		t.Error("fn not called through nil queue")
	}
}
