//go:build unix

package workers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// pi: packages/chord/src/next-node/workers/process.ts
// A process outside the worker's tree that holds its output and writes it without a pause (a process in a session of its
// own, which the kill of the worker's group does not reach) holds neither the worker's exit nor the close of its component.
// Pi tells `exit` when the process ended and never waits for the stream's end (`child.once("exit")`, process.ts:79 at
// f9a26365568127461c47a07a37dcb42c140a3ac5, :85 at 747c217b81980206063a89e46645b362de7ac8b1), and its `data` listeners go on
// hearing what the stream gets. Pi has no such test: Node's reads of one poll never run on without end.

// holderFloods makes the process outside the tree write the outputs it holds without a pause from the moment it is ready.
const holderFloods = "CHORD_WORKERS_FIXTURE_HOLDER_FLOODS"

// flood is that process's writing: a page-sized line (the most a pipe takes in one atomic write) over and over.
func flood(outputs []*os.File) {
	if len(outputs) == 0 {
		return
	}
	block := append(bytes.Repeat([]byte{'x'}, 4095), '\n')
	for {
		for _, output := range outputs {
			_, _ = output.Write(block)
		}
	}
}

// rawStream is a stream whose descriptor can be asked about (a pipe's end, a socket).
type rawStream interface {
	SyscallConn() (syscall.RawConn, error)
}

// queuedBytes is how many bytes a stream's read end holds unread, or -1 once it is closed (or when the platform cannot say).
func queuedBytes(stream rawStream) int {
	raw, err := stream.SyscallConn()
	if err != nil {
		return -1
	}
	return streamHolds(raw)
}

