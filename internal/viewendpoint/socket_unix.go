//go:build linux || darwin

package viewendpoint

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Supported reports whether this system serves a view endpoint.
const Supported = true

// sweepNames is the directory entries one start looks at for the sockets of processes that ended without removing
// theirs.
const sweepNames = 256

// ours reports whether info is a file of kind that uid owns and nobody else may use.
func ours(info os.FileInfo, kind os.FileMode, uid int) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().Type() == kind && int(st.Uid) == uid && info.Mode().Perm()&0o077 == 0
}

// listen creates dir when it is missing and binds <pid>.sock in it. The directory and a socket that is already there
// are used or removed only when they are this user's own: a directory (not a link to one) of mode 0700, a socket of
// mode 0600. own is the socket as it was created, for removeOwn.
func listen(dir string, uid int) (listener net.Listener, path string, own os.FileInfo, err error) {
	// Mkdir does not follow a link at dir, and Lstat below does not either.
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", nil, err
	}
	held, err := os.Lstat(dir) //perf:ok cold: one stat per bound extension host
	if err != nil {
		return nil, "", nil, err
	}
	if !ours(held, os.ModeDir, uid) {
		return nil, "", nil, fmt.Errorf("%s is not a directory of mode 0700 that this user owns; no view endpoint is served", dir)
	}
	sweep(dir, uid)
	path = filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	if limit := len(syscall.RawSockaddrUnix{}.Path) - 1; len(path) > limit {
		return nil, "", nil, fmt.Errorf("the socket path %s is %d bytes, longer than the %d a socket address holds; no view endpoint is served", path, len(path), limit)
	}
	// A process that had this pid before ended without removing its socket.
	if !removeStale(path, uid) {
		return nil, "", nil, fmt.Errorf("%s exists and is served by another process or is not a socket of mode 0600 that this user owns; no view endpoint is served", path)
	}
	// bind fails on a path that exists, a link included, so it creates the socket or nothing. Until the chmod the
	// socket has the mode the umask leaves it, inside a directory nobody else can enter.
	listener, err = net.Listen("unix", path)
	if err != nil {
		return nil, "", nil, err
	}
	// The endpoint removes the path itself (removeOwn), so closing the listener must not unlink whatever the path
	// names by then.
	if unix, ok := listener.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	fail := func(err error) (net.Listener, string, os.FileInfo, error) {
		_ = listener.Close()
		removeOwn(path, own)
		return nil, "", nil, err
	}
	if own, err = os.Lstat(path); err != nil { //perf:ok cold: one stat per bound extension host
		_ = listener.Close()
		return nil, "", nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fail(err)
	}
	// The directory is still the one that was checked, and the socket in it is the one just bound.
	now, err := os.Lstat(dir) //perf:ok cold: one stat per bound extension host
	if err != nil || !os.SameFile(held, now) || !ours(now, os.ModeDir, uid) {
		return fail(fmt.Errorf("%s changed while the view endpoint was created; no view endpoint is served", dir))
	}
	bound, err := os.Lstat(path) //perf:ok cold: one stat per bound extension host
	if err != nil || !os.SameFile(own, bound) || !ours(bound, os.ModeSocket, uid) {
		return fail(fmt.Errorf("%s changed while the view endpoint was created; no view endpoint is served", path))
	}
	return listener, path, own, nil
}

// removeStale removes path when it is this user's own socket and nobody serves it, and reports whether path is free.
// The refused connect is the proof that no process serves the socket: a pid proves nothing about a process in another
// pid namespace that shares the directory.
func removeStale(path string, uid int) bool {
	info, err := os.Lstat(path) //perf:ok cold: one stat per bound extension host
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || !ours(info, os.ModeSocket, uid) {
		return false
	}
	c, err := net.Dial("unix", path)
	if err == nil {
		_ = c.Close()
		return false
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return false
	}
	return os.Remove(path) == nil
}

// sweep removes the sockets in dir that are named for a process that no longer exists and that nobody serves.
func sweep(dir string, uid int) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	names, _ := d.Readdirnames(sweepNames)
	_ = d.Close()
	for _, name := range names {
		digits, isSocket := strings.CutSuffix(name, ".sock")
		pid, err := strconv.Atoi(digits)
		if !isSocket || err != nil || pid <= 0 || pid == os.Getpid() {
			continue
		}
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			removeStale(filepath.Join(dir, name), uid)
		}
	}
}

// removeOwn removes path while it still names the socket this endpoint created.
func removeOwn(path string, own os.FileInfo) {
	if own == nil {
		return
	}
	if now, err := os.Lstat(path); err == nil && os.SameFile(own, now) { //perf:ok cold: one stat per unbound extension host
		_ = os.Remove(path)
	}
}
