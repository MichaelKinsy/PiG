package tools

import (
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestFileMutationQueue_FIFOAdmissionOrder proves that FileMutationQueue.With
// serializes same-key mutations in the order callers were admitted, matching
// upstream core/tools/file-mutation-queue.ts::withFileMutationQueue's
// per-file Promise chain (each new call is chained onto whatever the map
// currently holds for that key, atomically, at registration time). The prior
// implementation instead handed out an independent *sync.Mutex per key: a
// goroutine released the bookkeeping map lock and only then raced,
// unsynchronized, to acquire that per-key mutex, so a goroutine whose
// registration landed later could still win that separate race and run its
// callback before an earlier-registered caller (a batched write-then-edit on
// one file could apply out of order; parity/scenarios/tools/
// 10-print-batched-file-mutation.toml).
//
// A pre-call atomic ticket (assigned just before invoking With) cannot
// reliably prove call order here: on a real multi-core machine, the gap
// between taking the ticket and the goroutine actually reaching With's
// admission section is itself an unsynchronized race, so it produces
// apparent "inversions" against ticket order for a correct implementation
// too. Instead this test blocks the key with a long-lived holder, then
// admits waiters one at a time, confirming (via q.chains, no sleeps) that
// each waiter's registration has actually landed before starting the next.
// That is a real, observed happens-before, so any admission-order violation
// it detects is a genuine bug, not scheduler noise.
func TestFileMutationQueue_FIFOAdmissionOrder(t *testing.T) {
	q := NewFileMutationQueue()
	const rawPath = "fifo-admission-order-path"
	key, err := canonicalKey(rawPath)
	if err != nil {
		t.Fatalf("canonicalKey: %v", err)
	}

	holderStarted := make(chan struct{})
	releaseHolder := make(chan struct{})

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = q.With(rawPath, func() error {
			close(holderStarted)
			<-releaseHolder
			return nil
		})
	})
	<-holderStarted

	q.mu.Lock()
	prevSlot := q.chains[key]
	q.mu.Unlock()

	const n = 64
	var mu sync.Mutex
	var order []int

	wg.Add(n)
	for i := range n {
		idx := i
		go func() {
			defer wg.Done()
			_ = q.With(rawPath, func() error {
				mu.Lock()
				order = append(order, idx)
				mu.Unlock()
				return nil
			})
		}()

		// Wait for goroutine idx's registration to actually land in the
		// chain before launching goroutine idx+1: an observed state
		// transition, not a timing guess.
		for spins := 0; ; spins++ {
			q.mu.Lock()
			cur := q.chains[key]
			q.mu.Unlock()
			if cur != prevSlot {
				prevSlot = cur
				break
			}
			if spins > 50_000_000 {
				t.Fatalf("goroutine %d registration never observed", idx)
			}
			runtime.Gosched()
		}
	}

	close(releaseHolder)
	wg.Wait()

	for i, v := range order {
		if v != i {
			t.Fatalf("FIFO admission order violated: expected %d at position %d, got order=%v", i, i, order)
		}
	}
}

// TestFileMutationQueue_CleanupOnDrain proves that once every queued
// mutation for a path has completed, the queue drops its bookkeeping entry
// for that path instead of retaining it forever. Upstream deletes the map
// entry in withFileMutationQueue's finally block, checking the entry is
// still its own chained Promise before deleting
// (file-mutation-queue.ts:57-59). The prior Go implementation never removed
// entries from its locks map, leaking one *sync.Mutex per distinct path for
// the process lifetime.
func TestFileMutationQueue_CleanupOnDrain(t *testing.T) {
	q := NewFileMutationQueue()
	path := "/fifo/contention/cleanup-file"

	const n = 8
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_ = q.With(path, func() error { return nil })
		}()
	}
	wg.Wait()

	q.mu.Lock()
	got := len(q.chains)
	q.mu.Unlock()
	if got != 0 {
		t.Fatalf("expected no pending queue entries after drain, got %d", got)
	}
}

// TestFileMutationQueue_PropagatesNonMissingPathError proves that
// canonicalKey (and therefore With/Reserve) only swallows a missing-path
// error (ENOENT/ENOTDIR), matching upstream's isMissingPathError, and
// propagates every other EvalSymlinks failure instead of silently falling
// back to the unresolved path. A symlink cycle (a -> b -> a) makes
// EvalSymlinks fail with ELOOP, a real, portable, root-independent case
// upstream does not treat as "missing."
func TestFileMutationQueue_PropagatesNonMissingPathError(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	testenv.Symlink(t, b, a)
	testenv.Symlink(t, a, b)

	q := NewFileMutationQueue()
	called := false
	err := q.With(a, func() error { called = true; return nil })
	if err == nil {
		t.Fatal("expected an error for a symlink cycle, got nil")
	}
	if called {
		t.Fatal("fn ran despite an unresolved key error")
	}
	if isMissingPathError(err) {
		t.Fatalf("symlink cycle misclassified as a missing path: %v", err)
	}
}
