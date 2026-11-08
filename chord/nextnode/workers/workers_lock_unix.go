//go:build unix

package workers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

// platformLockLocation is `processLock`'s choice of where the lock lives: Linux and Android an abstract socket (Node's
// `\0chord-lock-<key>`, written @ by Go), elsewhere a socket file.
func platformLockLocation(key string) (lockLocation, error) {
	if runtime.GOOS == "linux" || runtime.GOOS == "android" {
		return lockLocation{kind: lockAbstract, address: "@chord-lock-" + key}, nil
	}
	address, err := socketPath("chord-lock-" + key + ".sock")
	if err != nil {
		return lockLocation{}, err
	}
	return lockLocation{kind: lockFile, address: address}, nil
}

// maxSocketPathBytes: sun_path holds 104 bytes on macOS and the BSDs, the terminating NUL included.
const maxSocketPathBytes = 103

// socketPath is a lock socket's path: in the temporary directory, or, when that does not fit in sun_path (a deep TMPDIR),
// in a private per-user directory under /tmp that only its owner can reach.
func socketPath(file string) (string, error) {
	preferred := filepath.Join(os.TempDir(), file)
	if len(preferred) <= maxSocketPathBytes {
		return preferred, nil
	}
	uid := os.Getuid()
	directory := "/tmp/chord-lock-" + strconv.Itoa(uid)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != uid || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("lock directory %s must be a directory only its owner can access", directory)
	}
	return filepath.Join(directory, file), nil
}

func platformListenLock(location lockLocation) (net.Listener, error) {
	return net.Listen("unix", location.address)
}

func platformDialLock(location lockLocation, ctx context.Context) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", location.address)
}

func lockInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }

// lockRefused: nobody accepts on the path: a socket file nobody listens on, or no socket there (a regular file: ENOTSOCK on
// macOS, ECONNREFUSED on Linux; retrying would spin).
func lockRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTSOCK)
}
