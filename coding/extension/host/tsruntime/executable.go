package tsruntime

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

// Executable is a slot executable that Verify checked.
//
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
//
// On Linux it holds the file that was hashed open, and ExecPath names that open file (/proc/self/fd/N) instead of the path it was
// opened by, so the process a launch starts runs the bytes that were verified even when the path is replaced, renamed over or
// unlinked after Verify returned: verify and exec are the same file. ExecPath also compares the file's identity with the one
// verified and refuses an executable that was written to in place since, so the remaining window is the few microseconds between
// that check and the kernel taking the file for exec, after which a write to it fails with ETXTBSY. The comparison ignores the
// ctime of a file that no directory entry names any more, because unlinking it (the swap this defends against) sets its ctime, and
// nobody can open an unlinked file for writing; a writer that opened the file before the check and writes after it, through that
// open file, is a same-user process, which is inside the trust boundary.
//
// Where it cannot hold the file (every other platform; a file that starts with "#!", whose interpreter would open the descriptor
// after exec closed it; Android, whose launcher starts programs through the system linker; a /proc that does not show the file),
// ExecPath is the path, and the file at that path at exec time runs. A writer that can replace the executable between the check
// and the exec, or the record files and runtime.json, is inside the trust boundary there, as it was before the descriptor was held.
type Executable struct {
	path     string
	identity fileIdentity
	identErr error

	mu     sync.Mutex
	file   *os.File
	fdPath string
}

// newExecutable wraps file, which was opened at path and described by info. It holds file for a launch only when the platform can
// run it; Verify drops it otherwise once the check is done.
func newExecutable(path string, file *os.File, info os.FileInfo) *Executable {
	e := &Executable{path: path, file: file}
	e.identity, e.identErr = identify(info)
	if e.identErr == nil {
		e.fdPath = descriptorPath(file, info)
	}
	return e
}

// settle closes the file of an executable that is started by path: nothing will use the descriptor.
func (e *Executable) settle() {
	if e.fdPath == "" {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.file != nil {
			_ = e.file.Close() // Read-only: close cannot lose data.
			e.file = nil
		}
	}
}

// Path is the path the executable was opened by.
func (e *Executable) Path() string { return e.path }

// ExecPath returns the path to start the executable with: the verified open file, or the path where the platform cannot start a
// descriptor. The caller keeps the argument vector's first element as Path. It fails when the executable was released, or was
// written to or replaced in place since it was verified.
func (e *Executable) ExecPath() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fdPath == "" {
		return e.path, nil
	}
	if e.file == nil {
		return "", fmt.Errorf("the verified handle of %s was released before it started", e.path)
	}
	info, err := e.file.Stat()
	if err != nil {
		return "", err
	}
	current, err := identify(info)
	if err != nil || !e.identity.unchangedBy(current, unlinked(info)) {
		return "", errors.New(e.path + " changed after it was verified")
	}
	return e.fdPath, nil
}

// Close releases the open file. A launch calls it once its process has started, which is when the kernel has taken the file. It is
// safe on a nil Executable and more than once.
func (e *Executable) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.file == nil {
		return nil
	}
	err := e.file.Close()
	e.file = nil
	return err
}
