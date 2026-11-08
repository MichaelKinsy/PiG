//go:build windows

package workers

import (
	"context"
	"errors"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/chord/winpipe"
)

// platformLockLocation is `processLock`'s choice on Windows: a named pipe.
func platformLockLocation(key string) (lockLocation, error) {
	return lockLocation{kind: lockPipe, address: `\\.\pipe\chord-lock-` + key}, nil
}

// platformListenLock listens on the pipe (first instance only: a second owner's create fails with ERROR_ACCESS_DENIED),
// or, for a socket file lock, on a Unix socket file (Windows 10 and later).
func platformListenLock(location lockLocation) (net.Listener, error) {
	if location.kind == lockFile {
		return net.Listen("unix", location.address)
	}
	return winpipe.Listen(location.address, &winio.PipeConfig{})
}

func platformDialLock(location lockLocation, ctx context.Context) (net.Conn, error) {
	if location.kind == lockFile {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", location.address)
	}
	return winio.DialPipeContext(ctx, location.address)
}

func lockInUse(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_PIPE_BUSY)
}

// lockRefused: nobody accepts on the path (a socket file nobody listens on, or a regular file there).
func lockRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, windows.WSAENOTSOCK)
}
