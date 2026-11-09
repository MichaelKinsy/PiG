//go:build !windows

package daemon

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// closeAtCleanup closes file when the test ends and fails the test on a close error other than a close already made.
func closeAtCleanup(t *testing.T, file *os.File) {
	t.Helper()
	t.Cleanup(func() {
		if err := file.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("closing %s: %v", file.Name(), err)
		}
	})
}

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
	closeAtCleanup(t, read)
	closeAtCleanup(t, write)
	if read.SetReadDeadline(time.Now()) == nil {
		t.Fatal("a blocking inherited pipe is already in the poller; the test has nothing to prove")
	}
	// A second holder of the open file description, as a parent shell is: it sees the mode the daemon sets.
	holder, err := unix.FcntlInt(uintptr(fds[0]), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Close(holder); err != nil {
			t.Errorf("closing the other holder: %v", err)
		}
	})
	moved, restore := pollable(read)
	if flags, err := unix.FcntlInt(uintptr(holder), unix.F_GETFL, 0); err != nil || flags&unix.O_NONBLOCK == 0 {
		t.Fatalf("the other holder does not see the description non-blocking: flags %#x, %v", flags, err)
	}
	if err := moved.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatalf("the moved descriptor is not in the poller: %v", err)
	}
	if _, err := moved.Read(make([]byte, 1)); !os.IsTimeout(err) {
		t.Fatalf("read of an empty pipe: %v, want a timeout", err)
	}
	if _, err := write.WriteString("x"); err != nil {
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
	for name, fd := range map[string]int{"the inherited descriptor": fds[0], "the other holder": holder} {
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0); err != nil || flags&unix.O_NONBLOCK != 0 {
			t.Fatalf("%s was left non-blocking: flags %#x, %v", name, flags, err)
		}
	}
}

// TestPollableLeavesOtherFilesAlone: a regular file is not moved.
func TestPollableLeavesOtherFilesAlone(t *testing.T) {
	file, err := os.Create(t.TempDir() + "/file")
	if err != nil {
		t.Fatal(err)
	}
	closeAtCleanup(t, file)
	moved, restore := pollable(file)
	defer restore()
	if moved != file {
		t.Fatal("a regular file was duplicated")
	}
}

// TestWriteStderrWaitsOnAFullPipe: with 2>&1 the daemon's stderr is the description Serve made non-blocking, and Go's
// stderr (a blocking-mode file) answers a full pipe with EAGAIN. A message must wait for room, as Pi's eprintln does.
func TestWriteStderrWaitsOnAFullPipe(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatal(err)
	}
	// Blocking-mode files, as os.Stderr is, on a description that is then made non-blocking by the other holder.
	read, write := os.NewFile(uintptr(fds[0]), "read"), os.NewFile(uintptr(fds[1]), "write")
	closeAtCleanup(t, read)
	closeAtCleanup(t, write)
	if err := unix.SetNonblock(fds[1], true); err != nil {
		t.Fatal(err)
	}
	filler := make([]byte, 4096)
	for {
		if _, err := unix.Write(fds[1], filler); err != nil {
			break
		}
	}
	const message = "thread panicked: boom\n"
	done := make(chan struct{})
	go func() {
		writeStderr(write, message)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("the message was dropped while the pipe was full")
	default:
	}
	var got []byte
	buffer := make([]byte, 1<<16)
	for !strings.HasSuffix(string(got), message) {
		n, err := read.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buffer[:n]...)
	}
	<-done
}
