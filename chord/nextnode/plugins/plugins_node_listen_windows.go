//go:build windows

package plugins

import (
	"crypto/rand"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/chord/winpipe"
)

// pipeBuffer is the size of the buffer of each direction of the pipe.
const pipeBuffer = 64 << 10

// RemoveSocketDirsOf has nothing to remove on Windows: a named pipe leaves no directory behind.
func RemoveSocketDirsOf(string, string) {}

// listenNode listens for the connections of a Node subprocess (the channel's, and later the one of its worker thread, when the
// host needs synchronous callbacks) and returns the address it connects to. Node's net module treats every path on Windows as a
// named pipe and cannot reach an AF_UNIX socket, so the address is a named pipe with an unguessable name that only the current
// user can open; the listener accepts each connection on a pipe instance of its own. The pipe buffers pipeBuffer bytes each way:
// a pipe with no buffer completes a write only when the peer reads it, so a process that wrote its last frame (a `fatal`) and
// exits would lose it while the reader is busy, as a unix socket does not.
func listenNode() (listener net.Listener, address string, cleanup func(), err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", nil, fmt.Errorf("read the current user: %w", err)
	}
	address = `\\.\pipe\chord-plugins-` + rand.Text()
	listener, err = winpipe.Listen(address, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
		InputBufferSize:    pipeBuffer,
		OutputBufferSize:   pipeBuffer,
	})
	if err != nil {
		return nil, "", nil, err
	}
	return listener, address, func() {}, nil
}
