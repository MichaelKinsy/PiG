//go:build !windows

package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

const (
	errUnableToRemoveReplaced  = syscall.Errno(1175) // ERROR_UNABLE_TO_REMOVE_REPLACED
	errUnableToMoveReplacement = syscall.Errno(1176) // ERROR_UNABLE_TO_MOVE_REPLACEMENT
)

// replacementFiles is a directory with the replaced file ("old") and its replacement ("new") as ReplaceFileW is called with them.
func replacementFiles(t *testing.T) (path, temp string) {
	t.Helper()
	dir := t.TempDir()
	path, temp = filepath.Join(dir, "auth.json"), filepath.Join(dir, "auth.json.tmp-1")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, temp
}

func content(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

func linkError(code syscall.Errno) error {
	return &os.LinkError{Op: "replace", Old: "temp", New: "path", Err: code}
}

// steps returns file systems that fail as Microsoft documents ReplaceFileW to: each state is made with real renames, so what is left on disk is what the
// recovery had to work with.
func steps(failure syscall.Errno, move func(from, to string) error) replaceSteps {
	return replaceSteps{
		replaceFile: func(replaced, replacement, backup string) error {
			switch failure {
			case 0:
				if err := os.Rename(replaced, backup); err != nil {
					return err
				}
				return os.Rename(replacement, replaced)
			case errUnableToMoveReplacement2:
				// the replaced file is the backup, the replacement keeps its name, nothing is at path
				if err := os.Rename(replaced, backup); err != nil {
					return err
				}
			}
			// 1175, 1176 with a backup name, and every other error: both files keep their names
			return linkError(failure)
		},
		moveFile: move,
		copyDACL: func(string, string) error { return nil },
	}
}

func rename(from, to string) error { return os.Rename(from, to) }

// With a backup name every failure of ReplaceFileW has a known state (Microsoft: ReplaceFileW, "Return value"). 1175, 1176 and any other error leave both
// files under their names: the file at path is the old one and the replacement is the caller's to delete.
func TestReplaceKeepingBackupLeavesTheFileWhereBothKeepTheirNames(t *testing.T) {
	for _, failure := range []syscall.Errno{errUnableToRemoveReplaced, errUnableToMoveReplacement, syscall.EACCES} {
		t.Run(failure.Error(), func(t *testing.T) {
			path, temp := replacementFiles(t)
			err := replaceKeepingBackup(path, temp, steps(failure, rename))
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want %v", err, failure)
			}
			var kept *keptFilesError
			if errors.As(err, &kept) {
				t.Fatal("files are kept where both still have their names")
			}
			if got := content(path); got != "old" {
				t.Fatalf("file at path = %q, want the old content untouched", got)
			}
			if got := content(temp); got != "new" {
				t.Fatalf("replacement = %q, want it left for the caller", got)
			}
			if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a backup exists where none was made: %v", err)
			}
		})
	}
}

// 1177 leaves no file at path: the original is under the backup name. The replacement is moved into place, which finishes the replacement, and the
// backup is removed.
func TestReplaceKeepingBackupFinishesAReplacementThatCouldNotMoveTheReplacement(t *testing.T) {
	path, temp := replacementFiles(t)
	if err := replaceKeepingBackup(path, temp, steps(errUnableToMoveReplacement2, rename)); err != nil {
		t.Fatalf("error = %v, want the replacement finished", err)
	}
	if got := content(path); got != "new" {
		t.Fatalf("file at path = %q, want the new content", got)
	}
	for _, left := range []string{temp, temp + ".bak"} {
		if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s is left behind: %v", filepath.Base(left), err)
		}
	}
}

// Where the replacement cannot be moved into place either, the original is moved back from the backup: the file at path is the old content, the
// replacement is the caller's to delete, and no backup is left.
func TestReplaceKeepingBackupRestoresTheOriginalWhenItCannotFinish(t *testing.T) {
	path, temp := replacementFiles(t)
	refused := errors.New("sharing violation")
	err := replaceKeepingBackup(path, temp, steps(errUnableToMoveReplacement2, func(from, to string) error {
		if from == temp {
			return refused
		}
		return os.Rename(from, to)
	}))
	var kept *keptFilesError
	if !errors.Is(err, errUnableToMoveReplacement2) || !errors.Is(err, refused) || errors.As(err, &kept) {
		t.Fatalf("error = %v, want the failure with the original restored", err)
	}
	if got := content(path); got != "old" {
		t.Fatalf("file at path = %q, want the original restored", got)
	}
	if got := content(temp); got != "new" {
		t.Fatalf("replacement = %q, want it left for the caller", got)
	}
	if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a backup is left after the restore: %v", err)
	}
}

// Where nothing can be moved, both files hold data that exists nowhere else: the error names them and neither is deleted.
func TestReplaceKeepingBackupKeepsBothFilesWhenNothingCanBeMoved(t *testing.T) {
	path, temp := replacementFiles(t)
	err := replaceKeepingBackup(path, temp, steps(errUnableToMoveReplacement2, func(string, string) error { return errors.New("sharing violation") }))
	var kept *keptFilesError
	if !errors.As(err, &kept) {
		t.Fatalf("error = %v, want a keptFilesError", err)
	}
	if got := content(temp); got != "new" {
		t.Fatalf("replacement = %q, want it kept", got)
	}
	if got := content(temp + ".bak"); got != "old" {
		t.Fatalf("backup = %q, want the original kept", got)
	}
	if len(kept.files) != 2 || kept.files[0] != temp || kept.files[1] != temp+".bak" {
		t.Fatalf("files named = %v, want the replacement and the backup", kept.files)
	}
}

// A success removes the backup, which is the old content.
func TestReplaceKeepingBackupRemovesTheBackupAfterASuccess(t *testing.T) {
	path, temp := replacementFiles(t)
	if err := replaceKeepingBackup(path, temp, steps(0, rename)); err != nil {
		t.Fatal(err)
	}
	if got := content(path); got != "new" {
		t.Fatalf("file at path = %q, want the new content", got)
	}
	if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the backup is left after a success: %v", err)
	}
}

// WriteFile never deletes the temporary file of a replacement that reports files kept: it holds the only copy of the new content.
func TestWriteFileKeepsTheTemporaryFileOfAReplacementThatKeptFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	previous := replaceTemp
	var temporary string
	replaceTemp = func(temp, _ string, _ bool) error {
		temporary = temp
		return &keptFilesError{err: errors.New("replace failed"), files: []string{temp}}
	}
	t.Cleanup(func() { replaceTemp = previous })
	err := WriteFile(path, []byte("new"), 0o600)
	var kept *keptFilesError
	if !errors.As(err, &kept) {
		t.Fatalf("WriteFile = %v, want the keptFilesError", err)
	}
	if got := content(temporary); got != "new" {
		t.Fatalf("temporary file = %q, want the new content kept", got)
	}
}
