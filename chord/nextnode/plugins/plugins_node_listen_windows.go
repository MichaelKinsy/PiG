//go:build windows

package plugins

import (
	"crypto/rand"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// listenNode listens for the connections of a Node subprocess (the channel's, and later the one of its worker thread, when the
// host needs synchronous callbacks) and returns the address it connects to. Node's net module treats every path on Windows as a
// named pipe and cannot reach an AF_UNIX socket, so the address is a named pipe with an unguessable name that only the current
// user can open; the listener accepts each connection on a pipe instance of its own.
func listenNode() (listener net.Listener, address string, cleanup func(), err error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", nil, fmt.Errorf("read the current user: %w", err)
	}
	address = `\\.\pipe\chord-plugins-` + rand.Text()
	listener, err = winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		return nil, "", nil, err
	}
	return listener, address, func() {}, nil
}
