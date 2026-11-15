//go:build !windows

package atomicfile

import (
	"errors"
	"fmt"
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
		carry:    func(string, string) error { return nil },
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
			if _, ok := errors.AsType[*keptFilesError](err); ok {
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
	if _, ok := errors.AsType[*keptFilesError](err); !ok {
		t.Fatalf("WriteFile = %v, want the keptFilesError", err)
	}
	if got := content(temporary); got != "new" {
		t.Fatalf("temporary file = %q, want the new content kept", got)
	}
}

// carried records what it was called with and what the files were when it was.
type carried struct {
	from, to         string
	pathHeld, source string
	calls            int
}

func (c *carried) step(path string, result error) func(from, to string) error {
	return func(from, to string) error {
		c.calls++
		c.from, c.to = from, to
		c.pathHeld, c.source = content(path), content(from)
		return result
	}
}

// After ReplaceFileW the descriptor of the old file, which the backup still is, is carried onto the file at path, which holds the new content by then; a
// carry that leaves the descriptor equal is a success and the backup is deleted.
func TestReplaceKeepingBackupCarriesTheOldDescriptorOntoTheNewFile(t *testing.T) {
	path, temp := replacementFiles(t)
	s := steps(0, rename)
	var c carried
	s.carry = c.step(path, nil)
	if err := replaceKeepingBackup(path, temp, s); err != nil {
		t.Fatal(err)
	}
	if c.calls != 1 || c.from != temp+".bak" || c.to != path || c.pathHeld != "new" || c.source != "old" {
		t.Fatalf("carry called %d times from %q to %q with the path holding %q and the source %q, want once from the backup (old) to the path (new)", c.calls, c.from, c.to, c.pathHeld, c.source)
	}
	if got := content(path); got != "new" {
		t.Fatalf("file at path = %q, want the new content", got)
	}
	if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the backup is left after a success: %v", err)
	}
}

// Only SE_DACL_AUTO_INHERITED differing after the carry is not a failure: the new file stays, the backup goes, and there is no error.
func TestReplaceKeepingBackupSucceedsWhenOnlyTheAutoInheritedFlagDiffers(t *testing.T) {
	path, temp := replacementFiles(t)
	s := steps(0, rename)
	s.carry = func(string, string) error { return fmt.Errorf("carry: %w", errAutoInheritedDiffers) }
	if err := replaceKeepingBackup(path, temp, s); err != nil {
		t.Fatalf("error = %v, want success", err)
	}
	if got := content(path); got != "new" {
		t.Fatalf("file at path = %q, want the new content", got)
	}
	if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the backup is left after a success: %v", err)
	}
}

// A carry that leaves other entries or other protection than the old file's undoes the replacement: the old file is moved back from the backup, the error
// is the carry's, and nothing is left beside the file.
func TestReplaceKeepingBackupRestoresTheOldFileWhenTheAccessDiffersAfterTheReplacement(t *testing.T) {
	path, temp := replacementFiles(t)
	s := steps(0, rename)
	s.carry = func(string, string) error { return fmt.Errorf("carry: %w", errAccessDiffers) }
	err := replaceKeepingBackup(path, temp, s)
	if !errors.Is(err, errAccessDiffers) {
		t.Fatalf("error = %v, want the carry's error", err)
	}
	if _, ok := errors.AsType[*keptFilesError](err); ok {
		t.Fatal("the old file could be moved back, but the error names kept files")
	}
	if got := content(path); got != "old" {
		t.Fatalf("file at path = %q, want the old file restored", got)
	}
	for _, left := range []string{temp, temp + ".bak"} {
		if _, err := os.Stat(left); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s is left behind: %v", filepath.Base(left), err)
		}
	}
}

// A carry that fails for another reason than a difference it found (the descriptor could not be read back) is not taken for success either.
func TestReplaceKeepingBackupRestoresTheOldFileWhenTheCarryFails(t *testing.T) {
	path, temp := replacementFiles(t)
	s := steps(0, rename)
	failure := errors.New("the descriptor cannot be read")
	s.carry = func(string, string) error { return failure }
	if err := replaceKeepingBackup(path, temp, s); !errors.Is(err, failure) {
		t.Fatalf("error = %v, want the carry's error", err)
	}
	if got := content(path); got != "old" {
		t.Fatalf("file at path = %q, want the old file restored", got)
	}
}

// When the old file cannot be moved back either, the new file at path and the backup hold data that exists nowhere else: the error names both and neither is
// deleted.
func TestReplaceKeepingBackupKeepsBothFilesWhenTheOldFileCannotBeRestored(t *testing.T) {
	path, temp := replacementFiles(t)
	refused := errors.New("sharing violation")
	s := steps(0, func(from, to string) error {
		if to == path && from == temp+".bak" {
			return refused
		}
		return os.Rename(from, to)
	})
	s.carry = func(string, string) error { return errAccessDiffers }
	err := replaceKeepingBackup(path, temp, s)
	kept, ok := errors.AsType[*keptFilesError](err)
	if !ok || !errors.Is(err, errAccessDiffers) || !errors.Is(err, refused) {
		t.Fatalf("error = %v, want a keptFilesError with the carry's error and the refusal", err)
	}
	if len(kept.files) != 2 || kept.files[0] != path || kept.files[1] != temp+".bak" {
		t.Fatalf("files named = %v, want the file at path and the backup", kept.files)
	}
	if got := content(path); got != "new" {
		t.Fatalf("file at path = %q, want the new content kept", got)
	}
	if got := content(temp + ".bak"); got != "old" {
		t.Fatalf("backup = %q, want the original kept", got)
	}
}

