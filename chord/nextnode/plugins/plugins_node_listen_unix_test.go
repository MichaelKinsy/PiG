//go:build !windows

package plugins

import (
	"net"
	"os"
	"path/filepath"
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
	t.Setenv(SocketOwnerEnv, "dead")
	left, address, _, err := listenNodeIn(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = left.Close()
	t.Setenv(SocketOwnerEnv, "live")
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
