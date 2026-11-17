package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A temporary file that cannot take the access (the extended attributes on Unix, the DACL's entries and SE_DACL_PROTECTED on Windows) of the file it is to
// replace means nothing has been replaced yet: the write goes in place, as Pi writes it, is reported, and is not an error. The file is never replaced.
func TestWriteFileWritesInPlaceWhereTheTemporaryFileCannotTakeTheAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := errors.New("the temporary file takes no such access")
	previousCarry, previousReplace := copyAttributes, replaceTemp
	copyAttributes = func(*os.File, string) error { return refused }
	replaced := false
	replaceTemp = func(string, string, bool) error { replaced = true; return nil }
	t.Cleanup(func() { copyAttributes, replaceTemp = previousCarry, previousReplace })
	var causes []error
	SetInPlaceReporter(func(_ string, cause error) { causes = append(causes, cause) })
	t.Cleanup(func() { SetInPlaceReporter(nil) })
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile = %v, want an in-place write without an error", err)
	}
	if replaced {
		t.Fatal("the file was replaced although the temporary file could not take its access")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "new" {
		t.Fatalf("file = %q (%v), want the new content written in place", got, err)
	}
	if len(causes) != 1 || !errors.Is(causes[0], refused) {
		t.Fatalf("in-place writes reported = %v, want one with the refusal as the cause", causes)
	}
}
