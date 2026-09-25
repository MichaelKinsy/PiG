// Package env provides NodeExecutionEnv, the host-OS filesystem and shell
// implementation of the harness execution environment.
package env

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// NodeExecutionEnvOptions configure a NodeExecutionEnv.
type NodeExecutionEnvOptions struct {
	Cwd string
	// ShellPath is an explicit bash executable; empty selects the platform
	// default.
	ShellPath string
	// ShellEnv is layered over the inherited process environment for every
	// command.
	ShellEnv map[string]string
}

// NodeExecutionEnv is the host-OS execution environment: filesystem access
// and bash command execution for the harness. Its name mirrors the upstream
// Node.js implementation.
type NodeExecutionEnv struct {
	cwd       string
	shellPath string
	shellEnv  map[string]string

	mu              sync.Mutex
	activeChildPids map[int]struct{}
	// spillTempFile creates spill files; tests replace it to fail spills.
	spillTempFile func(ctx context.Context, options *harness.CreateTempFileOptions) (string, error)
}

var _ harness.ExecutionEnv = (*NodeExecutionEnv)(nil)

// NewNodeExecutionEnv constructs an environment rooted at options.Cwd.
func NewNodeExecutionEnv(options NodeExecutionEnvOptions) *NodeExecutionEnv {
	env := &NodeExecutionEnv{
		cwd:             options.Cwd,
		shellPath:       options.ShellPath,
		shellEnv:        options.ShellEnv,
		activeChildPids: map[int]struct{}{},
	}
	env.spillTempFile = env.CreateTempFile
	return env
}

// Cwd is the directory relative paths resolve against.
func (env *NodeExecutionEnv) Cwd() string { return env.cwd }

