package sdk

import (
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/extensions/sdk/internal/jsloop"
)

// fileMutationQueues serializes the mutations of one file inside this extension process, as Pi's module-level queue does (core/tools/file-mutation-queue.ts). Each
// file keeps the place of the last mutation registered for it and the number of mutations admitted and not finished; the entry is dropped when the count reaches zero.
// The entry is a value in the map, so a call that finds no queue makes no allocation but its place.
var fileMutationQueues = struct {
	mu     sync.Mutex
	queues map[string]mutationQueue
}{queues: map[string]mutationQueue{}}

type mutationQueue struct {
	tail     *jsloop.Place
	admitted int
}

// WithFileMutationQueue runs fn once every mutation registered earlier for the same file has finished, then lets the next one run, and returns fn's result and error.
// Mutations of different files run in parallel. It mirrors Pi's withFileMutationQueue, which edit and write extension tools wrap around their read-modify-write so
// parallel tool calls on one file cannot interleave: the file is keyed by its real path (a symlink and its target share a queue; a path that does not exist yet is
// keyed by its absolute path), and callers run in the order they called. A path that cannot be resolved for a reason other than a missing file or directory
// returns that error without running fn.
//
// The queue serializes mutations made through it inside this process; it does not coordinate with the host's built-in tools or other extension processes.
func WithFileMutationQueue[T any](filePath string, fn func() (T, error)) (T, error) {
	queues := &fileMutationQueues
	// The key is resolved and the position taken under one lock: admission order equals call order, which Pi gets from its registration queue.
	queues.mu.Lock()
	key, err := fileMutationQueueKey(filePath)
	if err != nil {
		queues.mu.Unlock()
		var zero T
		return zero, err
	}
	queue := queues.queues[key]
	place := jsloop.After(queue.tail)
	queue.tail = place
	queue.admitted++
	queues.queues[key] = queue
	queues.mu.Unlock()

	place.Wait()
	defer func() {
		place.Release()
		queues.mu.Lock()
		queue := queues.queues[key]
		if queue.admitted--; queue.admitted == 0 {
			delete(queues.queues, key)
		} else {
			queues.queues[key] = queue
		}
		queues.mu.Unlock()
	}()
	return fn()
}

func fileMutationQueueKey(filePath string) (string, error) {
	resolved, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if isMissingPathError(err) {
			return resolved, nil
		}
		return "", err
	}
	return real, nil
}

// windowsMissingPathErrors are the Windows system errors that libuv's uv_translate_sys_error names ENOENT, so Node's realpath reports each as a missing path.
var windowsMissingPathErrors = []syscall.Errno{
	2,     // ERROR_FILE_NOT_FOUND
	3,     // ERROR_PATH_NOT_FOUND
	15,    // ERROR_INVALID_DRIVE
	123,   // ERROR_INVALID_NAME
	126,   // ERROR_MOD_NOT_FOUND
	161,   // ERROR_BAD_PATHNAME
	203,   // ERROR_ENVVAR_NOT_FOUND
	267,   // ERROR_DIRECTORY
	4392,  // ERROR_INVALID_REPARSE_DATA
	11001, // WSAHOST_NOT_FOUND
	11004, // WSANO_DATA
}

// isMissingPathError is Pi's isMissingPathError: the error's Node code is ENOENT or ENOTDIR. On Windows that is decided by the system error, not by fs.ErrNotExist,
// which also covers ERROR_BAD_NETPATH (libuv: UNKNOWN).
func isMissingPathError(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return errors.Is(err, fs.ErrNotExist)
	}
	if errno == syscall.ENOENT || errno == syscall.ENOTDIR {
		return true
	}
	return runtime.GOOS == "windows" && slices.Contains(windowsMissingPathErrors, errno)
}
