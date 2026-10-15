//go:build unix

package codingagent

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// The state push builds the extension theme palette for every handler dispatch, and the palette asks chalkModifiersEnabled whether stdout is a terminal. os.File.Fd puts a descriptor Go made non-blocking back in blocking mode with an fcntl on every call, and term.IsTerminal adds an ioctl, so the probe must run once per stdout file, not once per call. A probe leaves its mark: Fd clears O_NONBLOCK, so a descriptor set non-blocking again after the first call stays so unless it is probed again.
func TestChalkModifiersEnabledProbesStdoutOncePerFile(t *testing.T) {
	pipeWriter := func() (*os.File, int) {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		// SyscallConn reads the descriptor without Fd's switch to blocking mode.
		raw, err := writer.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var fd int
		if err := raw.Control(func(descriptor uintptr) { fd = int(descriptor) }); err != nil {
			t.Fatal(err)
		}
		return writer, fd
	}
	nonBlocking := func(fd int) bool {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil {
			t.Fatal(err)
		}
		return flags&unix.O_NONBLOCK != 0
	}
	saved := os.Stdout
	t.Cleanup(func() { os.Stdout = saved })

	writer, fd := pipeWriter()
	if !nonBlocking(fd) {
		t.Fatal("os.Pipe returned a blocking descriptor; the probe has no mark to observe")
	}
	os.Stdout = writer
	want := chalkModifiersEnabled()
	if nonBlocking(fd) {
		t.Fatal("the first call did not probe the new stdout")
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if got := chalkModifiersEnabled(); got != want {
			t.Fatalf("chalkModifiersEnabled() = %v after the first call returned %v", got, want)
		}
	}
	if !nonBlocking(fd) {
		t.Fatal("chalkModifiersEnabled probed the same stdout file again (os.File.Fd cleared O_NONBLOCK): an fcntl and an ioctl per call")
	}

	// A swapped stdout is probed again.
	other, otherFD := pipeWriter()
	os.Stdout = other
	chalkModifiersEnabled()
	if nonBlocking(otherFD) {
		t.Fatal("a swapped stdout was not probed")
	}
}
