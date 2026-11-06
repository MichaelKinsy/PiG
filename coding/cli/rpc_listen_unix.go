//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// listenPrivateUnixSocket binds the --listen socket so that it is 0600 from the moment it is reachable by path, and without a process-wide umask change. net.Listen creates the socket file with the process umask, so the bind happens inside a fresh directory of mode 0700 (nobody else can traverse it, whatever the socket's own mode is), the socket is chmod'ed to 0600, and then it is hard-linked to path, which fails with EEXIST rather than replacing a path that appeared meanwhile. The directory name is short, because a socket path is limited to 104 bytes on macOS and 108 on Linux. pig additive (D232).
func listenPrivateUnixSocket(path string) (net.Listener, error) {
	// A socket address holds len(Path) bytes with the terminating NUL; a longer path that exists as a link would be a server nobody can connect to.
	limit := len(syscall.RawSockaddrUnix{}.Path) - 1
	if len(path) > limit {
		return nil, fmt.Errorf("the path is %d bytes, longer than the %d a socket address holds", len(path), limit)
	}
	dir, err := makePrivateBindDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bound := filepath.Join(dir, "s")
	if len(bound) > limit {
		return nil, fmt.Errorf("the private bind path %s is %d bytes, longer than the %d a socket address holds; use a shorter socket path", bound, len(bound), limit)
	}
	listener, err := net.Listen("unix", bound)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(bound, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	if err := os.Link(bound, path); err != nil {
		_ = listener.Close()
		if linkErr, ok := errors.AsType[*os.LinkError](err); ok {
			err = linkErr.Err
		}
		return nil, err
	}
	// The bind name is removed with its directory; the server removes path itself at exit (removeOwnSocket), so the listener must not unlink either name when it closes.
	if unix, ok := listener.(*net.UnixListener); ok {
		unix.SetUnlinkOnClose(false)
	}
	return listener, nil
}

// makePrivateBindDir creates a directory of mode 0700 with a short random name (five base-36 digits) below parent.
func makePrivateBindDir(parent string) (string, error) {
	for range 100 {
		dir := filepath.Join(parent, ".l"+strconv.FormatUint(rand.Uint64N(36*36*36*36*36), 36))
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("no unused private directory name below %s", parent)
}
