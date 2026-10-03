package nodeerrno

import "syscall"

// platformPosixCodes names the errnos, among libuv's codes, that only some
// platforms define.
var platformPosixCodes = map[syscall.Errno]string{
	syscall.EFTYPE:  "EFTYPE",
	syscall.ENODATA: "ENODATA",
}
