//go:build windows

package atomicfile

import (
	"context"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

// createTemp creates the temporary file beside the target, with the owner-only DACL of ownerfile.CreateTemp, and for a file that does not exist yet
// gives it the mode asked for, as the replacement did before (Windows keeps only the read-only attribute of a mode). The access of a file that exists
// is carried onto the temporary file by carryAttributes before anything is written to it.
func createTemp(dir, pattern string, perm os.FileMode, existed bool) (*os.File, error) {
	file, err := ownerfile.CreateTemp(dir, pattern)
	if err != nil || existed {
		return file, err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	return file, nil
}

// carryIdentity does nothing on Windows: the owner and group go with the rest of the security descriptor, in carryAttributes and after the replacement.
func carryIdentity(*os.File, os.FileInfo) error { return nil }

var procReplaceFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replace puts temp in place of path, which already has the access (DACL entries, SE_DACL_PROTECTED) of the file at path, carried by carryAttributes. A file
// that exists is replaced with ReplaceFileW (see [replaceKeepingBackup] for the files a failure leaves, and for what is done after it succeeds): Microsoft
// documents that it keeps the replaced file's attributes and streams and merges its DACL into the replacement; RUN 43699097748 showed that it always leaves
// SE_DACL_AUTO_INHERITED set on the file at path and can change the entries (the inherited flag of an entry dropped, explicit copies of inherited entries
// added). So after it the descriptor of the replaced file is carried
// back (carryDescriptor) and read back, which puts the old file back when the access differs. A file that does not exist yet is moved with MoveFileEx and
// written through. Both are retried for fsretry.RenameBudget while another process holds the file open. On a file system without ReplaceFileW the target's
// descriptor is carried onto temp before the move. The target is never truncated in place.
func replace(temp, path string, existed bool) error {
	//portlint:allow ctxbackground a credential write has no caller context: the replace must not be abandoned half way, and fsretry bounds it by RenameBudget
	ctx := context.Background()
	if !existed {
		return fsretry.Retry(ctx, nil, func() error { return moveFile(temp, path) })
	}
	err := replaceKeepingBackup(path, temp, replaceSteps{
		replaceFile: func(replaced, replacement, backup string) error {
			return fsretry.Retry(ctx, nil, func() error { return replaceFile(replaced, replacement, backup) })
		},
		moveFile: func(from, to string) error {
			return fsretry.Retry(ctx, nil, func() error { return moveFile(from, to) })
		},
		carry: carryOnto(path),
	})
	if !errors.Is(err, windows.ERROR_INVALID_FUNCTION) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return err
	}
	// The replacement already has the access of the file (carryAttributes); this carries it again from the file as it is now, and tolerates only the
	// flag a file system may set whatever it is given.
	if err := carryDescriptor(path, temp, false); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
		return err
	}
	return fsretry.Retry(ctx, nil, func() error { return moveFile(temp, path) })
}

// carryOnto is the carry of a replacement of path: onto path itself it is made for the live file (see [carryDescriptor]), onto any other file for a
// temporary one.
func carryOnto(path string) func(from, to string) error {
	return func(from, to string) error { return carryDescriptor(from, to, to == path) }
}

// replaceFile is ReplaceFileW(replaced, replacement, backup, no flags): the replaced file is kept under the backup name.
func replaceFile(replaced, replacement, backup string) error {
	to, err := windows.UTF16PtrFromString(replaced)
	if err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(replacement)
	if err != nil {
		return err
	}
	kept, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	//nolint:gosec // G103: the pointers address UTF-16 buffers that live across the call.
	if result, _, callErr := procReplaceFile.Call(uintptr(unsafe.Pointer(to)), uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(kept)), 0, 0, 0); result == 0 {
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
