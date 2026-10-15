package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"
)

func TestReadOnlyConfigRootError(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"EROFS":               {&fs.PathError{Op: "mkdir", Path: "/x", Err: syscall.EROFS}, true},
		"EACCES":              {&fs.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, true},
		"permission":          {fs.ErrPermission, true},
		"wrapped EROFS":       {fmt.Errorf("write Piglet verification cache: %w", &fs.PathError{Op: "rename", Path: "/x", Err: syscall.EROFS}), true},
		"not exist":           {&fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist}, false},
		"lock wait timed out": {context.DeadlineExceeded, false},
		"plain":               {errors.New("disk is full"), false},
		"nil":                 {nil, false},
	} {
		if got := isReadOnlyConfigRootError(tc.err); got != tc.want {
			t.Errorf("%s: isReadOnlyConfigRootError(%v) = %v, want %v", name, tc.err, got, tc.want)
		}
	}
}

// A read-only config root (a Nix or container image home, a locked-down CI cache) cannot hold the verification cache. Pi has no Binary to
// verify; on the same root Pi warns about its own settings lock (the "Invalid settings file" line). PiG adds no warning of its own there (D157):
// the next start checks the Binary in full again, and `pig diagnose` reports the read-only root. Every other write failure still warns.
func TestVerifyCacheWarningIsQuietOnlyOnAReadOnlyConfigRoot(t *testing.T) {
	for _, tc := range []struct {
		err  error
		warn bool
	}{
		{nil, false},
		{fmt.Errorf("write Piglet verification cache: %w", &fs.PathError{Op: "mkdir", Path: "/state", Err: syscall.EROFS}), false},
		{fmt.Errorf("write Piglet verification cache: %w", &fs.PathError{Op: "mkdir", Path: "/state", Err: syscall.EACCES}), false},
		{errors.New("write Piglet verification cache: disk is full"), true},
	} {
		var stderr bytes.Buffer
		reportVerifyCacheError(&stderr, tc.err)
		want := ""
		if tc.warn {
			want = "pig: warning: " + tc.err.Error() + "\npig: startup will continue; the next start verifies this Piglet Binary in full again\n"
		}
		if stderr.String() != want {
			t.Errorf("reportVerifyCacheError(%v) wrote %q, want %q", tc.err, stderr.String(), want)
		}
	}
}
