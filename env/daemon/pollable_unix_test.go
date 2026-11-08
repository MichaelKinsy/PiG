//go:build !windows

package daemon

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestPollableMovesInheritedPipesIntoThePoller: the daemon's stdin and stdout arrive as blocking pipes. A goroutine blocked in
// a read or write on one holds a scheduler slot, so a request that arrives while every slot is held waits for sysmon to take
// one back (up to 10 ms) before a worker runs it, and queued command output flows past the reply in that time. A pollable
// descriptor parks the goroutine instead.
func TestPollableMovesInheritedPipesIntoThePoller(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	// os.NewFile of a blocking descriptor is not in the poller: it has no deadlines.
	read, write := os.NewFile(uintptr(fds[0]), "read"), os.NewFile(uintptr(fds[1]), "write")
	defer read.Close()
	defer write.Close()
	if read.SetReadDeadline(time.Now()) == nil {
		t.Fatal("a blocking inherited pipe is already in the poller; the test has nothing to prove")
	}
	moved, restore := pollable(read)
	if err := moved.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("the moved descriptor is not in the poller: %v", err)
	}
	if _, err := moved.Read(make([]byte, 1)); !os.IsTimeout(err) {
		t.Fatalf("read of an empty pipe: %v, want a timeout", err)
	}
	if _, err := write.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := moved.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if n, err := moved.Read(buffer); n != 1 || err != nil || buffer[0] != 'x' {
		t.Fatalf("read through the moved descriptor: %d %v %q", n, err, buffer)
	}
	restore()
	if flags, err := unix.FcntlInt(uintptr(fds[0]), unix.F_GETFL, 0); err != nil || flags&unix.O_NONBLOCK != 0 {
		t.Fatalf("the inherited descriptor was left non-blocking: flags %#x, %v", flags, err)
	}
}

// TestPollableLeavesOtherFilesAlone: a regular file is not moved.
func TestPollableLeavesOtherFilesAlone(t *testing.T) {
	file, err := os.Create(t.TempDir() + "/file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	moved, restore := pollable(file)
	defer restore()
	if moved != file {
		t.Fatal("a regular file was duplicated")
	}
}
