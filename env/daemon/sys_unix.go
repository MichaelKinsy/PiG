//go:build !windows

package daemon

// Ports packages/env/daemon/src/sys/unix.rs

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const pathSeparator = "/"

// pathSeparators are the characters that separate path components.
const pathSeparators = "/"

// openReadOnly opens path for reading without blocking on FIFOs and, with noFollow, without following a final
// symbolic link.
func openReadOnly(path string, noFollow bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if noFollow {
		flags |= syscall.O_NOFOLLOW
	}
	return os.OpenFile(path, flags, 0)
}

// rustIOError is the text Rust's io::Error shows for a failure of the system: the system's message, capitalized as the C
// library writes it, and " (os error N)". It reports false for an error that names no system error.
func rustIOError(err error) (string, bool) {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		if !errors.Is(err, exec.ErrNotFound) {
			return "", false
		}
		errno = syscall.ENOENT
	}
	text := errno.Error()
	first, size := utf8.DecodeRuneInString(text)
	//portlint:allow utf16units Rust io::Error text, not a JS string: size is the UTF-8 byte length DecodeRuneInString returned for the first rune
	return fmt.Sprintf("%c%s (os error %d)", unicode.ToUpper(first), text[size:], int(errno)), true
}

// libuvNames are the codes the daemon names (sys/unix.rs:os_error_name); any other error is UNKNOWN.
var libuvNames = map[string]bool{
	"EPERM": true, "ENOENT": true, "EIO": true, "EBADF": true, "EAGAIN": true, "ENOMEM": true, "EACCES": true, "EBUSY": true,
	"EEXIST": true, "EXDEV": true, "ENOTDIR": true, "EISDIR": true, "EINVAL": true, "ENFILE": true, "EMFILE": true,
	"ETXTBSY": true, "EFBIG": true, "ENOSPC": true, "EROFS": true, "EMLINK": true, "ENAMETOOLONG": true, "ENOTEMPTY": true,
	"ELOOP": true, "ENOSYS": true, "ENOTSUP": true,
}

func libuvName(code string) string {
	if libuvNames[code] {
		return code
	}
	return "UNKNOWN"
}

// openDirectory is opendir(3): the directory is opened as one, so a file, however unreadable, is ENOTDIR before any
// permission check.
func openDirectory(path string) (*os.File, error) {
	for {
		descriptor, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		return os.NewFile(uintptr(descriptor), path), nil
	}
}

// isLinkRefusal reports whether an O_NOFOLLOW open failed because the final component is a symbolic link.
func isLinkRefusal(err error) bool {
	return errnoIs(err, syscall.ELOOP) || errnoIs(err, syscall.EMLINK)
}

// openForRead is Node's open(path, "r"): any kind of file, blocking.
func openForRead(path string) (*os.File, error) { return os.OpenFile(path, os.O_RDONLY, 0) }

// identity is the device and inode of a path's metadata, which identify a file across renames.
func identity(_ string, info os.FileInfo, _ bool) (dev, ino uint64) {
	if stats, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stats.Dev), uint64(stats.Ino) //nolint:unconvert // the field types differ by platform
	}
	return 0, 0
}

// tmpdir is Node's os.tmpdir(); Termux's Node falls back to $PREFIX/tmp.
func tmpdir() string {
	path := ""
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if value := os.Getenv(key); value != "" {
			path = value
			break
		}
	}
	if path == "" {
		path = "/tmp"
		if isAndroid() {
			prefix, ok := os.LookupEnv("PREFIX")
			if !ok {
				prefix = "/data/data/com.termux/files/usr"
			}
			path = prefix + "/tmp"
		}
	}
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = path[:len(path)-1]
	}
	return path
}

func home() string { return os.Getenv("HOME") }

// driveCwds is the per-drive working directories of Windows' `=C:` variables; there are none here.
func driveCwds() Object { return Object{} }

// rawName is the bytes of a file name, which libuv sorts directory listings by.
func rawName(name string) []int {
	raw := make([]int, len(name))
	for index := range len(name) {
		raw[index] = int(name[index])
	}
	return raw
}

const windowsOS = false

var childSignals sync.Once

// defaultChildSignals gives the programs the daemon starts every signal at its default, as the pre_exec hooks of `exec`
// and `pty` do (daemon/src/exec.rs, pty.rs). Go keeps a signal that was ignored when the daemon started (SIGHUP and
// SIGINT under nohup or in a background job) ignored in its children unless it handles the signal itself; handling it
// into a channel nobody reads keeps it ignored in the daemon, as in Pi's.
func defaultChildSignals() {
	childSignals.Do(func() {
		var ignored []os.Signal
		for number := syscall.Signal(1); number < 32; number++ {
			if number != syscall.SIGKILL && number != syscall.SIGSTOP && signal.Ignored(number) {
				ignored = append(ignored, number)
			}
		}
		if len(ignored) > 0 {
			signal.Notify(make(chan os.Signal, 1), ignored...)
		}
	})
}
