//go:build windows

package atomicfile

import (
	"context"
	"os"

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

// carryIdentity does nothing on Windows: the owner and group go with the rest of the security descriptor, in carryAttributes and before the move.
func carryIdentity(*os.File, os.FileInfo) error { return nil }

// replace puts temp in place of path in a single MoveFileExW call with MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH, retried for
// fsretry.RenameBudget while another process holds the target open without delete sharing. ReplaceFileW is not used because it renames the old file
// away to a backup before moving the replacement in, leaving path momentarily absent to concurrent stat, open and realpath callers.
//
// For an existing file, carryAttributes already carried the target's security descriptor and user-settable file attributes onto temp before data was
// written; carryDescriptor is run again from the file as it is now before the move, tolerating only a difference in SE_DACL_AUTO_INHERITED. The target
// is never truncated in place.
func replace(temp, path string, existed bool) error {
	//portlint:allow ctxbackground a credential write has no caller context: the replace must not be abandoned half way, and fsretry bounds it by RenameBudget
	ctx := context.Background()
	return replaceWithMove(ctx, temp, path, existed, windowsMoveSteps{
		carry: carryDescriptor,
		move:  moveFileWithFlags,
		retry: func(ctx context.Context, op func() error) error {
			return fsretry.Retry(ctx, nil, op)
		},
	})
}

// moveFile is MoveFileEx(from, to, MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH).
func moveFile(from, to string) error {
	return moveFileWithFlags(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func moveFileWithFlags(from, to string, flags uint32) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(source, target, flags); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}
