//go:build !darwin && !linux && !windows

package nodeerrno

import "syscall"

// platformPosixCodes names the errnos, among libuv's codes, that only some
// platforms define. This platform adds none.
var platformPosixCodes = map[syscall.Errno]string{}
