// Package installchange tells a running pig that the files it runs from changed on disk.
//
// Pi (packages/coding-agent/src/config.ts detectInstallChange, #10439) warns when an update or removal replaced the
// package its process loads code from. PiG is one native executable that embeds its code, so the same hazard comes
// from other files: an update replaces or removes the executable, and another pig's cache prune removes an extension
// cell or runtime file this process still starts on demand.
//
// pig additive (D95): there is no upstream equivalent of tracking an executable and cache files.
package installchange

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// FileID is a file's device and inode on Unix, or its volume serial number and file index on Windows.
type FileID struct {
	Device uint64
	Index  uint64
}

// Identity is what a path named when it was recorded.
type Identity struct {
	// Path is the path with its symbolic links resolved.
	Path    string
	File    FileID
	Size    int64
	ModTime time.Time
}

func (i Identity) same(other Identity) bool {
	return i.Path == other.Path && i.File == other.File && i.Size == other.Size && i.ModTime.Equal(other.ModTime)
}

// Kind classifies a change.
type Kind string

const (
	// BinaryReplaced means the executable at the recorded path is another file now.
	BinaryReplaced Kind = "binary-replaced"
	// BinaryRemoved means the executable is gone.
	BinaryRemoved Kind = "binary-removed"
	// FilesPruned means an extension cell or runtime file this process uses is gone.
	FilesPruned Kind = "files-pruned"
)

// Change is one detected change. Path names the file that changed.
type Change struct {
	Kind Kind
	Path string
}

// Tracker holds what one process recorded at startup.
type Tracker struct {
	started    bool
	executable Identity
	// launched is the path the process started from, which may be a symbolic link that a package manager repoints.
	launched string

	mu    sync.Mutex
	files []string
}

// NewTracker records executable's identity, or this process's executable (linkerexec.Executable, which knows a program started through the Android linker) when executable is empty. An executable that
// cannot be read at startup is not tracked, because there is nothing to compare it with.
func NewTracker(executable string) *Tracker {
	t := &Tracker{}
	if executable == "" {
		var err error
		if executable, err = linkerexec.Executable(); err != nil {
			return t
		}
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return t
	}
	identity, err := identityOf(resolved)
	if err != nil {
		return t
	}
	t.executable, t.launched, t.started = identity, executable, true
	return t
}

// Executable returns what was recorded for the executable.
func (t *Tracker) Executable() (Identity, bool) {
	if t == nil || !t.started {
		return Identity{}, false
	}
	return t.executable, true
}

// TrackFile records a cell or runtime file this process uses, so a later Detect notices that it is gone. A repeated path
// is recorded once.
func (t *Tracker) TrackFile(path string) {
	if t == nil || path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !slices.Contains(t.files, path) {
		t.files = append(t.files, path)
	}
}

// TrackedFiles returns the recorded cell and runtime files in the order they were recorded.
func (t *Tracker) TrackedFiles() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.files)
}

// Detect reports the first change since startup, or nil.
// Ports packages/coding-agent/src/config.ts (detectInstallChange).
//
// It checks the recorded paths and never searches for another
// install, so an install removed from under the process is reported as removed even when another executable of the
// same name sits further up the tree, as Pi reads the package.json it started from. Only a missing file counts: an
// error such as a permission failure says nothing about the install.
func (t *Tracker) Detect() *Change {
	if t == nil {
		return nil
	}
	if change := t.detectExecutable(); change != nil {
		return change
	}
	for _, path := range t.TrackedFiles() {
		if _, err := os.Lstat(path); isMissing(err) {
			return &Change{Kind: FilesPruned, Path: path}
		}
	}
	return nil
}

func (t *Tracker) detectExecutable() *Change {
	if !t.started {
		return nil
	}
	path := t.executable.Path
	current, err := identityOf(path)
	switch {
	case isMissing(err):
		return &Change{Kind: BinaryRemoved, Path: path}
	case err != nil:
		return nil
	case !current.same(t.executable):
		return &Change{Kind: BinaryReplaced, Path: path}
	}
	if t.launched == path {
		return nil
	}
	relaunched, err := filepath.EvalSymlinks(t.launched)
	switch {
	case isMissing(err):
		return &Change{Kind: BinaryRemoved, Path: t.launched}
	case err != nil:
		return nil
	case relaunched != path:
		return &Change{Kind: BinaryReplaced, Path: t.launched}
	}
	return nil
}

func isMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

var defaultTracker atomic.Pointer[Tracker]

// Record records this process's executable. The first call wins, so the identity is the startup one.
func Record() {
	defaultTracker.CompareAndSwap(nil, NewTracker(""))
}

// Default returns the tracker Record made, or nil before Record.
func Default() *Tracker { return defaultTracker.Load() }

// TrackFile records a cell or runtime file on the process's tracker. Before Record it does nothing.
func TrackFile(path string) { Default().TrackFile(path) }

// Detect reports the first change on the process's tracker. Before Record it reports nothing.
func Detect() *Change { return Default().Detect() }
