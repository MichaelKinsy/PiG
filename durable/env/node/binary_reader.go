package node

// Ports packages/durable/src/env/node.ts (NodeBinaryReader, NodeDirReader and NodeExecutionEnv.openBinaryReader,
// openDirReader).

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// binaryReadChunk is the largest single read of a BinaryReader, so a huge length allocates only as much as the file
// yields.
const binaryReadChunk = 1024 * 1024

// scanChunkBytes is the read size of ScanLines.
const scanChunkBytes = 64 * 1024

func closedError(what, path string) *durableenv.FileError {
	return durableenv.NewFileError(durableenv.FileErrorInvalid, what+" is closed", path, nil)
}

func symlinkRefused(path string, cause error) *durableenv.FileError {
	return durableenv.NewFileError(durableenv.FileErrorInvalid, "Refusing to follow a symbolic link", path, cause)
}

// nodeBinaryReader is a BinaryReader over one open file. Reads run concurrently; Close waits for those in flight.
type nodeBinaryReader struct {
	mu     sync.RWMutex
	file   *os.File
	path   string
	closed bool
}

var _ durableenv.BinaryReader = (*nodeBinaryReader)(nil)

func (reader *nodeBinaryReader) Info(ctx context.Context) (durableenv.FileInfo, error) {
	if err := abortedFileError(ctx, reader.path); err != nil {
		return durableenv.FileInfo{}, err
	}
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	if reader.closed {
		return durableenv.FileInfo{}, closedError("Binary reader", reader.path)
	}
	stats, err := reader.file.Stat()
	if err != nil {
		return durableenv.FileInfo{}, toFileError(err, fsCall{syscall: "fstat", path: reader.path, fd: true})
	}
	return fileInfoFromStats(reader.path, stats)
}

func (reader *nodeBinaryReader) Read(ctx context.Context, offset, length int64) ([]byte, error) {
	if err := abortedFileError(ctx, reader.path); err != nil {
		return nil, err
	}
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	if reader.closed {
		return nil, closedError("Binary reader", reader.path)
	}
	if offset < 0 || offset > maxSafeInteger || length < 0 || length > maxSafeInteger {
		return nil, durableenv.NewFileError(durableenv.FileErrorInvalid, "Offset and length must be non-negative safe integers", reader.path, nil)
	}
	var result []byte
	for total := int64(0); total < length; {
		chunk := make([]byte, min(length-total, binaryReadChunk))
		bytesRead, err := readFileAt(reader.file, chunk, offset+total)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, toFileError(err, fsCall{syscall: "read", path: reader.path, fd: true})
		}
		if abortErr := abortedFileError(ctx, reader.path); abortErr != nil {
			return nil, abortErr
		}
		if bytesRead == 0 {
			break
		}
		// A read shorter than its chunk reached the end of the file.
		if total == 0 && (bytesRead < len(chunk) || int64(bytesRead) == length) {
			return chunk[:bytesRead], nil
		}
		result = append(result, chunk[:bytesRead]...)
		total += int64(bytesRead)
		if bytesRead < len(chunk) {
			break
		}
	}
	if result == nil {
		result = []byte{}
	}
	return result, nil
}

func (reader *nodeBinaryReader) ScanLines(ctx context.Context, options durableenv.ScanLinesOptions) (durableenv.LineScan, error) {
	if err := abortedFileError(ctx, reader.path); err != nil {
		return durableenv.LineScan{}, err
	}
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	if reader.closed {
		return durableenv.LineScan{}, closedError("Binary reader", reader.path)
	}
	scanner, err := durableenv.NewLineScanner(options.StartLine, options.EndLine)
	if err != nil {
		return durableenv.LineScan{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "Invalid line range", reader.path, nil)
	}
	chunk := make([]byte, scanChunkBytes)
	for position := int64(0); ; {
		bytesRead, err := readFileAt(reader.file, chunk, position)
		if err != nil && !errors.Is(err, io.EOF) {
			return durableenv.LineScan{}, toFileError(err, fsCall{syscall: "read", path: reader.path, fd: true})
		}
		if abortErr := abortedFileError(ctx, reader.path); abortErr != nil {
			return durableenv.LineScan{}, abortErr
		}
		if bytesRead == 0 {
			return scanner.Finish(), nil
		}
		scanner.Push(chunk[:bytesRead])
		position += int64(bytesRead)
	}
}

func (reader *nodeBinaryReader) Close(context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil
	}
	reader.closed = true
	closeQuietly(reader.file)
	return nil
}

