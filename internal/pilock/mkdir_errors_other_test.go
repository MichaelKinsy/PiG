//go:build !windows

package pilock

import "syscall"

// Unix mkdir failures that are not EEXIST. Go's fs.ErrExist also matches ENOTEMPTY, which Node reports as ENOTEMPTY and proper-lockfile passes through.
var nonEEXISTMkdirErrors = map[string]error{
	"EPERM":     syscall.EPERM,
	"EACCES":    syscall.EACCES,
	"EBUSY":     syscall.EBUSY,
	"ENOTEMPTY": syscall.ENOTEMPTY,
}

var eexistMkdirErrors = map[string]error{
	"EEXIST": syscall.EEXIST,
}
