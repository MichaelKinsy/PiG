//go:build !windows

package plugins

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Not upstream's (Pi evaluates plugins in-process and has no socket directory). RemoveSocketDirsOf removes the directories a
// worker of one start left, and nothing of another: a live listener of a process from another pid namespace that shares the
// temporary directory (the same pid, another owner) survives, and a temporary directory whose name holds glob characters
// is searched all the same.
func TestRemoveSocketDirsOfRemovesOnlyTheDirectoriesOfItsOwner(t *testing.T) {
	// Short: a socket's path must fit a unix address.
	root, err := os.MkdirTemp("", "[x*?")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	setSocketOwner("dead")
	t.Cleanup(func() { setSocketOwner("") })
	left, address, _, err := listenNodeIn(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = left.Close()
	setSocketOwner("live")
	live, liveAddress, _, err := listenNodeIn(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = live.Close() }()

	RemoveSocketDirsOf(root, "dead")

	if _, err := os.Stat(filepath.Dir(address)); !os.IsNotExist(err) {
		t.Fatalf("the dead owner's directory = %v, want removed", err)
	}
	connection, err := net.Dial("unix", liveAddress)
	if err != nil {
		t.Fatalf("the live owner's listener: %v", err)
	}
	_ = connection.Close()
	RemoveSocketDirsOf(root, "")
	if _, err := os.Stat(filepath.Dir(liveAddress)); err != nil {
		t.Fatalf("no owner removed the live directory: %v", err)
	}
}

// A worker's socket path is short enough for a unix address (104 bytes on macOS) under a long temporary directory: 8 characters
// of owner, not the 26 of a random text, which made every path 18 bytes longer and failed `bind: invalid argument` from a
// temporary directory of about 50 bytes.
func TestASocketUnderALongTemporaryDirectoryBindsWithTheOwnerAWorkerGets(t *testing.T) {
	tmp := os.TempDir()
	pad := 60 - len(tmp) - 1
	if pad < 1 {
		t.Skipf("the temporary directory %q is already 60 bytes", tmp)
	}
	root := filepath.Join(tmp, strings.Repeat("d", pad))
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	setSocketOwner(NewSocketOwner())
	t.Cleanup(func() { setSocketOwner("") })
	listener, address, _, err := listenNodeIn(root)
	if err != nil {
		t.Fatalf("a %d-byte temporary directory: %v", len(root), err)
	}
	_ = listener.Close()
	if len(address) > 103 {
		t.Fatalf("socket path of %d bytes, past what macOS binds", len(address))
	}
}