// OpenBinaryReader opens a regular file for bounded positional reads. A directory fails with is_directory, other
// non-regular files with invalid; with NoFollow a symbolic link as the final path component fails with invalid.
func (env *NodeExecutionEnv) OpenBinaryReader(ctx context.Context, path string, options *durableenv.OpenBinaryReaderOptions) (durableenv.BinaryReader, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	noFollow := options != nil && options.NoFollow
	if isWindows && noFollow {
		// Windows has no O_NOFOLLOW; check the final component before opening (not race-free).
		stats, err := os.Lstat(resolved)
		if err != nil {
			return nil, toFileError(err, fsCall{syscall: "lstat", path: resolved})
		}
		if stats.Mode()&fs.ModeSymlink != 0 {
			return nil, symlinkRefused(resolved, nil)
		}
	}
	file, err := openReadOnly(resolved, noFollow)
	if err != nil {
		// O_NOFOLLOW reports a final-component symlink as ELOOP (EMLINK on some BSDs).
		if code := errnoCode(err); noFollow && (code == "ELOOP" || code == "EMLINK") {
			return nil, symlinkRefused(resolved, err)
		}
		return nil, toFileError(err, fsCall{syscall: "open", path: resolved})
	}
	stats, err := file.Stat()
	if err != nil {
		closeQuietly(file)
		return nil, toFileError(err, fsCall{syscall: "fstat", path: resolved, fd: true})
	}
	if !stats.Mode().IsRegular() {
		closeQuietly(file)
		if stats.IsDir() {
			return nil, durableenv.NewFileError(durableenv.FileErrorIsDirectory, "EISDIR: illegal operation on a directory, read", resolved, nil)
		}
		return nil, durableenv.NewFileError(durableenv.FileErrorInvalid, "Not a regular file", resolved, nil)
	}
	if abortErr := abortedFileError(ctx, resolved); abortErr != nil {
		closeQuietly(file)
		return nil, abortErr
	}
	return &nodeBinaryReader{file: file, path: resolved}, nil
}

// nodeDirReader is a DirReader over one open directory.
type nodeDirReader struct {
	mu     sync.Mutex
	dir    *os.File
	path   string
	done   bool
	closed bool
}

var _ durableenv.DirReader = (*nodeDirReader)(nil)

func (reader *nodeDirReader) Next(ctx context.Context, maxEntries int) (durableenv.DirPage, error) {
	if err := abortedFileError(ctx, reader.path); err != nil {
		return durableenv.DirPage{}, err
	}
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return durableenv.DirPage{}, closedError("Directory reader", reader.path)
	}
	if maxEntries <= 0 {
		return durableenv.DirPage{}, durableenv.NewFileError(durableenv.FileErrorInvalid, "maxEntries must be a positive safe integer", reader.path, nil)
	}
	entries := []durableenv.FileInfo{}
	for !reader.done && len(entries) < maxEntries {
		batch, err := reader.dir.ReadDir(maxEntries - len(entries))
		if errors.Is(err, io.EOF) {
			reader.done = true
			break
		}
		if err != nil {
			return durableenv.DirPage{}, toFileError(err, fsCall{syscall: "scandir", path: reader.path})
		}
		for _, entry := range batch {
			if err := abortedFileError(ctx, reader.path); err != nil {
				return durableenv.DirPage{}, err
			}
			entryPath := filepath.Join(reader.path, entry.Name())
			stats, err := os.Lstat(entryPath)
			if err != nil {
				// Removed between enumeration and lstat: not part of the listing any more.
				if errnoCode(err) == "ENOENT" {
					continue
				}
				return durableenv.DirPage{}, toFileError(err, fsCall{syscall: "lstat", path: entryPath})
			}
			if info, err := fileInfoFromStats(entryPath, stats); err == nil {
				entries = append(entries, info)
			}
		}
	}
	return durableenv.DirPage{Entries: entries, Done: reader.done}, nil
}

func (reader *nodeDirReader) Close(context.Context) error {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if reader.closed {
		return nil
	}
	reader.closed = true
	closeQuietly(reader.dir)
	return nil
}

// OpenDirReader opens a directory for paged listing; metadata is read only for returned entries.
func (env *NodeExecutionEnv) OpenDirReader(ctx context.Context, path string) (durableenv.DirReader, error) {
	resolved := resolvePath(env.Cwd(), path)
	if err := abortedFileError(ctx, resolved); err != nil {
		return nil, err
	}
	dir, err := os.Open(resolved)
	if err == nil {
		var stats fs.FileInfo
		if stats, err = dir.Stat(); err == nil && !stats.IsDir() {
			err = &fs.PathError{Op: "opendir", Path: resolved, Err: nodeErrorCode("ENOTDIR")}
		}
		if err != nil {
			closeQuietly(dir)
		}
	}
	if err != nil {
		return nil, toFileError(err, fsCall{syscall: "opendir", path: resolved})
	}
	if abortErr := abortedFileError(ctx, resolved); abortErr != nil {
		closeQuietly(dir)
		return nil, abortErr
	}
	return &nodeDirReader{dir: dir, path: resolved}, nil
}
