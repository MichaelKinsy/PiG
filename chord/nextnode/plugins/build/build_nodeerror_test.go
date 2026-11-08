package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// An input that cannot be read is a build error whose text is Node's (Pi's build reads it with `readFile`): the code, what it
// means, and the path. (Pi's text has the location of esbuild's own library in front of it, an artifact of its JavaScript API.)
func TestEsbuildBuildAnInputThatCannotBeReadIsNodesErrorText(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone.ts")
	_, err := os.ReadFile(missing)
	if got, want := nodeReadError(err, missing), "ENOENT: no such file or directory, open '"+missing+"'"; got != want {
		t.Errorf("a missing input: %q, want %q", got, want)
	}
	if got, want := nodeReadError(&fs.PathError{Op: "open", Path: missing, Err: fs.ErrPermission}, missing), "EACCES: permission denied, open '"+missing+"'"; got != want {
		t.Errorf("a forbidden input: %q, want %q", got, want)
	}
	if got, want := nodeReadError(errors.New("another failure"), missing), "another failure"; got != want {
		t.Errorf("another failure: %q, want %q", got, want)
	}
}