// AbsolutePath resolves path without requiring it to exist.
func (env *NodeExecutionEnv) AbsolutePath(_ context.Context, path string) (string, error) {
	return resolvePath(env.cwd, path), nil
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
func (env *NodeExecutionEnv) OpenTextLineReader(ctx context.Context, path string) (harness.TextLineReader, error) {
	resolved := resolvePath(env.cwd, path)
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
	return decodeUTF8(data), nil
}

// ReadTextLines reads up to options.MaxLines lines; a non-positive limit
// returns no lines.
func (env *NodeExecutionEnv) ReadTextLines(ctx context.Context, path string, options *harness.ReadTextLinesOptions) ([]string, error) {
	var maxLines *int
	if options != nil {
		maxLines = options.MaxLines
	}
	if maxLines != nil && *maxLines <= 0 {
		return []string{}, nil
	}
	reader, err := env.OpenTextLineReader(ctx, path)
	if err != nil {
		return nil, err
	}
	defer reader.Close(ctx)
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
	resolved := resolvePath(env.cwd, path)
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

// WriteFile creates or overwrites path, creating parent directories.
func (env *NodeExecutionEnv) WriteFile(ctx context.Context, path string, content []byte) error {
	return env.writeWithParents(ctx, path, content, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
}

// AppendFile creates or appends to path, creating parent directories.
func (env *NodeExecutionEnv) AppendFile(ctx context.Context, path string, content []byte) error {
	if err := env.writeWithParents(ctx, path, content, os.O_WRONLY|os.O_CREATE|os.O_APPEND); err != nil {
		return err
	}
	return abortedFileError(ctx, resolvePath(env.cwd, path))
}

func (env *NodeExecutionEnv) writeWithParents(ctx context.Context, path string, content []byte, flags int) error {
	resolved := resolvePath(env.cwd, path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o777); err != nil {
		return toFileError(err, fsCall{syscall: "mkdir", path: resolved})
	}
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	file, err := os.OpenFile(resolved, flags, 0o666)
	if err != nil {
		return toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return toFileError(err, fsCall{syscall: "write", path: resolved})
	}
	return nil
}

// RenameFile atomically renames sourcePath over destinationPath.
func (env *NodeExecutionEnv) RenameFile(ctx context.Context, sourcePath, destinationPath string) error {
	source := resolvePath(env.cwd, sourcePath)
	destination := resolvePath(env.cwd, destinationPath)
	if err := abortedFileError(ctx, destination); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return toFileError(err, fsCall{syscall: "rename", path: source, dest: destination})
	}
	return nil
}

// FileInfo returns metadata without following symlinks.
func (env *NodeExecutionEnv) FileInfo(ctx context.Context, path string) (harness.FileInfo, error) {
	resolved := resolvePath(env.cwd, path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return harness.FileInfo{}, err
	}
	stats, err := os.Lstat(resolved)
	if err != nil {
		return harness.FileInfo{}, toFileError(err, fsCall{syscall: "lstat", path: resolved})
	}
	return fileInfoFromStats(resolved, stats)
}

func fileInfoFromStats(path string, stats fs.FileInfo) (harness.FileInfo, error) {
	var kind harness.FileKind
	switch mode := stats.Mode(); {
	case mode.IsRegular():
		kind = harness.FileKindFile
	case mode.IsDir():
		kind = harness.FileKindDirectory
	case mode&fs.ModeSymlink != 0:
		kind = harness.FileKindSymlink
	default:
		return harness.FileInfo{}, &harness.FileError{Code: harness.FileErrorInvalid, Message: "Unsupported file type", Path: path}
	}
	return harness.FileInfo{
		Name:    filepath.Base(path),
		Path:    path,
		Kind:    kind,
		Size:    stats.Size(),
		MtimeMs: float64(stats.ModTime().UnixNano()) / 1e6,
	}, nil
}

// ListDir lists direct children in directory order without following
// symlinks; unsupported file types are omitted.
func (env *NodeExecutionEnv) ListDir(ctx context.Context, path string) ([]harness.FileInfo, error) {
	resolved := resolvePath(env.cwd, path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	names, err := readDirNames(resolved)
	if err != nil {
		return nil, toFileError(err, fsCall{syscall: "scandir", path: resolved})
	}
	infos := []harness.FileInfo{}
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
	resolved := resolvePath(env.cwd, path)
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
	_, err := env.FileInfo(ctx, path)
	if err == nil {
		return true, nil
	}
	var fileErr *harness.FileError
	if errors.As(err, &fileErr) && fileErr.Code == harness.FileErrorNotFound {
		return false, nil
	}
	return false, err
}

// CreateDir creates a directory; Recursive defaults to true.
func (env *NodeExecutionEnv) CreateDir(ctx context.Context, path string, options *harness.CreateDirOptions) error {
	resolved := resolvePath(env.cwd, path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	recursive := options == nil || options.Recursive == nil || *options.Recursive
	var err error
	if recursive {
		err = os.MkdirAll(resolved, 0o777)
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
func (env *NodeExecutionEnv) Remove(ctx context.Context, path string, options *harness.RemoveOptions) error {
	resolved := resolvePath(env.cwd, path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return err
	}
	if options == nil {
		options = &harness.RemoveOptions{}
	}
	stats, err := os.Lstat(resolved)
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist) && options.Force:
		return nil
	case err != nil:
		return toFileError(err, fsCall{syscall: "lstat", path: resolved})
	case stats.IsDir() && !options.Recursive:
		return &harness.FileError{Code: harness.FileErrorUnknown, Message: "Path is a directory: rm returned EISDIR (is a directory) " + resolved, Path: resolved}
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
func (env *NodeExecutionEnv) CreateTempFile(ctx context.Context, options *harness.CreateTempFileOptions) (string, error) {
	dir, err := env.CreateTempDir(ctx, nil)
	if err != nil {
		return "", err
	}
	if options == nil {
		options = &harness.CreateTempFileOptions{}
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

// Cleanup terminates every active command's process tree.
func (env *NodeExecutionEnv) Cleanup(context.Context) {
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
}

func (env *NodeExecutionEnv) trackChild(pid int, active bool) {
	env.mu.Lock()
	defer env.mu.Unlock()
	if active {
		env.activeChildPids[pid] = struct{}{}
	} else {
		delete(env.activeChildPids, pid)
	}
}
