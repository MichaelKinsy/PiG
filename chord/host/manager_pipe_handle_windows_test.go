//go:build windows

package host

import (
	"errors"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

var handlePipeSequence atomic.Int64

// handlePipePair is a duplex named pipe: the server end is wrapped in a handlePipe, the client end is read and written directly. overlapped selects the
// flag on the wrapped end (the one libuv creates by CreateFile in a child).
func handlePipePair(t *testing.T, overlapped bool) (*handlePipe, windows.Handle) {
	t.Helper()
	name := `\\.\pipe\pig-handlepipe-test-` + strconv.Itoa(windows.GetCurrentProcessId()) + "-" + strconv.FormatInt(handlePipeSequence.Add(1), 10)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX)
	if overlapped {
		flags |= windows.FILE_FLAG_OVERLAPPED
	}
	server, err := windows.CreateNamedPipe(namePtr, flags, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 65536, 65536, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := windows.CreateFile(namePtr, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(server)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(client) })
	return newHandlePipe(server, !overlapped), client
}

func forEachHandlePipeKind(t *testing.T, run func(t *testing.T, overlapped bool)) {
	for _, overlapped := range []bool{false, true} {
		name := "synchronous"
		if overlapped {
			name = "overlapped"
		}
		t.Run(name, func(t *testing.T) { run(t, overlapped) })
	}
}

// Close must end a Read that waits for data, and must not return before that Read has: no sleep-retry stands between the two.
func TestHandlePipeCloseEndsPendingRead(t *testing.T) {
	forEachHandlePipeKind(t, func(t *testing.T, overlapped bool) {
		for i := 0; i < 200; i++ {
			pipe, _ := handlePipePair(t, overlapped)
			result := make(chan error, 1)
			go func() {
				_, err := pipe.Read(make([]byte, 16))
				result <- err
			}()
			// Alternate between closing at once, which races the Read's entry into its I/O call, and after it has had time to wait.
			if i%2 == 1 {
				time.Sleep(2 * time.Millisecond)
			}
			closed := make(chan struct{})
			go func() { _ = pipe.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: Close did not return", i)
			}
			select {
			case err := <-result:
				if !errors.Is(err, net.ErrClosed) {
					t.Fatalf("round %d: Read returned %v, want net.ErrClosed", i, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: Read did not return after Close", i)
			}
		}
	})
}

// A Write while a Read waits for data must go through (on a synchronous handle by cancelling the wait), and the Read must then still see what the
// peer sends.
func TestHandlePipeWriteWhileReadWaits(t *testing.T) {
	forEachHandlePipeKind(t, func(t *testing.T, overlapped bool) {
		pipe, client := handlePipePair(t, overlapped)
		defer func() { _ = pipe.Close() }()
		type readResult struct {
			data string
			err  error
		}
		read := make(chan readResult, 1)
		go func() {
			buf := make([]byte, 16)
			n, err := pipe.Read(buf)
			read <- readResult{string(buf[:n]), err}
		}()
		for i := 0; i < 200; i++ {
			message := []byte("ping-" + strconv.Itoa(i))
			wrote := make(chan error, 1)
			go func() {
				_, err := pipe.Write(message)
				wrote <- err
			}()
			select {
			case err := <-wrote:
				if err != nil {
					t.Fatalf("write %d: %v", i, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("write %d: did not complete while a Read waited", i)
			}
			got := make([]byte, len(message))
			var total uint32
			for int(total) < len(got) {
				var n uint32
				if err := windows.ReadFile(client, got[total:], &n, nil); err != nil {
					t.Fatalf("client read %d: %v", i, err)
				}
				total += n
			}
			if string(got) != string(message) {
				t.Fatalf("client read %d: %q, want %q", i, got, message)
			}
		}
		var written uint32
		if err := windows.WriteFile(client, []byte("pong"), &written, nil); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-read:
			if result.err != nil || result.data != "pong" {
				t.Fatalf("Read returned %q, %v; want pong", result.data, result.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("Read did not see the peer's data")
		}
	})
}

// A Close racing Writes must end them all, whatever they were doing, and the peer's closing must read as EOF.
func TestHandlePipeCloseEndsWrites(t *testing.T) {
	forEachHandlePipeKind(t, func(t *testing.T, overlapped bool) {
		for i := 0; i < 100; i++ {
			pipe, _ := handlePipePair(t, overlapped)
			// Nobody reads the client end, so a large Write blocks once the pipe buffer is full.
			result := make(chan error, 1)
			go func() {
				_, err := pipe.Write(make([]byte, 4<<20))
				result <- err
			}()
			if i%2 == 1 {
				time.Sleep(2 * time.Millisecond)
			}
			closed := make(chan struct{})
			go func() { _ = pipe.Close(); close(closed) }()
			select {
			case <-closed:
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: Close did not return", i)
			}
			select {
			case err := <-result:
				if err == nil || errors.Is(err, io.EOF) {
					t.Fatalf("round %d: Write returned %v, want an error", i, err)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("round %d: Write did not return after Close", i)
			}
		}
	})
}
