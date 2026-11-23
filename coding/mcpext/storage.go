package mcpext

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/atomicfile"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// Ports packages/coding-agent/src/core/auth-storage.ts (AuthStorageBackend,
// FileAuthStorageBackend, InMemoryAuthStorageBackend), the parts the MCP
// credential store uses.

// AuthStorageBackend gives exclusive read-modify-write access to one stored
// document.
type AuthStorageBackend interface {
	// WithLock calls fn with the current content (exists is false when there is
	// none) while no other holder reads or writes it. When fn returns a non-nil
	// next, it replaces the content.
	WithLock(fn func(current string, exists bool) (next *string, err error)) error
}

// FileAuthStorageBackend stores the document in a file guarded by Pi's
// directory lock protocol. The file is created private (0600) in a private
// directory (0700).
type FileAuthStorageBackend struct{ path string }

// NewFileAuthStorageBackend returns a backend for path.
func NewFileAuthStorageBackend(path string) *FileAuthStorageBackend {
	return &FileAuthStorageBackend{path: path}
}

// statStoragePath is os.Stat for the pre-lock existence probe in WithLock; tests wrap it to order a concurrent writer between the probe and the initial create.
var statStoragePath = os.Stat

// WithLock implements [AuthStorageBackend].
func (b *FileAuthStorageBackend) WithLock(fn func(current string, exists bool) (*string, error)) (err error) {
	if _, statErr := os.Stat(filepath.Dir(b.path)); os.IsNotExist(statErr) {
		if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
			return err
		}
	}
	if _, statErr := statStoragePath(b.path); os.IsNotExist(statErr) {
		if err := createEmptyStorageFile(b.path); err != nil {
			return err
		}
	}
	lock, err := pilock.AcquireAuthSync(b.path)
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(); err == nil {
			err = releaseErr
		}
	}()
	data, readErr := os.ReadFile(b.path)
	exists := readErr == nil
	next, err := fn(string(data), exists)
	if err != nil {
		return err
	}
	if next != nil {
		// pig divergence (D651): replaced through a private temporary file and a rename, in place where that is refused; Pi writes the file in place.
		return atomicfile.WriteFile(b.path, []byte(*next), 0o600)
	}
	return nil
}

// createEmptyStorageFile materializes a missing credential file as "{}" using
// exclusive creation (O_CREATE|O_EXCL on POSIX, CREATE_NEW with an owner-only
// DACL on Windows), following dangling symlinks so a symlinked mcp-auth.json
// creates its target. A file that already exists is never opened or truncated.
func createEmptyStorageFile(path string) error {
	file, err := createNewFollowingLinks(path, ownerfile.CreateNew)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("{}")
	return errors.Join(writeErr, file.Close())
}

// createNewFollowingLinks creates path with create (an exclusive create) and,
// when path is a dangling symlink, creates its target. Mirrors ai/create_through_link.go.
func createNewFollowingLinks(path string, create func(string) (*os.File, error)) (*os.File, error) {
	file, err := create(path)
	if !errors.Is(err, fs.ErrExist) {
		return file, err
	}
	//perf:ok cold path: once per mcp-auth file create, after exclusive creation reported an existing entry or dangling link; stat decides whether the link is live or ELOOP
	if _, statErr := os.Stat(path); statErr == nil {
		return file, err
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		if errno, ok := errors.AsType[syscall.Errno](statErr); ok {
			return nil, &fs.PathError{Op: "open", Path: path, Err: errno}
		}
		return nil, err
	}
	target := path
	seen := map[string]bool{}
	for !seen[target] {
		seen[target] = true
		//perf:ok cold path: runs only when exclusive creation reported a dangling link; each hop is a link of the chain being resolved
		info, statErr := os.Lstat(target)
		if statErr != nil || info.Mode()&fs.ModeSymlink == 0 {
			break
		}
		next, readErr := os.Readlink(target)
		if readErr != nil {
			return nil, err
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(target), next)
		}
		target = next
	}
	if target == path {
		return nil, err
	}
	file, createErr := create(target)
	if pathErr, ok := errors.AsType[*fs.PathError](createErr); ok && pathErr.Path == target {
		return file, &fs.PathError{Op: pathErr.Op, Path: path, Err: pathErr.Err}
	}
	return file, createErr
}

// InMemoryAuthStorageBackend keeps the document in memory.
type InMemoryAuthStorageBackend struct {
	mu      sync.Mutex
	value   string
	present bool
}

// WithLock implements [AuthStorageBackend].
func (b *InMemoryAuthStorageBackend) WithLock(fn func(current string, exists bool) (*string, error)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	next, err := fn(b.value, b.present)
	if err != nil {
		return err
	}
	if next != nil {
		b.value, b.present = *next, true
	}
	return nil
}
