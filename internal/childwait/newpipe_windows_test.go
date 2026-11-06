//go:build windows

package childwait

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

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
	var flags uint32
	if err := windows.GetHandleInformation(handleOf(w), &flags); err != nil || flags&windows.HANDLE_FLAG_INHERIT == 0 {
		t.Fatalf("write end is not inheritable (%v, flags %d)", err, flags)
	}
	if err := windows.GetHandleInformation(handleOf(r), &flags); err != nil || flags&windows.HANDLE_FLAG_INHERIT != 0 {
		t.Fatalf("read end is inheritable (%v, flags %d)", err, flags)
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
