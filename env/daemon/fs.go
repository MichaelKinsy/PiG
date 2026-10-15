package daemon

// Ports packages/env/daemon/src/fs.rs

import (
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// pig divergence (D96): the Go daemon takes its target system from the build instead of Rust's cfg!.
// targetOS is the system the daemon is built for: GOOS, or `android` for the static linux/amd64 build that
// make build-env-daemons packages for Android on x86-64, where Go links GOOS=android only with cgo. It sets this
// variable with -ldflags -X, so that build reports and follows Android as Pi's daemon built for Android does.
var targetOS = runtime.GOOS

// isAndroid is Rust's cfg!(target_os = "android").
func isAndroid() bool { return targetOS == "android" }

func errnoIs(err error, errno syscall.Errno) bool { return errors.Is(err, errno) }

// baseName is Rust's Path::file_name: the last normal component, or empty.
func baseName(path string) string {
	separators := "/"
	if windowsOS {
		separators = `/\`
	}
	parts := strings.FieldsFunc(path, func(r rune) bool { return strings.ContainsRune(separators, r) })
	for len(parts) > 0 && parts[len(parts)-1] == "." {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 || parts[len(parts)-1] == ".." {
		return ""
	}
	return parts[len(parts)-1]
}

func fileKind(mode fs.FileMode) string {
	switch {
	case mode.IsRegular():
		return "file"
	case mode.IsDir():
		return "directory"
	case mode&fs.ModeSymlink != 0:
		return "symlink"
	}
	return "other"
}

// info is `{ name, kind, size, mtimeSec, mtimeNsec, dev, ino }` of lstat or fstat metadata; follow says whether the
// metadata followed a final symbolic link.
func info(path string, stats fs.FileInfo, follow bool) Object {
	modified := stats.ModTime()
	dev, ino := identity(path, stats, follow)
	return Object{
		"name":      baseName(path),
		"kind":      fileKind(stats.Mode()),
		"size":      stats.Size(),
		"mtimeSec":  modified.Unix(),
		"mtimeNsec": int64(modified.Nanosecond()),
		"dev":       dev,
		"ino":       ino,
	}
}

func lstat(path string) (Object, *Failure) {
	stats, err := os.Lstat(path)
	if err != nil {
		return nil, ioFailure(err, "lstat", path)
	}
	return info(path, stats, false), nil
}

func realpath(path string) (Object, *Failure) {
	resolved, err := realPath(path)
	if err != nil {
		return nil, ioFailure(err, "realpath", path)
	}
	return Object{"path": resolved}, nil
}

// mkdirRecursive is Node's recursive mkdir: an existing directory is fine, an existing file is EEXIST, a file in the
// way of a parent is ENOTDIR.
func mkdirRecursive(path string) error { return nodeerrno.MkdirAll(path, 0o777) }

func mkdir(path string, recursive bool) (Object, *Failure) {
	var err error
	if recursive {
		err = mkdirRecursive(path)
	} else {
		err = os.Mkdir(path, 0o777)
	}
	if err != nil {
		return nil, ioFailure(err, "mkdir", path)
	}
	return Object{}, nil
}

// openWrite opens a file in one of Node's write modes. Windows reports a directory in the way as access denied,
// where libuv reports EISDIR.
func openWrite(path string, flags int) (*os.File, error) {
	file, err := os.OpenFile(path, flags, 0o666)
	if err != nil && windowsOS && errorCode(err) != "EISDIR" {
		if stats, statErr := os.Stat(path); statErr == nil && stats.IsDir() {
			return nil, synthetic("EISDIR")
		}
	}
	return file, err
}

// writeFile is writeFile/appendFile: create missing parents like Node's recursive mkdir, then write. It returns the
// open file for further chunks.
func writeFile(path string, appendMode, parents bool, content []byte) (*os.File, *Failure) {
	if parent := parentOf(path); parents && parent != "" {
		if err := mkdirRecursive(parent); err != nil {
			return nil, ioFailure(err, "mkdir", parent)
		}
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if appendMode {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	file, err := openWrite(path, flags)
	if err != nil {
		return nil, ioFailure(err, "open", path)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return nil, ioFailure(err, "write", path)
	}
	return file, nil
}

// parentOf is Rust's Path::parent with an empty result for none.
func parentOf(path string) string {
	trimmed := strings.TrimRight(path, pathSeparators)
	if trimmed == "" {
		return ""
	}
	index := strings.LastIndexAny(trimmed, pathSeparators)
	if index < 0 {
		return ""
	}
	parent := strings.TrimRight(trimmed[:index], pathSeparators)
	if parent == "" {
		return trimmed[:index+1]
	}
	return parent
}

func truncate(path string, size int64) (Object, *Failure) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, ioFailure(err, "open", path)
	}
	defer func() { _ = file.Close() }()
	if err := file.Truncate(size); err != nil {
		return nil, ioFailure(err, "ftruncate", path)
	}
	return Object{}, nil
}

func fsync(path string) (Object, *Failure) {
	// POSIX refuses to open a directory for writing; check first so Windows reports the same.
	if stats, err := os.Stat(path); err == nil && stats.IsDir() {
		return nil, newFailure("EISDIR", "EISDIR: illegal operation on a directory, open '"+path+"'").withPath(path)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, ioFailure(err, "open", path)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return nil, ioFailure(err, "fsync", path)
	}
	return Object{}, nil
}

func rename(from, to string) (Object, *Failure) {
	if err := os.Rename(from, to); err != nil {
		return nil, ioFailureBetween(err, "rename", from, to)
	}
	return Object{}, nil
}

// rm is Node's fs.rm: missing paths fail unless force; a directory without recursive fails with ERR_FS_EISDIR.
func rm(path string, recursive, force bool) (Object, *Failure) {
	stats, err := os.Lstat(path)
	if err != nil {
		if force && errors.Is(err, fs.ErrNotExist) {
			return Object{}, nil
		}
		return nil, ioFailure(err, "lstat", path)
	}
	if stats.IsDir() {
		if !recursive {
			return nil, newFailure("ERR_FS_EISDIR", "Path is a directory: rm returned EISDIR (is a directory) "+path).withPath(path)
		}
		err = os.RemoveAll(path)
	} else {
		err = removeFile(path)
	}
	if err != nil {
		return nil, ioFailure(err, "rm", path)
	}
	return Object{}, nil
}

// removeFile removes a file or a symbolic link; on Windows a read-only file is retried after making it writable.
func removeFile(path string) error {
	err := os.Remove(path)
	if err != nil && windowsOS && errors.Is(err, fs.ErrPermission) {
		if chmodErr := os.Chmod(path, 0o666); chmodErr != nil {
			return chmodErr
		}
		return os.Remove(path)
	}
	return err
}

const tempNameAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// mkdtemp makes a new directory named prefix plus six random characters, as libuv's mkdtemp creates it.
func mkdtemp(prefix string) (Object, *Failure) {
	for range 1000 {
		var random [6]byte
		_, _ = rand.Read(random[:])
		suffix := make([]byte, 6)
		for index, value := range random {
			suffix[index] = tempNameAlphabet[int(value)%len(tempNameAlphabet)]
		}
		path := prefix + string(suffix)
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return Object{"path": path}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, ioFailure(err, "mkdtemp", prefix+"XXXXXX")
		}
	}
	return nil, ioFailure(synthetic("EEXIST"), "mkdtemp", prefix+"XXXXXX")
}

// openReader opens a regular file for positional reads; directories fail with EISDIR, other non-regular files with
// NOT_REGULAR, and with noFollow a final symbolic link with SYMLINK.
func openReader(path string, noFollow bool) (*os.File, Object, *Failure) {
	if windowsOS && noFollow {
		if stats, err := os.Lstat(path); err == nil && stats.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, newFailure("SYMLINK", "Refusing to follow a symbolic link").withPath(path)
		}
	}
	file, err := openReadOnly(path, noFollow)
	if err != nil {
		if noFollow && isLinkRefusal(err) {
			return nil, nil, newFailure("SYMLINK", "Refusing to follow a symbolic link").withPath(path)
		}
		return nil, nil, ioFailure(err, "open", path)
	}
	stats, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, ioFailure(err, "fstat", path)
	}
	switch {
	case stats.IsDir():
		_ = file.Close()
		return nil, nil, newFailure("EISDIR", "EISDIR: illegal operation on a directory, read").withPath(path)
	case !stats.Mode().IsRegular():
		_ = file.Close()
		return nil, nil, newFailure("NOT_REGULAR", "Not a regular file").withPath(path)
	}
	return file, info(path, stats, true), nil
}

// pread reads up to length bytes at offset; fewer only at the end of the file.
func pread(file *os.File, path string, offset int64, length int) ([]byte, *Failure) {
	buffer := make([]byte, length)
	filled := 0
	for filled < length {
		read, err := file.ReadAt(buffer[filled:], offset+int64(filled))
		filled += read
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ioFailure(err, "read", path)
		}
	}
	return buffer[:filled], nil
}

// read reads up to length bytes from the current position: one read call, as Node's readFile makes for files of
// unknown size (FIFOs, devices).
func read(file *os.File, path string, length int) ([]byte, *Failure) {
	buffer := make([]byte, length)
	for {
		read, err := file.Read(buffer)
		if err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return nil, ioFailure(err, "read", path)
		}
		return buffer[:read], nil
	}
}

// dirEntry is one directory entry as Node lists it: the name decoded as UTF-8 (invalid bytes replaced), and lstat of
// the directory joined with that decoded name, so a name that is not valid UTF-8 reports ENOENT as in Node. raw
// carries the original bytes of such a name, which libuv sorts by.
func dirEntry(dir, fileName string) Object {
	name := decodeUTF8(fileName)
	entryPath := joinPath(dir, name)
	entry := Object{"name": name}
	if stats, err := os.Lstat(entryPath); err != nil {
		entry["error"] = ioFailure(err, "lstat", entryPath).toJSON()
	} else {
		entry["info"] = info(entryPath, stats, false)
	}
	if raw := rawName(fileName); raw != nil && fileName != name {
		entry["raw"] = raw
	}
	return entry
}

func joinPath(dir, name string) string {
	if strings.HasSuffix(dir, pathSeparator) || (windowsOS && strings.HasSuffix(dir, "/")) {
		return dir + name
	}
	return dir + pathSeparator + name
}

// decodeUTF8 decodes bytes as WHATWG UTF-8, as Node decodes names.
func decodeUTF8(name string) string {
	decoder := durableenv.NewRangeDecoder()
	return decoder.Decode([]byte(name)) + decoder.Flush()
}

// realPath resolves every symbolic link of an existing path.
func realPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
