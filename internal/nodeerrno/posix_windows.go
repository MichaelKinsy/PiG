package nodeerrno

import "syscall"

// platformPosixCodes names the errnos, among libuv's codes, that only some
// platforms define; Go invents these on Windows too.
var platformPosixCodes = map[syscall.Errno]string{
	syscall.ENODATA:   "ENODATA",
	syscall.ENONET:    "ENONET",
	syscall.EREMOTEIO: "EREMOTEIO",
	syscall.EUNATCH:   "EUNATCH",
}
