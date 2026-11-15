package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ReplaceFileW's partial failures (winerror.h). They are numbers so that the decision below is the same code on every platform and a test can inject them.
const (
	errUnableToMoveReplacement2 = syscall.Errno(1177) // ERROR_UNABLE_TO_MOVE_REPLACEMENT_2
)

// errAutoInheritedDiffers is what a carry of a security descriptor returns when the entries of the DACL and SE_DACL_PROTECTED, which decide who may open the
// file, are as the old file's but SE_DACL_AUTO_INHERITED is not: a file system may set that bookkeeping flag whatever the caller asked for. It is not a
// failure; a carry that returns any other error did not give the file the old access.
var errAutoInheritedDiffers = errors.New("the security descriptor has the access of the old file but not its SE_DACL_AUTO_INHERITED flag")

// replaceSteps are the calls a replacement on Windows is made of, each already retried for as long as another process holds a file open; tests inject
// file systems that fail the way Microsoft documents ReplaceFileW to.
type replaceSteps struct {
	replaceFile func(replaced, replacement, backup string) error // ReplaceFileW with a backup name
	moveFile    func(from, to string) error                      // MoveFileEx, replacing an existing file
	// carry gives the file at to the security descriptor of the file at from and reads it back; it returns nil when the descriptor is equal, errAutoInheritedDiffers
	// when only SE_DACL_AUTO_INHERITED differs, and any other error when the access (the entries and SE_DACL_PROTECTED) is not the old file's.
	carry func(from, to string) error
}

// keptFilesError is the error of a replacement that failed with the data of the file in files, which must not be deleted: the replaced file is under another
// name than its own, and the file that holds the new content could not take its place.
type keptFilesError struct {
	err   error
	files []string
}

func (e *keptFilesError) Error() string {
	return fmt.Sprintf("%v (the files %s hold the file's data and are kept)", e.err, strings.Join(e.files, ", "))
}

func (e *keptFilesError) Unwrap() error { return e.err }

// replaceKeepingBackup puts temp in place of path with ReplaceFileW, given a backup name (the temporary file's name with .bak) so that every state a
// failure leaves is known. The caller has already carried the access of path onto temp, so that between ReplaceFileW and the carry below the file at
// path has the entries and SE_DACL_PROTECTED of the file it replaced whichever of the two descriptors ReplaceFileW keeps. Microsoft documents the states
// of a failed ReplaceFileW (learn.microsoft.com, ReplaceFileW, "Return value"):
//   - ERROR_UNABLE_TO_REMOVE_REPLACED (1175): the replaced file could not be deleted; both files keep their names;
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176): the replacement could not be renamed; with a backup name both files keep their names (without one the
//     replaced file would be gone and the replacement left under its own name);
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177): the replacement could not be moved and keeps its name, with the streams and attributes of the replaced
//     file; the replaced file exists under the backup name and path does not exist;
//   - any other error: both files keep their names.
//
// So the first three leave the file at path as it was, and the replacement is for the caller to delete. 1177 leaves no file at path: the replacement is
// moved into its place, which finishes what ReplaceFileW did, and when that cannot be done the backup is moved back, which reverses it. When neither can
// be done the error is a [keptFilesError] and neither file is deleted.
//
// After the replacement (a success, or a 1177 that was finished) the descriptor of the old file, which the backup still is, is carried onto the file at
// path, which restores what ReplaceFileW or MoveFileEx may have changed (SE_DACL_AUTO_INHERITED). If the access (the entries and SE_DACL_PROTECTED) is
// still not the old file's, the old file is moved back from the backup and the error is returned, a [keptFilesError] when that fails too; a difference in
// SE_DACL_AUTO_INHERITED alone is not a failure. After a success the backup, which is the old content, is deleted.
func replaceKeepingBackup(path, temp string, steps replaceSteps) error {
	backup := temp + ".bak"
	err := steps.replaceFile(path, temp, backup)
	if err == nil {
		return settle(path, backup, steps)
	}
	if !errors.Is(err, errUnableToMoveReplacement2) {
		return err
	}
	// The access of the replaced file goes onto the replacement first, as the unsupported-file-system fallback does: ReplaceFileW did not say it merged the DACL.
	completeErr := steps.carry(backup, temp)
	if errors.Is(completeErr, errAutoInheritedDiffers) {
		completeErr = nil
	}
	if completeErr == nil {
		completeErr = steps.moveFile(temp, path)
	}
	if completeErr == nil {
		return settle(path, backup, steps)
	}
	if restoreErr := steps.moveFile(backup, path); restoreErr != nil {
		return &keptFilesError{err: errors.Join(err, completeErr, restoreErr), files: []string{temp, backup}}
	}
	return errors.Join(err, completeErr)
}

// settle is the end of a replacement that put the new file at path with the old file under the backup name: the old file's descriptor is carried onto the
// new one. A carry that leaves the access as the old file's, or only SE_DACL_AUTO_INHERITED different, is done and the backup is deleted. Any other result
// puts the old file back (which has its own descriptor, whole) and is the error; if that cannot be done the new file at path and the backup are kept.
func settle(path, backup string, steps replaceSteps) error {
	carryErr := steps.carry(backup, path)
	if carryErr == nil || errors.Is(carryErr, errAutoInheritedDiffers) {
		_ = os.Remove(backup)
		return nil
	}
	carryErr = fmt.Errorf("the replacement did not keep the access of the file it replaced: %w", carryErr)
	if restoreErr := steps.moveFile(backup, path); restoreErr != nil {
		return &keptFilesError{err: errors.Join(carryErr, restoreErr), files: []string{path, backup}}
	}
	return carryErr
}
