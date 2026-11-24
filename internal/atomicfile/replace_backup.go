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
// failure leaves is known.
func replaceKeepingBackup(path, temp string, steps replaceSteps) error {
	backup := temp + ".bak"
	err := steps.replaceFile(path, temp, backup)
	if err == nil {
		return settle(path, backup, steps)
	}
	if !errors.Is(err, errUnableToMoveReplacement2) {
		return err
	}
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
