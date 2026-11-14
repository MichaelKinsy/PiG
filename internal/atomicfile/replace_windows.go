//go:build windows

package atomicfile

import (
	"context"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

// carryIdentity does nothing on Windows: ReplaceFileW merges the replaced file's security descriptor into the replacement.
func carryIdentity(*os.File, os.FileInfo) error { return nil }

var procReplaceFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replace puts temp in place of path. A file that exists is replaced with ReplaceFileW, which keeps the replaced file's DACL, attributes and streams
// (an administrator's widened DACL, or the owner-only DACL the store created, survive a rewrite, as they survive writeFileSync). A file that does not
// exist yet is moved with MoveFileEx and written through. Both are retried for fsretry.RenameBudget while another process holds the file open.
// On a file system without ReplaceFileW the target's DACL is copied onto temp before the move. The target is never truncated in place.
func replace(temp, path string, existed bool) error {
	//portlint:allow ctxbackground a credential write has no caller context: the replace must not be abandoned half way, and fsretry bounds it by RenameBudget
	ctx := context.Background()
	if !existed {
		return fsretry.Retry(ctx, nil, func() error { return moveFile(temp, path) })
	}
	err := fsretry.Retry(ctx, nil, func() error { return replaceFile(path, temp) })
	if !errors.Is(err, windows.ERROR_INVALID_FUNCTION) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return err
	}
	if err := copyDACL(path, temp); err != nil {
		return err
	}
	return fsretry.Retry(ctx, nil, func() error { return moveFile(temp, path) })
}

// replaceFile is ReplaceFileW(replaced, replacement, no backup, no flags).
func replaceFile(replaced, replacement string) error {
	to, err := windows.UTF16PtrFromString(replaced)
	if err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	//nolint:gosec // G103: the pointers address UTF-16 buffers that live across the call.
	if result, _, callErr := procReplaceFile.Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), 0, 0, 0, 0); result == 0 {
		return &os.LinkError{Op: "replace", Old: replacement, New: replaced, Err: callErr}
	}
	return nil
}

// moveFile is MoveFileEx(from, to, REPLACE_EXISTING|WRITE_THROUGH).
func moveFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// copyDACL gives the file at to the DACL of the file at from, with its protected flag.
func copyDACL(from, to string) error {
	descriptor, err := windows.GetNamedSecurityInfo(from, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
}
