//go:build windows

package childwait

import (
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetHandleInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")

// inheritable is GetHandleInformation(handle) & HANDLE_FLAG_INHERIT, which x/sys does not wrap.
func inheritable(t *testing.T, handle windows.Handle) bool {
	t.Helper()
	var flags uint32
	if ok, _, err := procGetHandleInformation.Call(uintptr(handle), uintptr(unsafe.Pointer(&flags))); ok == 0 { //nolint:gosec // G103: GetHandleInformation fills the flags variable, and x/sys has no wrapper for it.
		t.Fatalf("GetHandleInformation: %v", err)
	}
	return flags&windows.HANDLE_FLAG_INHERIT != 0
}

// The read end must be one Go parks on the completion port (only then does closing it end a pending read, and only then is a deadline accepted), and the
// write end the synchronous handle a child expects.
func TestNewPipeReadEndIsOverlappedAndWriteEndIsNot(t *testing.T) {
	r, w, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if err := r.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("the read end is not pollable: %v", err)
	}
	handleOf := func(f *os.File) (handle windows.Handle) {
		conn, err := f.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Control(func(fd uintptr) { handle = windows.Handle(fd) })
		return handle
	}
	if !overlapped(handleOf(r)) {
		t.Fatal("read end is synchronous")
	}
	if overlapped(handleOf(w)) {
		t.Fatal("write end is overlapped: a child writing without an OVERLAPPED would misbehave")
	}
	if !inheritable(t, handleOf(w)) {
		t.Fatal("write end is not inheritable")
	}
	if inheritable(t, handleOf(r)) {
		t.Fatal("read end is inheritable")
	}
	// A child asks its stdout about itself (Node shutting it down, MSYS choosing a chunk size): that needs FILE_READ_ATTRIBUTES on the write end.
	var flags, outSize, inSize, instances uint32
	if err := windows.GetNamedPipeInfo(handleOf(w), &flags, &outSize, &inSize, &instances); err != nil {
		t.Fatalf("the write end cannot be asked about its pipe: %v", err)
	}
}

// A wait with nothing to read ends at its timeout, and leaves the pipe usable: the cancelled zero-byte read took nothing.
func TestPollReadableTimesOutAndTakesNothing(t *testing.T) {
	r, w, err := NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	start := time.Now()
	for range 20 {
		if readable, closed := pollReadable(r, 20); readable || closed {
			t.Fatalf("pollReadable = %v, %v on an empty pipe", readable, closed)
		}
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("20 waits of 20 ms took %v: they returned early", elapsed)
	}
	_, _ = w.WriteString("abc")
	buf := make([]byte, 8)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "abc" {
		t.Fatalf("Read = %q, %v", buf[:n], err)
	}
}

// A synchronous handle (os.Pipe's) is still answered, by the 1 ms poll.
func TestPollReadableOnASynchronousPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if readable, closed := pollReadable(r, 5); readable || closed {
		t.Fatalf("pollReadable = %v, %v on an empty pipe", readable, closed)
	}
	_, _ = w.WriteString("x")
	if readable, closed := pollReadable(r, 1000); !readable || closed {
		t.Fatalf("pollReadable = %v, %v with a byte waiting", readable, closed)
	}
}
