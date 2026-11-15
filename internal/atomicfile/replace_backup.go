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

// replaceSteps are the calls a replacement on Windows is made of, each already retried for as long as another process holds a file open; tests inject
// file systems that fail the way Microsoft documents ReplaceFileW to.
type replaceSteps struct {
	replaceFile func(replaced, replacement, backup string) error // ReplaceFileW with a backup name
	moveFile    func(from, to string) error                      // MoveFileEx, replacing an existing file
	copyDACL    func(from, to string) error                      // the DACL of from onto to
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
// failure leaves is known. Microsoft documents the states of a failed ReplaceFileW (learn.microsoft.com, ReplaceFileW, "Return value"):
//   - ERROR_UNABLE_TO_REMOVE_REPLACED (1175): the replaced file could not be deleted; both files keep their names;
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT (1176): the replacement could not be renamed; with a backup name both files keep their names (without one the
//     replaced file would be gone and the replacement left under its own name);
//   - ERROR_UNABLE_TO_MOVE_REPLACEMENT_2 (1177): the replacement could not be moved and keeps its name, with the streams and attributes of the replaced
//     file; the replaced file exists under the backup name and path does not exist;
//   - any other error: both files keep their names.
//
// So the first three leave the file at path as it was, and the replacement is for the caller to delete. 1177 leaves no file at path: the replacement is
// moved into its place, which finishes what ReplaceFileW did, and when that cannot be done the backup is moved back, which reverses it. When neither can
// be done the error is a [keptFilesError] and neither file is deleted. After a success the backup, which is the old content, is deleted.
func replaceKeepingBackup(path, temp string, steps replaceSteps) error {
	backup := temp + ".bak"
	err := steps.replaceFile(path, temp, backup)
	if err == nil {
		_ = os.Remove(backup)
		return nil
	}
	if !errors.Is(err, errUnableToMoveReplacement2) {
		return err
	}
	// The access of the replaced file goes onto the replacement first, as the unsupported-file-system fallback does: ReplaceFileW did not say it merged the DACL.
	completeErr := steps.copyDACL(backup, temp)
	if completeErr == nil {
		completeErr = steps.moveFile(temp, path)
	}
	if completeErr == nil {
		_ = os.Remove(backup)
		return nil
	}
	if restoreErr := steps.moveFile(backup, path); restoreErr != nil {
		return &keptFilesError{err: errors.Join(err, completeErr, restoreErr), files: []string{temp, backup}}
	}
	return errors.Join(err, completeErr)
}
