// Package node opens JSONL durable storage on the local file system, the Go counterpart of Pi's Node JSONL adapter.
package node

// Ports packages/durable/src/storage/jsonl/node.ts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// OpenNodeJsonlStorage opens or creates a JSONL storage directory on the local file system, resolving a relative
// directory against the process working directory.
func OpenNodeJsonlStorage(
	ctx context.Context, directory string, options jsonl.JsonlStorageOptions,
) (*jsonl.JsonlStorage, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return jsonl.Open(ctx, directory, &NodeFileSystem{Cwd: cwd}, options)
}

// NodeFileSystem is the local file system as JSONL storage uses it. Its operations follow the file half of Pi's
// NodeExecutionEnv (packages/durable/src/env/node.ts): paths resolve against Cwd, and failures are *env.FileError.
type NodeFileSystem struct {
	Cwd string
}

var _ jsonl.FileSystem = (*NodeFileSystem)(nil)

// resolve is resolvePath (packages/durable/src/env/node.ts:58-71): "~" and "~/" expand to the home directory, a
// file:// URL becomes its path, and the result is Node's path.resolve against Cwd.
func (fileSystem *NodeFileSystem) resolve(path string) (string, error) {
	windows := runtime.GOOS == "windows"
	normalized := path
	switch {
	case normalized == "~":
		normalized = homedir()
	case strings.HasPrefix(normalized, "~/") || (windows && strings.HasPrefix(normalized, `~\`)):
		normalized = filepath.Join(homedir(), normalized[2:])
	case strings.HasPrefix(normalized, "file://"):
		// Keep malformed URLs as ordinary paths so file system methods preserve their non-throwing contract.
		if converted, err := nodeurl.FileURLToPath(normalized, windows); err == nil {
			normalized = converted
		}
	}
	if nodepath.IsAbsolute(normalized) {
		return nodepath.Resolve(normalized)
	}
	return nodepath.Resolve(fileSystem.Cwd, normalized)
}

// homedir is Node's os.homedir(): the HOME (USERPROFILE on Windows) variable, else the account's home directory.
func homedir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	if account, err := user.Current(); err == nil {
		return account.HomeDir
	}
	return ""
}

// toFileError classifies an OS failure as Pi's toFileError does (env/node.ts).
func toFileError(err error, path string) error {
	if fileError, ok := errors.AsType[*env.FileError](err); ok {
		return fileError
	}
	if pathError, ok := errors.AsType[*fs.PathError](err); ok {
		path = pathError.Path
	}
	code := env.FileErrorUnknown
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code = env.FileErrorAborted
	case errors.Is(err, fs.ErrNotExist):
		code = env.FileErrorNotFound
	case errors.Is(err, fs.ErrPermission):
		code = env.FileErrorPermissionDenied
	case errors.Is(err, syscall.ENOTDIR):
		code = env.FileErrorNotDirectory
	case errors.Is(err, syscall.EISDIR):
		code = env.FileErrorIsDirectory
	case errors.Is(err, syscall.EINVAL):
		code = env.FileErrorInvalid
	}
	return env.NewFileError(code, err.Error(), path, err)
}

func aborted(ctx context.Context, path string) error {
	if ctx.Err() != nil {
		return env.NewFileError(env.FileErrorAborted, "aborted", path, nil)
	}
	return nil
}

func contentBytes(content any) []byte {
	switch typed := content.(type) {
	case string:
		return []byte(typed)
	case []byte:
		return typed
	default:
		return nil
	}
}

// AbsolutePath resolves path against Cwd.
func (fileSystem *NodeFileSystem) AbsolutePath(_ context.Context, path string) (string, error) {
	return fileSystem.resolve(path)
}

// JoinPath joins and cleans path parts.
func (fileSystem *NodeFileSystem) JoinPath(_ context.Context, parts []string) (string, error) {
	return filepath.Join(parts...), nil
}

// ReadBinaryFile reads a whole file.
func (fileSystem *NodeFileSystem) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := aborted(ctx, resolved); err != nil {
		return nil, err
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	return content, nil
}

// WriteFile replaces a file's content, creating its parent directory.
func (fileSystem *NodeFileSystem) WriteFile(ctx context.Context, path string, content any) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o777); err != nil {
		return toFileError(err, resolved)
	}
	if err := os.WriteFile(resolved, contentBytes(content), 0o666); err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

// AppendFile appends to a file, creating it and its parent directory.
func (fileSystem *NodeFileSystem) AppendFile(ctx context.Context, path string, content any) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o777); err != nil {
		return toFileError(err, resolved)
	}
	file, err := os.OpenFile(resolved, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o666)
	if err != nil {
		return toFileError(err, resolved)
	}
	_, writeErr := file.Write(contentBytes(content))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return toFileError(err, resolved)
	}
	return aborted(ctx, resolved)
}

// TruncateFile truncates or extends a file to exactly size bytes.
func (fileSystem *NodeFileSystem) TruncateFile(ctx context.Context, path string, size int64) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	if size < 0 || size > 1<<53-1 {
		return env.NewFileError(env.FileErrorInvalid, "File size must be a non-negative safe integer", resolved, nil)
	}
	file, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		return toFileError(err, resolved)
	}
	truncateErr := file.Truncate(size)
	_ = file.Close()
	if truncateErr != nil {
		return toFileError(truncateErr, resolved)
	}
	return aborted(ctx, resolved)
}

// FlushFile flushes a file's contents and the metadata needed to retrieve them.
func (fileSystem *NodeFileSystem) FlushFile(ctx context.Context, path string) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	file, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		return toFileError(err, resolved)
	}
	syncErr := file.Sync()
	_ = file.Close()
	if syncErr != nil {
		return toFileError(syncErr, resolved)
	}
	return aborted(ctx, resolved)
}

// RenameFile renames a file, replacing the destination.
func (fileSystem *NodeFileSystem) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	source, err := fileSystem.resolve(sourcePath)
	if err != nil {
		return err
	}
	destination, err := fileSystem.resolve(destinationPath)
	if err != nil {
		return err
	}
	if err := aborted(ctx, destination); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return toFileError(err, source)
	}
	return nil
}

// ListDir lists a directory's entries without following symbolic links.
func (fileSystem *NodeFileSystem) ListDir(ctx context.Context, path string) ([]env.FileInfo, error) {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return nil, err
	}
	if err := aborted(ctx, resolved); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, toFileError(err, resolved)
	}
	infos := make([]env.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if err := aborted(ctx, resolved); err != nil {
			return nil, err
		}
		entryPath := filepath.Join(resolved, entry.Name())
		info, err := os.Lstat(entryPath)
		if err != nil {
			return nil, toFileError(err, entryPath)
		}
		kind, ok := fileKind(info)
		if !ok {
			continue
		}
		infos = append(infos, env.FileInfo{
			Name:    entry.Name(),
			Path:    entryPath,
			Kind:    kind,
			Size:    info.Size(),
			MtimeMs: float64(info.ModTime().UnixNano()) / 1e6,
		})
	}
	return infos, nil
}

func fileKind(info fs.FileInfo) (env.FileKind, bool) {
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return env.FileKindSymlink, true
	case info.IsDir():
		return env.FileKindDirectory, true
	case info.Mode().IsRegular():
		return env.FileKindFile, true
	default:
		return "", false
	}
}

// CreateDir creates a directory; options default to recursive.
func (fileSystem *NodeFileSystem) CreateDir(ctx context.Context, path string, options *env.CreateDirOptions) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	if options == nil || options.Recursive == nil || *options.Recursive {
		err = os.MkdirAll(resolved, 0o777)
	} else {
		err = os.Mkdir(resolved, 0o777)
	}
	if err != nil {
		return toFileError(err, resolved)
	}
	return nil
}

// Remove removes a file or, with Recursive, a tree; Force ignores a missing path.
func (fileSystem *NodeFileSystem) Remove(ctx context.Context, path string, options *env.RemoveOptions) error {
	resolved, err := fileSystem.resolve(path)
	if err != nil {
		return err
	}
	if err := aborted(ctx, resolved); err != nil {
		return err
	}
	recursive := options != nil && options.Recursive
	force := options != nil && options.Force
	if recursive {
		if _, statErr := os.Lstat(resolved); statErr != nil {
			err = statErr
		} else {
			err = os.RemoveAll(resolved)
		}
	} else {
		err = os.Remove(resolved)
	}
	if err != nil {
		if force && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return toFileError(err, resolved)
	}
	return nil
}
