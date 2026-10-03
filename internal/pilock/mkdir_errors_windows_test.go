package pilock

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// Windows mkdir failures that are not EEXIST: ERROR_ACCESS_DENIED (a lock directory pending deletion, Node EPERM), ERROR_SHARING_VIOLATION (Node EBUSY) and ERROR_DIR_NOT_EMPTY (Node ENOTEMPTY).
var nonEEXISTMkdirErrors = map[string]error{
	"ERROR_ACCESS_DENIED":     syscall.Errno(5),
	"ERROR_SHARING_VIOLATION": syscall.Errno(32),
	"ERROR_DIR_NOT_EMPTY":     syscall.Errno(145),
}

// The Node message prefix ("<code>: <uv_strerror>") for each nonEEXISTMkdirErrors entry, from libuv's uv_translate_sys_error.
var nonEEXISTMkdirMessages = map[string]string{
	"ERROR_ACCESS_DENIED":     "EPERM: operation not permitted",
	"ERROR_SHARING_VIOLATION": "EBUSY: resource busy or locked",
	"ERROR_DIR_NOT_EMPTY":     "ENOTEMPTY: directory not empty",
}

// libuv src/win/error.c maps both to UV_EEXIST.
var eexistMkdirErrors = map[string]error{
	"ERROR_ALREADY_EXISTS": syscall.ERROR_ALREADY_EXISTS,
	"ERROR_FILE_EXISTS":    syscall.ERROR_FILE_EXISTS,
}

// libuv translates a Windows error it has no code for to UV_UNKNOWN (src/win/error.c uv_translate_sys_error), and Node reports it as "UNKNOWN: unknown error, <syscall> '<path>'". mkdir under a share this machine does not have fails that way (ERROR_BAD_NET_NAME), and proper-lockfile passes it through (lib/lockfile.js:47-48). PiG reports the same message, never Go's text, and errors.Is still matches the Windows error. Each row runs Pi's pinned proper-lockfile on the same path first.
func TestAcquireSurfacesAnErrorLibuvCannotNameAsUnknownUpstream(t *testing.T) {
	path := fmt.Sprintf(`\\localhost\pig-no-such-share-%x\auth.json`, rand.Uint64())
	for i, api := range []string{"sync", "async"} {
		t.Run(api, func(t *testing.T) {
			pi := runProperLockfile(t, path, api)
			message, ok := strings.CutPrefix(pi, "code=UNKNOWN message=")
			if !ok {
				t.Fatalf("proper-lockfile %s under a missing share: %q, want UNKNOWN", api, pi)
			}
			lock, err := linkAcquirers[i].run(path)
			if lock != nil {
				_ = lock.Release()
				t.Fatal("acquired a lock under a missing share")
			}
			if !errors.Is(err, windows.ERROR_BAD_NET_NAME) || nodeerrno.ErrorCode(err) != "" || errors.Is(err, ErrLocked) {
				t.Fatalf("err = %v, want mkdir's ERROR_BAD_NET_NAME, which libuv has no code for", err)
			}
			if err.Error() != message {
				t.Fatalf("message = %q, want proper-lockfile's %q", err.Error(), message)
			}
		})
	}
}
