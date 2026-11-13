package ai

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// socketCodeErrnos is, per Node socket error code of retry.ts's list, the errors Go reports for the same event on Windows: the Winsock
// (WSAE*) and system error numbers that libuv, and so Node, names with the code (internal/nodeerrno). They are not syscall.ECONNRESET
// and its siblings, values Go invents on Windows that no system call returns.
var socketCodeErrnos = map[string][]syscall.Errno{
	"ECONNRESET":   {windows.WSAECONNRESET, windows.ERROR_NETNAME_DELETED},
	"ECONNABORTED": {windows.WSAECONNABORTED, windows.ERROR_CONNECTION_ABORTED},
	"ECONNREFUSED": {windows.WSAECONNREFUSED, windows.ERROR_CONNECTION_REFUSED},
	"ETIMEDOUT":    {windows.WSAETIMEDOUT, windows.ERROR_SEM_TIMEOUT},
	"EPIPE":        {windows.WSAESHUTDOWN, windows.ERROR_BAD_PIPE, windows.ERROR_PIPE_NOT_CONNECTED},
}