// A replacement that stopped half way (1177) is finished with the old descriptor carried onto the replacement first and onto the file at path after the
// move, and the same decisions apply to both.
func TestReplaceKeepingBackupCarriesTheDescriptorAroundTheMoveOfAHalfWayReplacement(t *testing.T) {
	t.Run("both carries equal or auto-inherited only", func(t *testing.T) {
		path, temp := replacementFiles(t)
		s := steps(errUnableToMoveReplacement2, rename)
		var sources, targets []string
		s.carry = func(from, to string) error {
			sources, targets = append(sources, from), append(targets, to)
			return errAutoInheritedDiffers
		}
		if err := replaceKeepingBackup(path, temp, s); err != nil {
			t.Fatalf("error = %v, want the replacement finished", err)
		}
		if len(sources) != 2 || sources[0] != temp+".bak" || targets[0] != temp || sources[1] != temp+".bak" || targets[1] != path {
			t.Fatalf("carries from %v to %v, want the backup onto the replacement, then onto the path", sources, targets)
		}
		if got := content(path); got != "new" {
			t.Fatalf("file at path = %q, want the new content", got)
		}
		if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("the backup is left after a success: %v", err)
		}
	})
	t.Run("the replacement cannot take the access", func(t *testing.T) {
		path, temp := replacementFiles(t)
		s := steps(errUnableToMoveReplacement2, rename)
		s.carry = func(string, string) error { return errAccessDiffers }
		err := replaceKeepingBackup(path, temp, s)
		if !errors.Is(err, errUnableToMoveReplacement2) || !errors.Is(err, errAccessDiffers) {
			t.Fatalf("error = %v, want the replace error and the carry's", err)
		}
		if got := content(path); got != "old" {
			t.Fatalf("file at path = %q, want the old file restored", got)
		}
		if got := content(temp); got != "new" {
			t.Fatalf("replacement = %q, want it left for the caller", got)
		}
	})
	t.Run("the access differs after the move", func(t *testing.T) {
		path, temp := replacementFiles(t)
		s := steps(errUnableToMoveReplacement2, rename)
		calls := 0
		s.carry = func(string, string) error {
			calls++
			if calls == 1 {
				return nil
			}
			return errAccessDiffers
		}
		err := replaceKeepingBackup(path, temp, s)
		if !errors.Is(err, errAccessDiffers) {
			t.Fatalf("error = %v, want the carry's error", err)
		}
		if got := content(path); got != "old" {
			t.Fatalf("file at path = %q, want the old file restored", got)
		}
		if _, err := os.Stat(temp + ".bak"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a backup is left after the restore: %v", err)
		}
	})
}

// carryVerdict: the entries with their flags and SE_DACL_PROTECTED are the access; SE_DACL_AUTO_INHERITED alone is not.
func TestCarryVerdict(t *testing.T) {
	base := access{protected: true, entries: []string{"allow-owner"}}
	for _, tc := range []struct {
		name string
		got  access
		want error
	}{
		{"equal", base, nil},
		{"only the auto-inherited flag", access{protected: true, autoInherited: true, entries: []string{"allow-owner"}}, errAutoInheritedDiffers},
		{"other entries", access{protected: true, entries: []string{"allow-everyone"}}, errAccessDiffers},
		{"an entry more", access{protected: true, entries: []string{"allow-owner", "allow-everyone"}}, errAccessDiffers},
		{"other entry flags", access{protected: true, entries: []string{"allow-owner-inherit"}}, errAccessDiffers},
		{"not protected", access{entries: []string{"allow-owner"}}, errAccessDiffers},
		{"auto-inherited and other entries", access{protected: true, autoInherited: true, entries: []string{"x"}}, errAccessDiffers},
		{"a NULL DACL", access{protected: true, null: true}, errAccessDiffers},
		{"no DACL", access{protected: true, absent: true}, errAccessDiffers},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := carryVerdict(base, tc.got); !errors.Is(got, tc.want) || (tc.want == nil) != (got == nil) {
				t.Fatalf("carryVerdict = %v, want %v", got, tc.want)
			}
		})
	}
}

// The owner and the group are carried as far as the token may set them: the refusals of a token without the right are not errors, every other error is.
func TestIgnoreOwnerRefusal(t *testing.T) {
	for _, code := range []syscall.Errno{errInvalidOwner, errInvalidPrimaryGroup, errPrivilegeNotHeld, errAccessDenied} {
		t.Run(code.Error(), func(t *testing.T) {
			if err := ignoreOwnerRefusal(&os.PathError{Op: "set owner", Path: "x", Err: code}); err != nil {
				t.Fatalf("error = %v, want it ignored", err)
			}
		})
	}
	sharing := syscall.Errno(32) // ERROR_SHARING_VIOLATION
	if err := ignoreOwnerRefusal(&os.PathError{Op: "set owner", Path: "x", Err: sharing}); !errors.Is(err, sharing) {
		t.Fatalf("error = %v, want the sharing violation returned", err)
	}
	if err := ignoreOwnerRefusal(nil); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
}
