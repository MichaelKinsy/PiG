package viewendpoint

import (
	"errors"
	"net"
	"syscall"
)

// peerUID returns the user of the process at the other end of c, as the kernel recorded it at connect (SO_PEERCRED).
func peerUID(c net.Conn) (int, error) {
	unix, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a Unix socket connection")
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return int(cred.Uid), nil
}