// goroutineIn reports whether a goroutine of this process has every fragment in its stack.
func goroutineIn(fragments ...string) bool {
	buffer := make([]byte, 1<<20)
	for {
		size := runtime.Stack(buffer, true)
		if size < len(buffer) {
			buffer = buffer[:size]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	for goroutine := range strings.SplitSeq(string(buffer), "\n\n") {
		found := true
		for _, fragment := range fragments {
			found = found && strings.Contains(goroutine, fragment)
		}
		if found {
			return true
		}
	}
	return false
}

// The exit of a worker whose output a process outside its tree keeps full is told, with what the stream held when the worker
// ended. The outside process writes without a pause and the app's OnOutput is slow (it returns once the stream holds more),
// so every read after the end finds data: a drain that settled on finding the stream empty never would. The exit must not
// wait for more than what the stream held, which is at most a pipe's capacity, and what the process wrote before it ended
// is told before it; what the outside process writes after it is told too, as before.
func TestAWorkerProcessExitIsNotHeldByAProcessOutsideItsTreeThatKeepsItsOutputFull(t *testing.T) {
	const (
		// fuse: the bytes told after the worker ended, without its exit, after which the stream is not going to run empty.
		fuse = 2 << 20
		// held: what may be told after the end and before the exit: the pipe's capacity (64 KiB) with a wide margin.
		held = 256 << 10
	)
	var (
		afterEnd, afterExit atomic.Int64
		started             atomic.Pointer[WorkerProcess]
		output              atomic.Pointer[os.File]
		exitTold            atomic.Bool
		fuseOnce            sync.Once
		mu                  sync.Mutex
		head, atExit        []byte
		holder              int
		holderTail          []byte
	)
	holderLine := regexp.MustCompile(`grandchild (\d+)\n`)
	// pending: the worker's stdout drain was told the process ended (its count of what the stream holds is made then) and has
	// not settled. What is told before the end is not told after it, however much a reader that nothing preempts gets through;
	// what is told after the settlement is the stream read on, which the exit does not wait for (it waits to be scheduled).
	pending := func() bool {
		process := started.Load()
		if process == nil {
			return false
		}
		told := process.drains[0]
		select {
		case <-told.settled:
			return false
		default:
			return told.ended.Load()
		}
	}
	fused := make(chan struct{})
	var settledAtFuse []bool
	holderPid := func() (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		return holder, holder != 0
	}
	killHolder := func() {
		if pid, ok := holderPid(); ok {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	process, err := NewWorkerProcess(
		ForkOptions{
			Entry: fixtureEntry(t),
			Env:   withEnv(fixtureEnvironment("lastwords"), map[string]string{lastWordsGrandchild: "before", outsideTreeHolds: "stdout", holderFloods: "1"}),
			OnOutput: func(stream string, chunk []byte) {
				if stream != "stdout" {
					return
				}
				mu.Lock()
				if len(head) < 512<<10 {
					head = append(head, chunk...)
				}
				// The holder writes without a pause from the moment it is ready, and the worker writes the holder's pid only after it hears so: under load the flood is
				// in the stream ahead of that line by more than head holds, so the pid is taken from the stream as it is told, not from head.
				if holder == 0 {
					window := slices.Concat(holderTail, chunk)
					if match := holderLine.FindSubmatch(window); match != nil {
						for _, digit := range match[1] {
							holder = holder*10 + int(digit-'0')
						}
					} else {
						holderTail = bytes.Clone(window[max(0, len(window)-32):])
					}
				}
				mu.Unlock()
				switch {
				case exitTold.Load():
					afterExit.Add(int64(len(chunk)))
				case pending():
					if afterEnd.Add(int64(len(chunk))) > fuse {
						fuseOnce.Do(func() {
							if process := started.Load(); process != nil {
								for _, told := range process.drains {
									select {
									case <-told.settled:
										settledAtFuse = append(settledAtFuse, true)
									default:
										settledAtFuse = append(settledAtFuse, false)
									}
								}
							}
							killHolder()
							close(fused)
						})
						return
					}
					// Slow: it returns once the stream holds more, so that the read after it finds it.
					if file := output.Load(); file != nil {
						for queuedBytes(file) == 0 {
							select {
							case <-fused:
								return
							default:
							}
							runtime.Gosched()
						}
					}
				}
			},
		},
		ProcessEvents{
			Exited: func(Exit) {
				mu.Lock()
				atExit = bytes.Clone(head)
				mu.Unlock()
				exitTold.Store(true)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		killHolder()
		process.release()
	})
	started.Store(process)
	output.Store(process.outputs[0])
	select {
	case <-process.Exited():
	case <-fused:
		<-process.Exited()
		t.Fatalf("the exit was told only when the holder was killed: %d bytes of what a process outside the tree wrote were told after the worker ended without it, and each read found more (drains settled at that point: %v)", afterEnd.Load(), settledAtFuse)
	}
	if exit := process.Exit(); exit.Code == nil || *exit.Code != 1 || exit.Signal != nil {
		t.Errorf("exit = %+v, want {code: 1, signal: null}", exit)
	}
	if told := afterEnd.Load(); told > held {
		t.Errorf("%d bytes were told after the worker ended and before its exit, want what the stream held, at most %d", told, held)
	}
	mu.Lock()
	written := string(atExit)
	mu.Unlock()
	for _, want := range []string{lastWordsOutput, holderReady} {
		if !strings.Contains(written, want) {
			t.Errorf("told before the exit: %.80q..., want %q, written before the worker ended", written, want)
		}
	}
	pid, ok := holderPid()
	if !ok || !alive(pid) {
		t.Fatalf("holder %d alive = %v: it proved nothing", pid, ok && alive(pid))
	}
	// What the holder writes after the exit is still told.
	until(t, func() bool { return afterExit.Load() >= 1<<16 })
}

// Close waits for the readers of an exited worker's streams: a reader that is in the app's OnOutput with a chunk a process
// outside the worker's tree wrote after the exit is not left running when Close returns. Close is bounded by its context,
// which ends the wait of an OnOutput that does not return; the reader returns once it does, as the stream was closed.
func TestCloseWaitsForTheReaderOfAnExitedWorkersOutputThatIsInTheAppsOnOutput(t *testing.T) {
	entered, gate := make(chan struct{}), make(chan struct{})
	var enter, open sync.Once
	letGo := func() { open.Do(func() { close(gate) }) }
	f := newFleet(t, fleetOptions{
		env: map[string]string{lastWordsGrandchild: "before", outsideTreeHolds: "stdout,stderr,channel"},
		onOutput: func(stream string, chunk []byte) bool {
			if stream == "stdout" && strings.Contains(string(chunk), strings.TrimSpace(holderLate)) {
				enter.Do(func() { close(entered) })
				<-gate
			}
			return true
		},
	})
	t.Cleanup(letGo)
	worker, _ := f.startWorker(t, "S1")
	holder := f.outsideTree(t, 1)
	f.stopWorker(t, "S1", worker)
	if err := syscall.Kill(holder, syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	<-entered
	if !alive(holder) || !worker.process.stillRead() {
		t.Fatalf("holder alive = %v, the worker's streams still read = %v: it proved nothing", alive(holder), worker.process.stillRead())
	}
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- f.workers.Close(ctx) }()
	for {
		select {
		case err := <-closed:
			t.Fatalf("Close returned (%v) while the reader of the exited worker's stdout was still in the app's OnOutput: it did not wait for the readers", err)
		default:
		}
		if goroutineIn("(*Workers).Close", "awaitReaders") {
			break
		}
		runtime.Gosched()
	}
	cancel()
	if err := <-closed; !errors.Is(err, context.Canceled) {
		t.Errorf("Close = %v after its context ended, want the context's cause", err)
	}
	letGo()
	until(t, func() bool { return !worker.process.stillRead() })
	if err := f.workers.Close(background); err != nil {
		t.Errorf("Close once the reader returned = %v, want nil", err)
	}
	if readers := streamReaders(); readers != 0 {
		t.Errorf("%d goroutines read a worker's stream after Close", readers)
	}
}
