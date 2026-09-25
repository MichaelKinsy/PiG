package tools

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/agent"
)

// FileMutationQueue serialises concurrent mutations targeting the same
// canonical file path, in the order callers entered With. Operations on
// different files proceed in parallel.
//
// Mirrors upstream core/tools/file-mutation-queue.ts::withFileMutationQueue,
// which chains a per-file Promise so mutations for one file apply in the
// order withFileMutationQueue was called, and drops the map entry once the
// chain drains. Upstream additionally serialises the async key-resolution
// step itself through a package-level registrationQueue so that admission
// order matches call order even though realpath is async; pig's key
// resolution (canonicalKey) is synchronous, so admission and key resolution
// happen inside the same lock/unlock section below, giving the same
// call-order guarantee without a separate queue.
//
// A single per-key sync.Mutex is not equivalent: two goroutines that both
// see the mutex missing (or unlocked) race, unsynchronised, to acquire it,
// so a goroutine that entered With later can still win and run its callback
// before an earlier caller. The chain below removes that race by deciding
// each caller's position atomically, under q.mu, and having callers wait on
// a channel handed to them at that moment rather than a second Lock() race.
type FileMutationQueue struct {
	mu     sync.Mutex
	chains map[string]chan struct{} // path -> signal closed when its holder's fn returns
}

// NewFileMutationQueue returns a ready-to-use queue.
func NewFileMutationQueue() *FileMutationQueue {
	return &FileMutationQueue{chains: make(map[string]chan struct{})}
}

// With runs fn once every mutation registered earlier for the same canonical
// path has completed, then hands the path to the next queued caller and
// returns fn's error. filePath is resolved to its canonical form before
// keying (matching upstream realpath behaviour: symlinks to the same file
// share one queue slot). If q is nil (e.g. in tests that construct tools
// directly), fn is called without serialisation: safe for single-threaded
// test contexts.
func (q *FileMutationQueue) With(filePath string, fn func() error) error {
	if q == nil {
		return fn()
	}
	ticket, err := q.Reserve(filePath)
	if err != nil {
		return err
	}
	ticket.Wait()
	defer ticket.Release()
	return fn()
}

// MutationTicket is a reserved position in a FileMutationQueue, obtained via
// Reserve. It lets a caller fix its place in call order before starting any
// concurrent work, rather than only being able to register and wait in one
// inseparable step (With).
type MutationTicket struct {
	q    *FileMutationQueue
	key  string
	prev <-chan struct{}
	next chan struct{}
}

// Reserve registers filePath's next queue position, synchronously, without
// waiting for the previous holder. Call Wait for this ticket's turn, then
// Release exactly once, after the mutation completes. Reserve lets a caller
// (for example the parallel tool dispatcher, via QueueOrderable) establish
// admission order itself, in its own call-ordered loop, decoupled from when
// the reserving goroutine actually runs its mutation.
func (q *FileMutationQueue) Reserve(filePath string) (*MutationTicket, error) {
	q.mu.Lock()
	key, err := canonicalKey(filePath)
	if err != nil {
		q.mu.Unlock()
		return nil, err
	}
	prev := q.chains[key]
	next := make(chan struct{})
	q.chains[key] = next
	q.mu.Unlock()
	return &MutationTicket{q: q, key: key, prev: prev, next: next}, nil
}

// Wait blocks until every earlier-registered mutation for this ticket's
// canonical path has completed.
func (t *MutationTicket) Wait() {
	if t.prev != nil {
		<-t.prev
	}
}

// Release signals this ticket's completion to the next queued caller, and
// drops the queue's bookkeeping for this path if no later caller has
// registered behind it. Callers must call Release exactly once, regardless
// of whether the mutation succeeded.
func (t *MutationTicket) Release() {
	close(t.next)
	t.q.mu.Lock()
	if t.q.chains[t.key] == t.next {
		delete(t.q.chains, t.key)
	}
	t.q.mu.Unlock()
}

// canonicalKey returns an absolute, symlink-resolved path for use as a map
// key. Mirrors upstream getMutationQueueKey: a missing path (ENOENT) or a
// non-directory path component (ENOTDIR) falls back to the resolved,
// non-canonical path, matching upstream's isMissingPathError check, since a
// file that does not exist yet (a fresh write target) has no realpath to
// resolve. Every other EvalSymlinks error (permission denied, too many
// levels of symlinks, and so on) propagates, matching upstream's rethrow.
func canonicalKey(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return real, nil
	}
	if isMissingPathError(err) {
		return abs, nil
	}
	return "", err
}

// isMissingPathError mirrors upstream's isMissingPathError: only ENOENT and
// ENOTDIR are treated as "the path does not exist yet"; every other error
// (EACCES, ELOOP, and so on) is a real failure the caller must see.
func isMissingPathError(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// runQueued runs fn serialised against path through q, honoring a queue
// position the parallel tool dispatcher already reserved for this call
// (agent.MutationTicketFromContext) instead of registering a second time.
// Tools that implement agent.QueueOrderable to reserve their place in
// dispatch order (see mutation_reservation.go) must call this instead of
// q.With directly, or their reservation is never waited on or released.
func runQueued(ctx context.Context, q *FileMutationQueue, path string, fn func() error) error {
	if ticket, ok := agent.MutationTicketFromContext(ctx); ok {
		ticket.Wait()
		defer ticket.Release()
		return fn()
	}
	return q.With(path, fn)
}
