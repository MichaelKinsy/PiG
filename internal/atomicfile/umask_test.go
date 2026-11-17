//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

const (
	newFileHelperEnv = "ATOMICFILE_TEST_NEW_FILE" // the path the helper process writes
	newFilePermEnv   = "ATOMICFILE_TEST_NEW_FILE_PERM"
)

// TestWriteFileNewFileHelper is the child of TestWriteFileGivesANewFileTheModeOpenGives: it runs in a process whose umask the parent chose, and does nothing
// when run on its own.
func TestWriteFileNewFileHelper(t *testing.T) {
	path := os.Getenv(newFileHelperEnv)
	if path == "" {
		return
	}
	perm, err := strconv.ParseUint(os.Getenv(newFilePermEnv), 8, 32)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), os.FileMode(perm)); err != nil {
		t.Fatal(err)
	}
}

// A file that does not exist yet gets the mode a writeFileSync(path, data, {mode: perm}) gives it: perm with the umask of the process taken out. A caller
// that asks for 0666 (the MCP config, the keybindings migration) must not get a world-writable file under umask 022, and one that asks for 0644 must
// not get a group- and world-readable file under umask 077. Under umask 000 nothing is taken out: the credential files (0600) stay 0600.
func TestWriteFileGivesANewFileTheModeOpenGives(t *testing.T) {
	for _, perm := range []os.FileMode{0o600, 0o644, 0o666} {
		for _, umask := range []os.FileMode{0o000, 0o002, 0o022, 0o077} {
			t.Run(fmt.Sprintf("perm %04o umask %04o", perm, umask), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "mcp.json")
				child := exec.Command("sh", "-c", fmt.Sprintf(`umask %04o; exec "$0" -test.run='^TestWriteFileNewFileHelper$' -test.count=1`, umask), os.Args[0])
				child.Env = append(os.Environ(), newFileHelperEnv+"="+path, newFilePermEnv+"="+strconv.FormatUint(uint64(perm), 8))
				if out, err := child.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if want := perm &^ umask; info.Mode().Perm() != want {
					t.Fatalf("mode of a new file = %04o, want %04o (perm %04o without umask %04o, as open(O_CREAT) gives it)", info.Mode().Perm(), want, perm, umask)
				}
				noTemporaryFile(t, dir)
			})
		}
	}
}
