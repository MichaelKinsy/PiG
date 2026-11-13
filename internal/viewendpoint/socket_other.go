//go:build !linux && !darwin

package viewendpoint

import (
	"errors"
	"net"
	"os"
	"runtime"
)

// Supported reports whether this system serves a view endpoint. pig additive (D614): only where the kernel names the
// peer's user and a socket file keeps an owner and a mode. Windows keeps no mode on a socket file, so viewEndpoint:
// socket is refused there: the session runs and no socket is served.
const Supported = false

func listen(string, int) (net.Listener, string, os.FileInfo, error) {
	return nil, "", nil, errors.New("the Piglet key viewEndpoint: socket is not available on " + runtime.GOOS + "; no view endpoint is served")
}

func removeOwn(string, os.FileInfo) {}

func peerUID(net.Conn) (int, error) { return 0, errors.New("unsupported") }
