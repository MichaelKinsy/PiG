package sdk

import (
	"errors"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"syscall"
)

// fileMutationQueues serializes the mutations of one file inside this extension process, as Pi's module-level queue does (core/tools/file-mutation-queue.ts). Each
// entry is a chain: its value closes when the last registered mutation of that file finishes.
var fileMutationQueues = struct {
	mu     sync.Mutex
	chains map[string]chan struct{}
}{chains: map[string]chan struct{}{}}

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
	previous := queues.chains[key]
	done := make(chan struct{})
	queues.chains[key] = done
	queues.mu.Unlock()

	if previous != nil {
		<-previous
	}
	defer func() {
		queues.mu.Lock()
		if queues.chains[key] == done {
			delete(queues.chains, key)
		}
		queues.mu.Unlock()
		close(done)
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
