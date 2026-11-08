package mcpext

import (
	"os"
	"path/filepath"
	"sync"

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

// WithLock implements [AuthStorageBackend].
func (b *FileAuthStorageBackend) WithLock(fn func(current string, exists bool) (*string, error)) (err error) {
	if _, statErr := os.Stat(filepath.Dir(b.path)); os.IsNotExist(statErr) {
		if err := os.MkdirAll(filepath.Dir(b.path), 0o700); err != nil {
			return err
		}
	}
	if _, statErr := os.Stat(b.path); os.IsNotExist(statErr) {
		if err := os.WriteFile(b.path, []byte("{}"), 0o600); err != nil {
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
		return writeFileAtomic(b.path, []byte(*next))
	}
	return nil
}

// writeFileAtomic replaces path with data through a temporary file in the same directory (fsynced, then renamed), so a crash or a full disk
// never leaves a torn credential file. Pi's writeFileSync keeps the mode of a file that exists and creates a new one private (0600); so does this.
func writeFileAtomic(path string, data []byte) (err error) {
	// writeFileSync writes through a symlink to its target; the rename below would replace the link itself.
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}
	mode := os.FileMode(0o600)
	//perf:ok one stat per credential write, which already syncs a file
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	temp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = temp.Close()
			_ = os.Remove(temp.Name())
		}
	}()
	if err = temp.Chmod(mode); err != nil {
		return err
	}
	if _, err = temp.Write(data); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	// The rename is durable once its directory entry is; a platform that cannot open or sync a directory has nothing more to give.
	syncDirectory(filepath.Dir(path))
	return nil
}

// syncDirectory flushes a directory's entries and reports whether it could; the credential write is complete either way.
func syncDirectory(path string) bool {
	dir, err := os.Open(path)
	if err != nil {
		return false
	}
	synced := dir.Sync() == nil
	_ = dir.Close()
	return synced
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
