// Package node is the local execution environment of pi-durable: NodeExecutionEnv runs on the host file system and
// shell. It is a separate package, as upstream's env/node subpath, so the portable env package loads no host package.
package node

// Ports packages/durable/src/env/node.ts

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the largest size
// TruncateFile accepts.
const maxSafeInteger = 1<<53 - 1

// NodeExecutionEnvOptions configure a NodeExecutionEnv.
type NodeExecutionEnvOptions struct {
	Cwd string
	// ShellPath is an explicit bash executable; empty selects the platform
	// default.
	ShellPath string
	// ShellEnv is layered over the inherited process environment for every
	// command.
	ShellEnv map[string]string
	// Watch configures FileSystem.Watch.
	Watch NodeWatchOptions
}

// NodeExecutionEnv is the host-OS execution environment: filesystem access and
// bash command execution. Its name mirrors the upstream Node.js implementation.
type NodeExecutionEnv struct {
	// Self is the environment the methods of this one call for their own
	// building blocks: Exec creates its spill file with Self.CreateTempFile,
	// Exists calls Self.FileInfo, ReadTextLines calls Self.OpenTextLineReader,
	// and CreateTempFile calls Self.CreateTempDir. A type that embeds
	// NodeExecutionEnv and overrides one of those methods sets Self to itself,
	// which is the dispatch a TypeScript subclass gets from this. It defaults to
	// the NodeExecutionEnv.
	Self durableenv.ExecutionEnv

	// cwdMu guards cwd: SetCwd may run while other goroutines resolve paths.
	cwdMu     sync.RWMutex
	cwd       string
	shellPath string
	shellEnv  map[string]string
	watch     NodeWatchOptions

	mu              sync.Mutex
	activeChildPids map[int]struct{}
}

var _ durableenv.ExecutionEnv = (*NodeExecutionEnv)(nil)

// NewNodeExecutionEnv constructs an environment rooted at options.Cwd.
func NewNodeExecutionEnv(options NodeExecutionEnvOptions) *NodeExecutionEnv {
	env := &NodeExecutionEnv{
		cwd:             options.Cwd,
		shellPath:       options.ShellPath,
		shellEnv:        options.ShellEnv,
		watch:           options.Watch,
		activeChildPids: map[int]struct{}{},
	}
	env.Self = env
	return env
}

// Id is the file namespace: every local environment sees the same files.
func (env *NodeExecutionEnv) Id() string { return "node:local" }

// Cwd is the directory relative paths resolve against.
func (env *NodeExecutionEnv) Cwd() string {
	env.cwdMu.RLock()
	defer env.cwdMu.RUnlock()
	return env.cwd
}

// SetCwd changes the directory relative paths resolve against.
func (env *NodeExecutionEnv) SetCwd(cwd string) {
	env.cwdMu.Lock()
	defer env.cwdMu.Unlock()
	env.cwd = cwd
}

// AbsolutePath resolves path without requiring it to exist.
func (env *NodeExecutionEnv) AbsolutePath(_ context.Context, path string) (string, error) {
	return resolvePath(env.Cwd(), path), nil
}

// JoinPath joins and normalizes path segments like Node's path.join.
func (env *NodeExecutionEnv) JoinPath(_ context.Context, parts []string) (string, error) {
	joined := filepath.Join(parts...)
	if joined == "" {
		return ".", nil
	}
	if endsWithSeparator(lastNonEmpty(parts)) && !endsWithSeparator(joined) {
		joined += string(filepath.Separator)
	}
	return joined, nil
}

func lastNonEmpty(parts []string) string {
	for _, part := range slices.Backward(parts) {
		if part != "" {
			return part
		}
	}
	return ""
}

func endsWithSeparator(path string) bool {
	return path != "" && os.IsPathSeparator(path[len(path)-1])
}

// OpenTextLineReader opens path for pull-based line reading.
func (env *NodeExecutionEnv) OpenTextLineReader(ctx context.Context, path string) (durableenv.TextLineReader, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	if abortErr := abortedFileError(ctx, resolved); abortErr != nil {
		_ = file.Close()
		return nil, abortErr
	}
	return &nodeTextLineReader{file: file, path: resolved, chunk: make([]byte, textLineChunkBytes)}, nil
}

