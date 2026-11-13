//go:build !windows

package ai

import "syscall"

// socketCodeErrnos is, per Node socket error code of retry.ts's list, the errno Go reports for the same event on POSIX (linux, darwin):
// the constant of the same name, whose number differs by platform.
var socketCodeErrnos = map[string][]syscall.Errno{
	"ECONNRESET":   {syscall.ECONNRESET},
	"ECONNABORTED": {syscall.ECONNABORTED},
	"ECONNREFUSED": {syscall.ECONNREFUSED},
	"ETIMEDOUT":    {syscall.ETIMEDOUT},
	"EPIPE":        {syscall.EPIPE},
}
