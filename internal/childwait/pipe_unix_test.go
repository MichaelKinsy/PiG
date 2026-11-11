//go:build unix

package childwait

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
func holdReadersUntilTheGrace() *Hooks {
	graceExpired := make(chan struct{})
	var once sync.Once
	return &Hooks{
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

// Node's loop hands a child's stdout to the byte cap before its stderr when the child wrote stdout first, so a reader of a later pipe waits until an earlier
// pipe's bytes are delivered. That also holds when the earlier reader takes them while the later one is deciding whether to wait, which is a window of
// microseconds that only host load widens. The test plays the earlier reader from AfterPeek, the point between the later reader's two questions: on the
// first pass it claims its turn and empties its pipe, on the third it delivers. The later reader must not return before that delivery.
func TestAwaitTurnWaitsForAnEarlierReaderThatTakesItsBytesWhileALaterReaderDecides(t *testing.T) {
	stdoutR, stdoutW := newPipe(t)
	stderrR, stderrW := newPipe(t)
	// The child wrote stdout, then stderr.
	if _, err := stdoutW.WriteString("ab"); err != nil {
		t.Fatal(err)
	}
	if _, err := stderrW.WriteString("cd"); err != nil {
		t.Fatal(err)
	}
	var (
		passes    int
		delivered bool
	)
	p := &Pipes{
		files:    []*os.File{stdoutR, stderrR},
		stopped:  make([]atomic.Bool, 2),
		inflight: make([]atomic.Bool, 2),
	}
	p.hooks = &Hooks{AfterPeek: func(earlier int) {
		passes++
		switch passes {
		case 1:
			p.inflight[earlier].Store(true)
			if n, err := stdoutR.Read(make([]byte, 8)); err != nil || n != 2 {
				t.Errorf("the earlier reader took %d bytes, %v; want the 2 the child wrote", n, err)
			}
		case 3:
			delivered = true
			p.inflight[earlier].Store(false)
		}
	}}
	claimed, ok := p.awaitTurn(1, stderrR)
	if !claimed || !ok {
		t.Fatalf("awaitTurn = (%v, %v), want the turn claimed", claimed, ok)
	}
	if !delivered {
		t.Fatal("the later reader claimed its turn before the earlier reader delivered the bytes it took")
	}
}