// ReadTextFile reads path as UTF-8, replacing invalid sequences.
func (env *NodeExecutionEnv) ReadTextFile(ctx context.Context, path string) (string, error) {
	data, err := env.ReadBinaryFile(ctx, path)
	if err != nil {
		return "", err
	}
	return jsstring.FromUTF8(data), nil
}

// ReadTextLines reads up to options.MaxLines lines; a non-positive limit
// returns no lines.
func (env *NodeExecutionEnv) ReadTextLines(ctx context.Context, path string, options *durableenv.ReadTextLinesOptions) ([]string, error) {
	var maxLines *int
	if options != nil {
		maxLines = options.MaxLines
	}
	if maxLines != nil && *maxLines <= 0 {
		return []string{}, nil
	}
	reader, err := env.Self.OpenTextLineReader(ctx, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close(ctx) }()
	lines := []string{}
	for maxLines == nil || len(lines) < *maxLines {
		line, err := reader.ReadLine(ctx)
		if err != nil {
			return nil, err
		}
		if line == nil {
			break
		}
		lines = append(lines, line.Text)
	}
	return lines, nil
}

// ReadBinaryFile reads path.
func (env *NodeExecutionEnv) ReadBinaryFile(ctx context.Context, path string) ([]byte, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	if abortErr := abortedFileError(ctx, resolved); abortErr != nil {
		return nil, abortErr
	}
	return data, nil
}

// WriteFile creates or overwrites path with content, a string or []byte,
// creating parent directories.
func (env *NodeExecutionEnv) WriteFile(ctx context.Context, path string, content any) error {
	return env.writeWithParents(ctx, resolvePath(env.Cwd(), path), content, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
}

// AppendFile creates or appends to path with content, a string or []byte,
// creating parent directories.
func (env *NodeExecutionEnv) AppendFile(ctx context.Context, path string, content any) error {
	resolved := resolvePath(env.Cwd(), path)
	if err := env.writeWithParents(ctx, resolved, content, os.O_WRONLY|os.O_CREATE|os.O_APPEND); err != nil {
		return err
	}
	return abortedFileError(ctx, resolved)
}

// writeWithParents writes content to the resolved path after creating its
// parent directory as upstream's mkdir(resolve(resolved, ".."), {recursive:
// true}) does: a failure reports the parent, with EEXIST when the parent exists
// but is not a directory.
func (env *NodeExecutionEnv) writeWithParents(ctx context.Context, resolved string, content any, flags int) error {
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	data, err := fileContent(content)
	if err != nil {
		return &durableenv.FileError{Code: durableenv.FileErrorInvalid, Message: err.Error(), Path: resolved}
	}
	parent := filepath.Dir(resolved)
	if err := nodeerrno.MkdirAll(parent, 0o777); err != nil {
		return toFileError(err, fsCall{syscall: "mkdir", path: parent})
	}
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	file, err := os.OpenFile(resolved, flags, 0o666)
	if err != nil {
		return toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return toFileError(err, fsCall{syscall: "write", path: resolved})
	}
	return nil
}

// TruncateFile truncates or extends path to exactly size bytes; it never
// creates the file. A size that is negative or above JavaScript's largest safe
// integer is invalid.
func (env *NodeExecutionEnv) TruncateFile(ctx context.Context, path string, size int64) error {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	if size < 0 || size > maxSafeInteger {
		return &durableenv.FileError{Code: durableenv.FileErrorInvalid, Message: "File size must be a non-negative safe integer", Path: resolved}
	}
	file, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		return toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	defer closeQuietly(file)
	if err := file.Truncate(size); err != nil {
		return toFileError(err, fsCall{syscall: "ftruncate", path: resolved, fd: true})
	}
	return abortedFileError(ctx, resolved)
}

// syncFile flushes an open file to stable storage; tests replace it to fail the
// flush.
var syncFile = (*os.File).Sync

// FlushFile flushes the contents of path, and the metadata needed to retrieve
// them from an open file handle, to stable storage.
func (env *NodeExecutionEnv) FlushFile(ctx context.Context, path string) error {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	file, err := os.OpenFile(resolved, os.O_RDWR, 0)
	if err != nil {
		// POSIX refuses to open a directory for writing with EISDIR; Windows refuses it as access denied, where Node
		// opens it, so a directory is reported as one.
		if stats, statErr := os.Stat(resolved); statErr == nil && stats.IsDir() && errnoCode(err) != "EISDIR" {
			return &durableenv.FileError{Code: durableenv.FileErrorIsDirectory, Message: "Is a directory", Path: resolved}
		}
		return toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	defer closeQuietly(file)
	if err := syncFile(file); err != nil {
		return toFileError(err, fsCall{syscall: "fsync", path: resolved, fd: true})
	}
	return abortedFileError(ctx, resolved)
}

// closeQuietly closes a file whose outcome was already decided; closing is
// best-effort.
func closeQuietly(file *os.File) { _ = file.Close() }

// fileContent is the bytes of the content a write takes: a string or a []byte.
func fileContent(content any) ([]byte, error) {
	switch typed := content.(type) {
	case string:
		return []byte(typed), nil
	case []byte:
		return typed, nil
	}
	return nil, fmt.Errorf("file content must be a string or []byte, not %T", content)
}

// RenameFile atomically renames sourcePath over destinationPath.
func (env *NodeExecutionEnv) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	source := resolvePath(env.Cwd(), sourcePath)
	destination := resolvePath(env.Cwd(), destinationPath)
	if err := abortedFileError(ctx, destination); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return toFileError(err, fsCall{syscall: "rename", path: source, dest: destination})
	}
	return nil
}

