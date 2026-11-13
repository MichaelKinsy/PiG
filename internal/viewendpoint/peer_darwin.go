package viewendpoint

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the user of the process at the other end of c, as the kernel recorded it at connect
// (LOCAL_PEERCRED).
func peerUID(c net.Conn) (int, error) {
	conn, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a Unix socket connection")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Xucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return int(cred.Uid), nil
}
