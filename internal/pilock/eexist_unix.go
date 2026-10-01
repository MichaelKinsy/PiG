//go:build unix

package pilock

import (
	"errors"
	"syscall"
)

// isEEXIST reports Node's EEXIST code. fs.ErrExist also matches ENOTEMPTY, which Node reports as ENOTEMPTY.
func isEEXIST(err error) bool {
	return errors.Is(err, syscall.EEXIST)
}
