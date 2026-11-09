//go:build unix

package workers

// Ports packages/chord/src/node/workers/lock.ts

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
	//portlint:allow utf16units lock.ts measures the same unit: Buffer.byteLength(preferred) <= MAX_SOCKET_PATH_BYTES, the sun_path limit is in bytes
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

// lockRefused reports that nobody accepts on the lock's address: connect(2) fails with ECONNREFUSED to a socket file nothing listens on, and to a plain file (a crashed holder's leftover) with ECONNREFUSED on Linux and ENOTSOCK on macOS and the BSDs. Any other error means the holder is gone or going, so the waiter takes the lock again.
func lockRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOTSOCK)
}
