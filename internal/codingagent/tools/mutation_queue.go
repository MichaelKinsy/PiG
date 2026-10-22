package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// FileMutationQueue serialises concurrent mutations of one file, in the order
// callers entered With: mutations whose canonical paths are equal, and
// mutations of an existing file reached by different canonical paths (hard
// links, and case aliases that Go's symlink resolution leaves apart). That
// second rule goes beyond Pi (D190). Operations on different files proceed in
// parallel. It coordinates only mutations registered in this process.
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
	files  map[string]os.FileInfo
}

// NewFileMutationQueue returns a ready-to-use queue.
func NewFileMutationQueue() *FileMutationQueue {
	return &FileMutationQueue{chains: make(map[string]chan struct{}), files: make(map[string]os.FileInfo)}
}

// With runs fn once every mutation registered earlier for the same canonical
// path, or for the same existing file under another canonical path, has
// completed, then hands the path to the next queued caller and returns fn's
// error. filePath is resolved to its canonical form before keying (matching
// upstream realpath behaviour: symlinks to the same file share one queue
// slot). If q is nil (e.g. in tests that construct tools directly), fn is
// called without serialisation: safe for single-threaded test contexts.
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

// WithFileMutationQueue is upstream's withFileMutationQueue (file-mutation-queue.ts:32): it runs fn once every mutation registered earlier for the same file has completed (the same canonical path, or the same existing file under another path, D190), through the process-wide queue that every edit and write tool made by the Create functions shares, and returns fn's result. Mutations of different files run in parallel. An error from resolving the path's canonical form is returned without running fn, as upstream's rejection is.
func WithFileMutationQueue[T any](filePath string, fn func() (T, error)) (T, error) {
	var result T
	err := processFileMutationQueue.With(filePath, func() error {
		var err error
		result, err = fn()
		return err
	})
	return result, err
}

// ReserveFileMutation registers filePath's next position in the process-wide queue that WithFileMutationQueue and every edit and write tool share, synchronously, and returns the ticket: Wait (or WaitContext) for the turn, then Release exactly once after the mutation completes. The extension host reserves through it for the mutations an extension process runs under its own withFileMutationQueue, so they queue with the built-in tools and with each other (plumbing for Pi's single process, D19: each SDK runs in a process of its own).
func ReserveFileMutation(filePath string) (*MutationTicket, error) {
	return processFileMutationQueue.Reserve(filePath)
}

// MutationTicket is a reserved position in a FileMutationQueue, obtained via
// Reserve. It lets a caller fix its place in call order before starting any
// concurrent work, rather than only being able to register and wait in one
// inseparable step (With).
type MutationTicket struct {
	q    *FileMutationQueue
	key  string
	prev []<-chan struct{}
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
	//perf:ok once per file mutation call, which reads and writes the file; the identity of the file is what D190 orders by
	info, err := os.Stat(key)
	if err != nil && !isMissingPathError(err) {
		q.mu.Unlock()
		return nil, err
	}
	var prev []<-chan struct{}
	for path, pending := range q.chains {
		observed := q.files[path]
		if observed == nil && info != nil {
			// A queued write may have created a file that was missing at registration.
			//perf:ok only for a queued call whose file was missing when it queued, and only while a call for an existing file is queued behind it
			observed, _ = os.Stat(path)
		}
		// pig divergence (D190): a mutation also waits for earlier mutations of the same existing file reached by another canonical path (a hard link, or a case alias on a case-insensitive filesystem); Pi keys only by realpath, so it runs them at once and one overwrites the other.
		if path == key || (info != nil && observed != nil && os.SameFile(info, observed)) {
			prev = append(prev, pending)
		}
	}
	next := make(chan struct{})
	q.chains[key] = next
	q.files[key] = info
	q.mu.Unlock()
	return &MutationTicket{q: q, key: key, prev: prev, next: next}, nil
}

// Wait blocks until earlier mutations of this ticket's path or file identity complete.
func (t *MutationTicket) Wait() {
	for _, prev := range t.prev {
		<-prev
	}
}

// WaitContext is Wait that gives up when ctx ends: it returns ctx's error and the ticket is the queue's from then on, so the caller must not Release it. A ticket that
// gave up keeps its place until every earlier mutation of the path has completed and only then passes it on; a successor never runs ahead of a predecessor that is
// still inside the queue. A ticket with no earlier mutation is admitted whatever ctx says.
func (t *MutationTicket) WaitContext(ctx context.Context) error {
	for i, prev := range t.prev {
		select {
		case <-prev:
		case <-ctx.Done():
			t.handOff(t.prev[i:])
			return ctx.Err()
		}
	}
	return nil
}

// handOff releases t once every mutation in rest has completed, for a ticket whose WaitContext gave up. The goroutine runs no caller
// code: it only receives from channels the queue closes and closes t's own, so there is no caller panic for it to contain, and a
// recover here would hide a double Release instead of surfacing it.
func (t *MutationTicket) handOff(rest []<-chan struct{}) {
	go func() {
		for _, prev := range rest {
			<-prev
		}
		t.Release()
	}()
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
		delete(t.q.files, t.key)
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
	abs, err := nodepath.Resolve(p)
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
	// filepath.EvalSymlinks reports a symlink loop with a plain error where realpath rejects with ELOOP.
	if err.Error() == "EvalSymlinks: too many links" { //portlint:allow erroridentity path/filepath returns an unexported errors.New with this text and no sentinel (symlink.go)
		return "", &fs.PathError{Op: "realpath", Path: abs, Err: syscall.ELOOP}
	}
	return "", err
}

// isMissingPathError mirrors upstream's isMissingPathError: only the Node
// codes ENOENT and ENOTDIR are treated as "the path does not exist yet";
// every other error (EACCES, EPERM, ELOOP, and so on) is a real failure the
// caller must see. On Windows every system error libuv names ENOENT
// (ERROR_INVALID_NAME, ERROR_DIRECTORY, and so on) counts as missing.
func isMissingPathError(err error) bool {
	switch nodeErrorCode(err) {
	case "ENOENT", "ENOTDIR":
		return true
	}
	return false
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
