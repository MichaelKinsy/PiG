package pilock

import (
	"errors"
	"syscall"
)

// isEEXIST reports Node's EEXIST code: libuv src/win/error.c uv_translate_sys_error maps only ERROR_ALREADY_EXISTS and ERROR_FILE_EXISTS to UV_EEXIST. fs.ErrExist also matches ERROR_DIR_NOT_EMPTY, which libuv maps to UV_ENOTEMPTY.
func isEEXIST(err error) bool {
	return errors.Is(err, syscall.ERROR_ALREADY_EXISTS) || errors.Is(err, syscall.ERROR_FILE_EXISTS)
}
