package tsruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Executable is a slot executable that Verify checked.
//
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
//
// On Linux it holds the file that was hashed open, and ExecPath names that open file instead of the path it was opened by: a symbolic
// link named after the executable, in a private directory, to /proc/self/fd/N, so that the kernel names the process after the
// executable (comm, and so ps, top, pgrep and OOM logs) and not after the descriptor number. So the process a launch starts runs the bytes that were verified even when the path is replaced, renamed over or
// unlinked after Verify returned: verify and exec are the same file. ExecPath also compares the file's identity with the one
// verified and refuses an executable that was written to in place since, so the remaining window is the few microseconds between
// that check and the kernel taking the file for exec, after which a write to it fails with ETXTBSY. The comparison ignores the
// ctime of a file that no directory entry names any more, because unlinking it (the swap this defends against) sets its ctime, and
// nobody can open an unlinked file for writing; a writer that opened the file before the check and writes after it, through that
// open file, is a same-user process, which is inside the trust boundary.
//
// The directory is made by os.MkdirTemp (mode 0700, a new random name that no earlier process's directory is ever reused for) under
// os.TempDir, when ExecPath is first asked for, and removed by Close. A directory that a crashed process left is removed by a later
// process once it is older than staleExecDirAge. The link adds nothing to the trust boundary beyond a directory that only the
// current user can enter: a process of that user can replace it, as it can replace the executable and runtime.json. Where the
// directory cannot be made, ExecPath is /proc/self/fd/N itself and the process is named after the descriptor.
//
// Where it cannot hold the file (every platform but Linux; on Linux a file that starts with "#!", whose interpreter would open the
// descriptor after exec closed it; Android, whose launcher starts programs through the system linker; a /proc that does not show the
// file), ExecPath is the path, and the file at that path at exec time runs. A writer that can replace the executable between the
// check and the exec, or the record files and runtime.json, is inside the trust boundary there, as it was before the descriptor was
// held.
type Executable struct {
	path     string
	identity fileIdentity
	identErr error

	mu     sync.Mutex
	file   *os.File
	fdPath string
	// dir and link are the private directory and the link in it, made by the first ExecPath.
	dir, link string
}

// staleExecDirAge is how old a private directory of another process must be to be taken for one a crashed process left: a launch
// holds its directory from ExecPath until the process has started, which is milliseconds.
// pig additive (D19): Pi has no runtime slot whose executable a start verifies.
const staleExecDirAge = time.Hour

// execDirPrefix names the private directories under os.TempDir.
const execDirPrefix = "pig-exec-"

var sweepExecDirsOnce sync.Once

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
	return e.execPath(true)
}

// probePath is ExecPath for a start that is not worth a named link, such as the --version probe: /proc/self/fd/N.
func (e *Executable) probePath() (string, error) {
	return e.execPath(false)
}

func (e *Executable) execPath(named bool) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fdPath == "" {
		return e.path, nil
	}
	if e.file == nil {
		return "", fmt.Errorf("the verified handle of %s was released before it started", e.path)
	}
	info, err := e.file.Stat() //perf:ok cold: once per extension launch
	if err != nil {
		return "", err
	}
	current, err := identify(info)
	if err != nil || !e.identity.unchangedBy(current, unlinked(info)) {
		return "", errors.New(e.path + " changed after it was verified")
	}
	if !named {
		return e.fdPath, nil
	}
	if e.link == "" {
		e.link = e.makeLink()
	}
	return e.link, nil
}

// makeLink makes the private directory and the link named after the executable, and returns the link, or /proc/self/fd/N when it
// cannot be made.
func (e *Executable) makeLink() string {
	//portlint:allow golifetime a one-shot sweep of old private directories that ends when it has listed the temporary directory once; nothing waits for it and losing it at exit loses nothing
	sweepExecDirsOnce.Do(func() { go sweepExecDirs(os.TempDir(), time.Now()) })
	dir, err := os.MkdirTemp("", execDirPrefix+"*")
	if err != nil {
		return e.fdPath
	}
	link := filepath.Join(dir, filepath.Base(e.path))
	if err := os.Symlink(e.fdPath, link); err != nil {
		_ = os.RemoveAll(dir)
		return e.fdPath
	}
	e.dir = dir
	return link
}

// sweepExecDirs removes the private directories in tmp that the current user owns and that are older than staleExecDirAge at now.
func sweepExecDirs(tmp string, now time.Time) {
	dir, err := os.Open(tmp)
	if err != nil {
		return
	}
	names, _ := dir.Readdirnames(-1)
	_ = dir.Close() // Read-only: close cannot lose data.
	for _, name := range names {
		if !strings.HasPrefix(name, execDirPrefix) {
			continue
		}
		path := filepath.Join(tmp, name)
		//perf:ok cold: the sweep runs once per extension launch, over the few leftover directories of dead launches
		if info, err := os.Lstat(path); err == nil && info.IsDir() && ownedByCurrentUser(info) && now.Sub(info.ModTime()) >= staleExecDirAge {
			_ = os.RemoveAll(path)
		}
	}
}

// Close releases the open file and removes the private directory. A launch calls it once its process has started, which is when the
// kernel has taken the file. It is safe on a nil Executable and more than once.
func (e *Executable) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dir != "" {
		_ = os.RemoveAll(e.dir)
		e.dir, e.link = "", ""
	}
	if e.file == nil {
		return nil
	}
	err := e.file.Close()
	e.file = nil
	return err
}
