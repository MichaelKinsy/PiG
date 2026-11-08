package sync

import (
	"context"
	"errors"
	"io/fs"
)

// Ports packages/chord/src/sync/files.ts
//
// The file operations the directory-backed sync pieces need, as an interface: any environment that has them (a local
// directory, a container, a remote host) can back a sync remote or the host's sync state.
//
// Go forms: Pi's operations return a `FileResult` and report a missing file with `code: "not_found"`; here they return an
// error, and a missing file or directory is an error that is `fs.ErrNotExist` (IsNotFound), which `os` errors are. Context
// comes first, as Go convention, where Pi passes it last. Pi shapes the interface as Durable's `FileSystem` so its
// environments pass as they are; Go cannot share its method types without a dependency on Durable, so a host adapts its
// environment with a few lines (map its not-found to fs.ErrNotExist, its options to the flags below).

// SyncFileKind is what a directory entry is.
type SyncFileKind string

const (
	SyncFile      SyncFileKind = "file"
	SyncDirectory SyncFileKind = "directory"
	SyncSymlink   SyncFileKind = "symlink"
)

// SyncFileInfo is a directory entry.
type SyncFileInfo struct {
	Name string
	Kind SyncFileKind
}

// SyncFileReader is a file opened for reads at offsets: what streaming a body back needs.
type SyncFileReader interface {
	// Read is up to length bytes at offset; fewer only at the end of the file.
	Read(ctx context.Context, offset, length int64) ([]byte, error)
	Close(ctx context.Context) error
}

// SyncFileSystem is the file operations of the directory-backed sync pieces.
type SyncFileSystem interface {
	JoinPath(ctx context.Context, parts []string) (string, error)
	ReadTextFile(ctx context.Context, path string) (string, error)
	ReadBinaryFile(ctx context.Context, path string) ([]byte, error)
	// OpenBinaryReader opens a file for bounded reads, so a body streams back without being read whole.
	OpenBinaryReader(ctx context.Context, path string) (SyncFileReader, error)
	WriteFile(ctx context.Context, path string, content []byte) error
	// AppendFile appends to a file, creating it: a body is written chunk by chunk as it streams.
	AppendFile(ctx context.Context, path string, content []byte) error
	RenameFile(ctx context.Context, sourcePath, destinationPath string) error
	ListDir(ctx context.Context, path string) ([]SyncFileInfo, error)
	CreateDir(ctx context.Context, path string, recursive bool) error
	// Remove removes a file or directory (the contents too when recursive); force: a missing path is no error.
	Remove(ctx context.Context, path string, recursive, force bool) error
}

// IsNotFound is whether err failed because the file or directory is missing.
func IsNotFound(err error) bool { return errors.Is(err, fs.ErrNotExist) }
