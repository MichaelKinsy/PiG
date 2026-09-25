//go:build !windows

package subprocess

import "net"

// ListenExtension listens for one extension process on the AF_UNIX socket at
// sockPath and returns the address the process finds in PIG_EXT_SOCKET. The
// second argument, whether the process runs under Node, matters only on
// Windows.
func ListenExtension(sockPath string, _ bool) (net.Listener, string, error) {
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, "", err
	}
	return ln, sockPath, nil
}
