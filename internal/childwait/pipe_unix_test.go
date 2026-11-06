//go:build unix

package childwait

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
// first pass it claims its turn, empties its pipe and hands the delivery to another goroutine, which delivers and hands the turn on as a
// reader does, whether or not the later reader has gone to sleep yet. The later reader must not return before that delivery, and it must wake on the hand-off, which is the only thing that tells it to look again.
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
		passes    atomic.Int32
		delivered atomic.Bool
	)
	p := newPipes([]*os.File{stdoutR, stderrR}, nil, nil)
	p.hooks = &Hooks{AfterPeek: func(earlier int) {
		if passes.Add(1) == 1 {
			p.inflight[earlier].Store(true)
			if n, err := stdoutR.Read(make([]byte, 8)); err != nil || n != 2 {
				t.Errorf("the earlier reader took %d bytes, %v; want the 2 the child wrote", n, err)
			}
			go func() {
				delivered.Store(true)
				p.inflight[earlier].Store(false)
				p.handOff(earlier)
			}()
		}
	}}
	claimed, ok := p.awaitTurn(1, stderrR)
	if !claimed || !ok {
		t.Fatalf("awaitTurn = (%v, %v), want the turn claimed", claimed, ok)
	}
	if !delivered.Load() {
		t.Fatal("the later reader claimed its turn before the earlier reader delivered the bytes it took")
	}
}

// A reader that waits for an earlier reader sleeps until that reader delivers: it does not ask the kernel again and again meanwhile. Each question is a
// pass through AfterPeek, so a reader that polled would pass thousands of times in the time this test waits.
func TestAwaitTurnSleepsWhileAnEarlierReaderHoldsOutput(t *testing.T) {
	stdoutR, _ := newPipe(t)
	stderrR, stderrW := newPipe(t)
	if _, err := stderrW.WriteString("cd"); err != nil {
		t.Fatal(err)
	}
	var passes atomic.Int32
	asked := make(chan struct{}, 8)
	p := newPipes([]*os.File{stdoutR, stderrR}, nil, nil)
	// The earlier reader has taken bytes and not delivered them yet.
	p.inflight[0].Store(true)
	p.hooks = &Hooks{AfterPeek: func(int) {
		passes.Add(1)
		asked <- struct{}{}
	}}
	type turn struct{ claimed, ok bool }
	result := make(chan turn, 1)
	go func() {
		claimed, ok := p.awaitTurn(1, stderrR)
		result <- turn{claimed, ok}
	}()
	<-asked
	select {
	case got := <-result:
		t.Fatalf("awaitTurn returned %+v while the earlier reader held its bytes", got)
	case <-time.After(100 * time.Millisecond):
	}
	if got := passes.Load(); got != 1 {
		t.Fatalf("the waiting reader asked %d times; want 1: it must sleep until the earlier reader delivers", got)
	}
	p.inflight[0].Store(false)
	p.handOff(0)
	if got := <-result; !got.claimed || !got.ok {
		t.Fatalf("awaitTurn = %+v, want the turn claimed after the delivery", got)
	}
}

// Wait closes the pipes and releases the readers: one that waits for an earlier reader sees its own pipe gone and stops.
func TestAwaitTurnStopsWhenWaitReleasesThePipes(t *testing.T) {
	stdoutR, _ := newPipe(t)
	stderrR, stderrW := newPipe(t)
	if _, err := stderrW.WriteString("cd"); err != nil {
		t.Fatal(err)
	}
	asked := make(chan struct{}, 8)
	p := newPipes([]*os.File{stdoutR, stderrR}, nil, &Hooks{AfterPeek: func(int) { asked <- struct{}{} }})
	p.inflight[0].Store(true)
	type turn struct{ claimed, ok bool }
	result := make(chan turn, 1)
	go func() {
		claimed, ok := p.awaitTurn(1, stderrR)
		result <- turn{claimed, ok}
	}()
	<-asked
	_ = stderrR.Close()
	close(p.released)
	if got := <-result; got.claimed || got.ok {
		t.Fatalf("awaitTurn = %+v, want the closed pipe reported", got)
	}
}

// An idle pipe is waited for without a clock: a write wakes the reader, and so does closing the pipe.
func TestPollReadableWaitsForTheWriteOrTheClose(t *testing.T) {
	for _, step := range []string{"write", "close"} {
		r, w := newPipe(t)
		type answer struct{ readable, closed bool }
		result := make(chan answer, 1)
		go func() {
			readable, closed := pollReadable(r, 3_600_000)
			result <- answer{readable, closed}
		}()
		select {
		case got := <-result:
			t.Fatalf("%s: pollReadable returned %+v with nothing to read", step, got)
		case <-time.After(50 * time.Millisecond):
		}
		want := answer{readable: true}
		if step == "write" {
			if _, err := w.WriteString("x"); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = r.Close()
			want = answer{closed: true}
		}
		if got := <-result; got != want {
			t.Fatalf("%s: pollReadable = %+v, want %+v", step, got, want)
		}
	}
}

// A child that writes stdout and then stderr is seen in that order however the scheduler runs the readers: the later reader waits for the earlier one's
// delivery, and the delivery is what wakes it. The test keeps two goroutines per P busy, writes a stdout chunk and then a stderr chunk 50 times, and checks that each
// stdout chunk was delivered before the stderr chunk written after it.
func TestPipesDeliverStdoutBeforeStderrUnderLoad(t *testing.T) {
	stdoutR, stdoutW := newPipe(t)
	stderrR, stderrW := newPipe(t)
	stop := make(chan struct{})
	var load sync.WaitGroup
	for range 2 * runtime.GOMAXPROCS(0) {
		load.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = n * n
			}
		})
	}
	defer func() { close(stop); load.Wait() }()
	delivered := make(chan int, 2)
	pipes := Start([]*os.File{stdoutR, stderrR}, func(index int, _ []byte) { delivered <- index }, nil)
	const rounds = 50
	for round := range rounds {
		if _, err := stdoutW.WriteString("out\n"); err != nil {
			t.Fatal(err)
		}
		if _, err := stderrW.WriteString("err\n"); err != nil {
			t.Fatal(err)
		}
		first, second := <-delivered, <-delivered
		if first != 0 || second != 1 {
			t.Fatalf("round %d: delivered pipe %d then pipe %d, want stdout (0) before stderr (1)", round, first, second)
		}
	}
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if !pipes.Wait(func() bool { return false }) {
		t.Fatal("Wait reports a pipe that did not end")
	}
}
