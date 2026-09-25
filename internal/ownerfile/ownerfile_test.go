package ownerfile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func ownerOnly(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := ownerfile.OwnerOnly(path, info)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// permissiveDir returns a directory whose ordinary new files others can read,
// checked with a control file, so an owner-only result below is meaningful.
func permissiveDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testenv.InheritOthersRead(t, dir)
	control := filepath.Join(dir, "control")
	if err := os.WriteFile(control, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(control, 0o644); err != nil {
		t.Fatal(err)
	}
	if ownerOnly(t, control) {
		t.Fatal("an ordinary file in the test directory is owner-only, so exposure could not be detected")
	}
	return dir
}

// A file for secrets is owner-only from the moment it exists, before any byte
// is written, even in a directory whose new files others can read. A DACL
// applied after creation would leave a window in which another principal can
// open a handle it keeps.
func TestCreateNewIsOwnerOnlyBeforeItsFirstWrite(t *testing.T) {
	path := filepath.Join(permissiveDir(t), "secret")
	file, err := ownerfile.CreateNew(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ownerOnly(t, path) {
		_ = file.Close()
		t.Fatal("a new owner-only file is readable by others before its first write")
	}
	if _, err := file.WriteString("SECRET"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "SECRET" {
		t.Fatalf("read back %q, %v", data, err)
	}
	if _, err := ownerfile.CreateNew(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("CreateNew over an existing file: %v, want os.ErrExist", err)
	}
}

// CreateTemp gives a temporary file the same protection, with os.CreateTemp's
// naming.
func TestCreateTempIsOwnerOnlyBeforeItsFirstWrite(t *testing.T) {
	dir := permissiveDir(t)
	file, err := ownerfile.CreateTemp(dir, ".stage-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	name := filepath.Base(file.Name())
	if filepath.Dir(file.Name()) != dir || !strings.HasPrefix(name, ".stage-") || !strings.HasSuffix(name, ".tmp") || len(name) == len(".stage-.tmp") {
		t.Fatalf("temporary file %s, want %s/.stage-<random>.tmp", file.Name(), dir)
	}
	if !ownerOnly(t, file.Name()) {
		t.Fatal("a new owner-only temporary file is readable by others before its first write")
	}
	if _, err := ownerfile.CreateTemp(dir, "a/b*"); err == nil {
		t.Fatal("a pattern with a path separator was accepted")
	}
}