// FileInfo returns metadata without following symlinks.
func (env *NodeExecutionEnv) FileInfo(ctx context.Context, path string) (durableenv.FileInfo, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return durableenv.FileInfo{}, err
	}
	stats, err := os.Lstat(resolved)
	if err != nil {
		return durableenv.FileInfo{}, toFileError(err, fsCall{syscall: "lstat", path: resolved})
	}
	return fileInfoFromStats(resolved, stats)
}

func fileInfoFromStats(path string, stats fs.FileInfo) (durableenv.FileInfo, error) {
	var kind durableenv.FileKind
	switch mode := stats.Mode(); {
	case mode.IsRegular():
		kind = durableenv.FileKindFile
	case mode.IsDir():
		kind = durableenv.FileKindDirectory
	case mode&fs.ModeSymlink != 0:
		kind = durableenv.FileKindSymlink
	default:
		return durableenv.FileInfo{}, &durableenv.FileError{Code: durableenv.FileErrorInvalid, Message: "Unsupported file type", Path: path}
	}
	return durableenv.FileInfo{
		Name:    filepath.Base(path),
		Path:    path,
		Kind:    kind,
		Size:    stats.Size(),
		MtimeMs: float64(stats.ModTime().UnixNano()) / 1e6,
	}, nil
}

// ListDir lists direct children in directory order without following
// symlinks; unsupported file types are omitted.
func (env *NodeExecutionEnv) ListDir(ctx context.Context, path string) ([]durableenv.FileInfo, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	names, err := readDirNames(resolved)
	if err != nil {
		return nil, toFileError(err, fsCall{syscall: "scandir", path: resolved})
	}
	infos := []durableenv.FileInfo{}
	for _, name := range names {
		if err := abortedFileError(ctx, resolved); err != nil {
			return nil, err
		}
		entryPath := filepath.Join(resolved, name)
		stats, err := os.Lstat(entryPath)
		if err != nil {
			return nil, toFileError(err, fsCall{syscall: "lstat", path: entryPath})
		}
		if info, err := fileInfoFromStats(entryPath, stats); err == nil {
			infos = append(infos, info)
		}
	}
	return infos, nil
}

func readDirNames(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "scandir", Path: path, Err: nodeErrorCode("ENOTDIR")}
	}
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	names, err := directory.Readdirnames(-1)
	_ = directory.Close()
	return names, err
}

// CanonicalPath resolves every symlink in an existing path.
func (env *NodeExecutionEnv) CanonicalPath(ctx context.Context, path string) (string, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", toFileError(err, fsCall{syscall: "realpath", path: resolved})
	}
	return canonical, nil
}

// Exists reports false for missing paths and other failures as errors.
func (env *NodeExecutionEnv) Exists(ctx context.Context, path string) (bool, error) {
	_, err := env.Self.FileInfo(ctx, path)
	if err == nil {
		return true, nil
	}
	var fileErr *durableenv.FileError
	if errors.As(err, &fileErr) && fileErr.Code == durableenv.FileErrorNotFound {
		return false, nil
	}
	return false, err
}

