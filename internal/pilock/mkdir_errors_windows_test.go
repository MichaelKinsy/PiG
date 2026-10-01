package pilock

import "syscall"

// Windows mkdir failures that are not EEXIST: ERROR_ACCESS_DENIED (a lock directory pending deletion, Node EPERM), ERROR_SHARING_VIOLATION (Node EBUSY) and ERROR_DIR_NOT_EMPTY (Node ENOTEMPTY).
var nonEEXISTMkdirErrors = map[string]error{
	"ERROR_ACCESS_DENIED":     syscall.Errno(5),
	"ERROR_SHARING_VIOLATION": syscall.Errno(32),
	"ERROR_DIR_NOT_EMPTY":     syscall.Errno(145),
}

// libuv src/win/error.c maps both to UV_EEXIST.
var eexistMkdirErrors = map[string]error{
	"ERROR_ALREADY_EXISTS": syscall.ERROR_ALREADY_EXISTS,
	"ERROR_FILE_EXISTS":    syscall.ERROR_FILE_EXISTS,
}
