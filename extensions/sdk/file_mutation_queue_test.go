package sdk

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// queuedChain returns the chain link the queue holds for key, nil when none.
func queuedChain(key string) chan struct{} {
	fileMutationQueues.mu.Lock()
	defer fileMutationQueues.mu.Unlock()
	return fileMutationQueues.chains[key]
}

// awaitAdmission spins until the chain link for key is no longer previous, that is until a caller started earlier has taken its position, and returns the new link.
// The first caller holds the file until every caller is admitted, so no link is released while this waits.
func awaitAdmission(t *testing.T, key string, previous chan struct{}) chan struct{} {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if current := queuedChain(key); current != previous {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatal("a caller never took its queue position")
		}
		runtime.Gosched()
	}
}

func TestWithFileMutationQueueSerializesOneFileInCallOrder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	key, err := fileMutationQueueKey(file)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var running atomic.Int32
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	var link chan struct{}
	for id := range 4 {
		wg.Go(func() {
			_, _ = WithFileMutationQueue(file, func() (struct{}, error) {
				if running.Add(1) != 1 {
					t.Error("two mutations of one file overlapped")
				}
				if id == 0 {
					<-release
				}
				mu.Lock()
				order = append(order, id)
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				running.Add(-1)
				return struct{}{}, nil
			})
		})
		link = awaitAdmission(t, key, link)
	}
	close(release)
	wg.Wait()
	for i, id := range order {
		if id != i {
			t.Fatalf("callers ran in order %v, want call order", order)
		}
	}
	if len(order) != 4 {
		t.Fatalf("ran %v, want every caller", order)
	}
	fileMutationQueues.mu.Lock()
	defer fileMutationQueues.mu.Unlock()
	if len(fileMutationQueues.chains) != 0 {
		t.Fatalf("drained chains were kept: %v", fileMutationQueues.chains)
	}
}

// A failing mutation still hands the file to the caller queued behind it, and a symlink queues behind its target.
func TestWithFileMutationQueueReleasesAQueuedSymlinkCallerAfterAnError(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "a.txt"), filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	key, err := fileMutationQueueKey(target)
	if err != nil {
		t.Fatal(err)
	}
	if linkKey, err := fileMutationQueueKey(link); err != nil || linkKey != key {
		t.Fatalf("symlink key %q (%v) differs from its target's %q", linkKey, err, key)
	}
	release := make(chan struct{})
	var aliasRan atomic.Bool
	want := errors.New("boom")
	firstErr := make(chan error, 1)
	go func() {
		_, err := WithFileMutationQueue(target, func() (int, error) {
			<-release
			if aliasRan.Load() {
				t.Error("the symlink caller ran while its target was held")
			}
			return 0, want
		})
		firstErr <- err
	}()
	held := awaitAdmission(t, key, nil)
	type result struct {
		value int
		err   error
	}
	second := make(chan result, 1)
	go func() {
		value, err := WithFileMutationQueue(link, func() (int, error) {
			aliasRan.Store(true)
			return 7, nil
		})
		second <- result{value, err}
	}()
	awaitAdmission(t, key, held)
	close(release)
	if err := <-firstErr; !errors.Is(err, want) {
		t.Fatalf("error = %v, want the task's", err)
	}
	select {
	case got := <-second:
		if got.err != nil || got.value != 7 {
			t.Fatalf("queued caller returned %d, %v", got.value, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the failing mutation never released the caller queued behind it")
	}
}

// A path below a regular file (ENOTDIR) is keyed by its absolute path, as Pi's isMissingPathError treats ENOTDIR like ENOENT.
func TestWithFileMutationQueueKeysAPathBelowAFileByItsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	below := filepath.Join(file, "child")
	key, err := fileMutationQueueKey(below)
	if err != nil {
		t.Fatalf("a path below a file failed: %v", err)
	}
	if want, _ := filepath.Abs(below); key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

// isMissingPathError follows the Node error code: ENOENT and ENOTDIR are missing, and on Windows exactly the system errors libuv names ENOENT.
func TestIsMissingPathErrorFollowsTheNodeCode(t *testing.T) {
	windows := runtime.GOOS == "windows"
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"ENOENT", &fs.PathError{Op: "lstat", Path: "x", Err: syscall.ENOENT}, true},
		{"ENOTDIR", &fs.PathError{Op: "lstat", Path: "x", Err: syscall.ENOTDIR}, true},
		{"fs.ErrNotExist without an errno", fs.ErrNotExist, true},
		{"too many links", errors.New("EvalSymlinks: too many links"), false},
		{"ERROR_INVALID_NAME", &fs.PathError{Op: "lstat", Path: "x", Err: syscall.Errno(123)}, windows},
		{"ERROR_DIRECTORY", &fs.PathError{Op: "lstat", Path: "x", Err: syscall.Errno(267)}, windows},
		{"ERROR_BAD_NETPATH", &fs.PathError{Op: "lstat", Path: "x", Err: syscall.Errno(53)}, false},
	} {
		if got := isMissingPathError(tc.err); got != tc.want {
			t.Errorf("%s: missing = %v, want %v", tc.name, got, tc.want)
		}
	}
}