// CreateDir creates a directory; Recursive defaults to true.
func (env *NodeExecutionEnv) CreateDir(ctx context.Context, path string, options *durableenv.CreateDirOptions) error {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	recursive := options == nil || options.Recursive == nil || *options.Recursive
	var err error
	if recursive {
		// Node's recursive mkdir reports EEXIST for a path that exists but is not a directory.
		err = nodeerrno.MkdirAll(resolved, 0o777)
	} else {
		err = os.Mkdir(resolved, 0o777)
	}
	if err != nil {
		return toFileError(err, fsCall{syscall: "mkdir", path: resolved})
	}
	return nil
}

// Remove deletes a file or directory; a directory requires Recursive and a
// missing path requires Force.
func (env *NodeExecutionEnv) Remove(ctx context.Context, path string, options *durableenv.RemoveOptions) error {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	if options == nil {
		options = &durableenv.RemoveOptions{}
	}
	stats, err := os.Lstat(resolved)
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist) && options.Force:
		return nil
	case err != nil:
		return toFileError(err, fsCall{syscall: "lstat", path: resolved})
	case stats.IsDir() && !options.Recursive:
		return &durableenv.FileError{Code: durableenv.FileErrorUnknown, Message: "Path is a directory: rm returned EISDIR (is a directory) " + resolved, Path: resolved}
	}
	if err := os.RemoveAll(resolved); err != nil {
		return toFileError(err, fsCall{syscall: "rm", path: resolved})
	}
	return nil
}

const tempNameAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// CreateTempDir creates a directory named prefix plus six random characters
// under the OS temp directory; prefix defaults to "tmp-".
func (env *NodeExecutionEnv) CreateTempDir(ctx context.Context, prefix *string) (string, error) {
	if err := abortedFileError(ctx, ""); err != nil {
		return "", err
	}
	name := "tmp-"
	if prefix != nil {
		name = *prefix
	}
	base := filepath.Join(os.TempDir(), name)
	for {
		suffix := make([]byte, 6)
		for index := range suffix {
			suffix[index] = tempNameAlphabet[randomIndex(len(tempNameAlphabet))]
		}
		path := base + string(suffix)
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", toFileError(err, fsCall{syscall: "mkdtemp", path: base + "XXXXXX"})
		}
	}
}

func randomIndex(limit int) int {
	var value [1]byte
	_, _ = rand.Read(value[:])
	return int(value[0]) % limit
}

// CreateTempFile creates an empty file named prefix + UUID + suffix inside a
// fresh temporary directory.
func (env *NodeExecutionEnv) CreateTempFile(ctx context.Context, options *durableenv.CreateTempFileOptions) (string, error) {
	dir, err := env.Self.CreateTempDir(ctx, new("tmp-"))
	if err != nil {
		return "", err
	}
	if options == nil {
		options = &durableenv.CreateTempFileOptions{}
	}
	path := filepath.Join(dir, options.Prefix+randomUUID()+options.Suffix)
	if err := os.WriteFile(path, nil, 0o666); err != nil {
		return "", toFileError(err, fsCall{syscall: "open", path: path})
	}
	return path, nil
}

// randomUUID returns an RFC 9562 version 4 UUID.
func randomUUID() string {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

// Cleanup terminates every active command's process tree. It is best-effort and
// returns nil.
func (env *NodeExecutionEnv) Cleanup(context.Context) error {
	env.mu.Lock()
	pids := make([]int, 0, len(env.activeChildPids))
	for pid := range env.activeChildPids {
		pids = append(pids, pid)
	}
	clear(env.activeChildPids)
	env.mu.Unlock()
	for _, pid := range pids {
		_ = killProcessTree(pid)
	}
	return nil
}

// startChild starts cmd with nodespawn.Start, which closes the parent's copies
// of the child's files, and registers its pid as one step under the lock
// Cleanup takes, as upstream's synchronous spawn-then-add does: a child is
// never running yet unknown to a concurrent Cleanup.
func (env *NodeExecutionEnv) startChild(cmd *exec.Cmd) error {
	env.mu.Lock()
	defer env.mu.Unlock()
	if err := nodespawn.Start(cmd); err != nil {
		return err
	}
	env.activeChildPids[cmd.Process.Pid] = struct{}{}
	return nil
}

func (env *NodeExecutionEnv) untrackChild(pid int) {
	env.mu.Lock()
	defer env.mu.Unlock()
	delete(env.activeChildPids, pid)
}
