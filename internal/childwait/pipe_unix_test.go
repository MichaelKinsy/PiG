//go:build unix

package childwait

import (
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
)

func newPipe(t *testing.T) (r, w *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}

// holdReadersUntilTheGrace makes every reader fall behind the child's exit by more than the grace, as a reader that a loaded machine does not schedule
// for Grace does: each waits before its first read until the grace has elapsed once.
func holdReadersUntilTheGrace() Hooks {
	graceExpired := make(chan struct{})
	var once sync.Once
	return Hooks{
		BeforeRead:   func() { <-graceExpired },
		GraceExpired: func() { once.Do(func() { close(graceExpired) }) },
	}
}

// collector gathers what the readers deliver, per pipe.
type collector struct {
	mu  sync.Mutex
	got [2]strings.Builder
}

func (c *collector) take(index int, chunk []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got[index].Write(chunk)
}

func (c *collector) text(index int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got[index].String()
}

// poll(2) tells the two ends of a pipe's life apart without taking a byte: a write end open or closed, and bytes waiting or not. A pipe whose write
// ends are all closed reports POLLHUP whether or not bytes remain, and the end of the stream is readable on macOS, so only a pipe that still has a
// write end reports unread bytes.
func TestPollPipeReportsTheWriteEndsAndTheBytesOfAPipe(t *testing.T) {
	r, w := newPipe(t)
	check := func(step string, wantHungUp, wantUnread bool) {
		t.Helper()
		hungUp, unread, known := pollPipe(r)
		if !known || hungUp != wantHungUp || unread != wantUnread {
			t.Fatalf("%s: hungUp=%v unread=%v known=%v, want hungUp=%v unread=%v known=true", step, hungUp, unread, known, wantHungUp, wantUnread)
		}
	}
	check("empty, write end open", false, false)
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	check("bytes waiting, write end open", false, true)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check("bytes waiting, write end closed", true, false)
	if _, err := r.Read(make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	check("drained, write end closed", true, false)
}

// polling a pipe leaves it as it was: the descriptor stays non-blocking under the reader (File.Fd would make it blocking and tie up a thread per
// reader) and no byte is taken.
func TestPollPipeTakesNothingAndLeavesTheReaderWorking(t *testing.T) {
	r, w := newPipe(t)
	if _, err := w.WriteString("kept"); err != nil {
		t.Fatal(err)
	}
	nonblocking := func() bool {
		conn, err := r.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var flags int
		var flagsErr error
		if err := conn.Control(func(fd uintptr) { flags, flagsErr = unix.FcntlInt(fd, unix.F_GETFL, 0) }); err != nil || flagsErr != nil {
			t.Fatalf("F_GETFL: %v, %v", err, flagsErr)
		}
		return flags&unix.O_NONBLOCK != 0
	}
	if !nonblocking() {
		t.Fatal("os.Pipe's read end is not non-blocking before the poll; the assertion below proves nothing")
	}
	for range 3 {
		if _, unread, known := pollPipe(r); !known || !unread {
			t.Fatalf("unread=%v known=%v, want the waiting bytes", unread, known)
		}
	}
	if !nonblocking() {
		t.Fatal("polling made the read end blocking")
	}
	buf := make([]byte, 16)
	n, err := r.Read(buf)
	if err != nil || string(buf[:n]) != "kept" {
		t.Fatalf("read %q, %v after polling, want %q", buf[:n], err, "kept")
	}
}

// A reader that falls behind the exit by more than the grace must still deliver what the exited command wrote when no process holds the pipe: every
// write end is closed, so the grace has not measured a quiet writer. mayHoldOutput says a descendant may hold it, as it does on Unix, where a
// process that left the group can; the pipe itself says none does.
func TestPipesWaitReadsToEOFAfterTheGraceWhenEveryWriteEndIsClosed(t *testing.T) {
	r, w := newPipe(t)
	if _, err := w.WriteString("kept"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var c collector
	pipes := Start([]*os.File{r}, c.take, holdReadersUntilTheGrace())
	if closed := pipes.Wait(yes); !closed {
		t.Fatal("Wait released a pipe that no process holds")
	}
	if got := c.text(0); got != "kept" {
		t.Fatalf("output = %q, want %q", got, "kept")
	}
}

// A holder keeps the write end open, so the grace applies to it, but the reader fell behind: what the exited command wrote is waiting unread, and
// the grace must neither release the pipe on it nor discard it. The holder's own silence afterwards releases the wait.
func TestPipesWaitDeliversWhatWaitsUnreadInAPipeAHolderKeepsOpen(t *testing.T) {
	r, w := newPipe(t)
	if _, err := w.WriteString("kept"); err != nil {
		t.Fatal(err)
	}
	var c collector
	pipes := Start([]*os.File{r}, c.take, holdReadersUntilTheGrace())
	if closed := pipes.Wait(yes); closed {
		t.Fatal("Wait reported an ended pipe while the holder keeps its write end open")
	}
	if got := c.text(0); got != "kept" {
		t.Fatalf("output = %q, want %q", got, "kept")
	}
}

// With two pipes, a pipe that ended gives no reason to release the other: the one that waits unread is delivered, and the holder of the second
// pipe is quiet only once both readers have everything.
func TestPipesWaitDeliversEveryPipeWhenOneEndedAndAHolderKeepsTheOther(t *testing.T) {
	outRead, outWrite := newPipe(t)
	errRead, errWrite := newPipe(t)
	if _, err := outWrite.WriteString("out"); err != nil {
		t.Fatal(err)
	}
	if err := outWrite.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := errWrite.WriteString("err"); err != nil {
		t.Fatal(err)
	}
	var c collector
	pipes := Start([]*os.File{outRead, errRead}, c.take, holdReadersUntilTheGrace())
	if closed := pipes.Wait(yes); closed {
		t.Fatal("Wait reported ended pipes while a holder keeps the second write end open")
	}
	if out, errText := c.text(0), c.text(1); out != "out" || errText != "err" {
		t.Fatalf("output = %q and %q, want %q and %q", out, errText, "out", "err")
	}
}
