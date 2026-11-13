//go:build unix

package workers

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
)

// pi: packages/chord/src/node/workers/process.ts
// What a worker wrote before it ended is told before its exit, whichever of the two this side sees first. Pi has no test of
// it: Node gives it (the messages a child wrote are read from its channel and emitted before its `exit` event: both are in
// the event loop's poll, and libuv runs the signal that reports the exit after the reads of the same poll). In Go the
// channel's reader and the exit's waiter are two goroutines, so the order is the process's own to keep, and these tests
// hold each side in turn. Those that tell what did NOT happen yet run in a synctest bubble for its Wait: it returns once
// this side did everything it can do while one of its goroutines is held, which nothing of the process itself tells (the
// fault was an exit told too early; the cure is an exit that waits). Unix only: go-winio serves its pipes from a goroutine
// of its own that never ends, which a bubble cannot hold, and a session of its own is the Unix way out of a worker's tree.

func init() { startOutsideTree = startSessionGrandchild }

// startSessionGrandchild starts a process in a session of its own, which the kill of the worker's group does not reach, and
// writes its pid to stdout. It inherits what the worker has that a process it starts inherits, its output aside.
func startSessionGrandchild() {
	grandchild := exec.Command(os.Args[0])
	grandchild.Env = append(os.Environ(), fixtureEnv+"=grandchild")
	grandchild.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := grandchild.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_, _ = fmt.Fprintf(os.Stdout, "grandchild %d\n", grandchild.Process.Pid)
}

// lastWordsTold is what a worker process told, in order, and what the workers component records for its exit: the reason
// of a `fatal` told before the exit, else publicExit's fixed text (workers_workers.go `exited`).
type lastWordsTold struct {
	mu       sync.Mutex
	control  []string
	frames   []string
	output   string
	lost     int
	exits    int
	atExit   lastWordsSeen
	recorded string
}

// lastWordsSeen is what was told so far.
type lastWordsSeen struct {
	Control []string
	Frames  []string
}

// lastWordsWhole is the whole of what the fixture tells.
var lastWordsWhole = lastWordsSeen{Control: []string{string(FromWorkerStarted), string(FromWorkerFatal)}, Frames: []string{lastWordsAnswer}}

func (told *lastWordsTold) seen() (lastWordsSeen, int) {
	told.mu.Lock()
	defer told.mu.Unlock()
	return lastWordsSeen{Control: append([]string(nil), told.control...), Frames: append([]string(nil), told.frames...)}, told.exits
}

// grandchild is the pid the fixture wrote, once it did.
func (told *lastWordsTold) grandchild() (int, bool) {
	told.mu.Lock()
	defer told.mu.Unlock()
	match := regexp.MustCompile(`grandchild (\d+)\n`).FindStringSubmatch(told.output)
	if match == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(match[1])
	return pid, err == nil
}

// startLastWords starts the fixture. deliver stands where the component's handler waits for its loop: it runs on the
// channel's reader before a control message is taken (kept); output runs on the pump that tells a stdout chunk. A test
// holds that goroutine there.
func startLastWords(t *testing.T, told *lastWordsTold, env map[string]string, deliver func(FromWorker), output func()) *WorkerProcess {
	t.Helper()
	var reason *CrossedError
	process, err := NewWorkerProcess(
		ForkOptions{
			Entry: fixtureEntry(t),
			Env:   withEnv(fixtureEnvironment("lastwords"), env),
			OnOutput: func(stream string, chunk []byte) {
				if stream != "stdout" {
					return
				}
				told.mu.Lock()
				told.output += string(chunk)
				told.mu.Unlock()
				if output != nil {
					output()
				}
			},
		},
		ProcessEvents{
			Control: func(message FromWorker) {
				if deliver != nil {
					deliver(message)
				}
				told.mu.Lock()
				defer told.mu.Unlock()
				told.control = append(told.control, string(message.Kind))
				if message.Kind == FromWorkerFatal {
					reason = message.Error
				}
			},
			LinkLost: func() {
				told.mu.Lock()
				defer told.mu.Unlock()
				told.lost++
			},
			LinkClosed: func() {},
			Exited: func(exit Exit) {
				told.mu.Lock()
				defer told.mu.Unlock()
				told.exits++
				told.atExit = lastWordsSeen{Control: append([]string(nil), told.control...), Frames: append([]string(nil), told.frames...)}
				told.recorded = publicExit(exit, &child{status: WorkerStarting}).Message
				if reason != nil {
					told.recorded = reason.Message
				}
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	process.Link.OnMessage(func(message []byte) {
		told.mu.Lock()
		defer told.mu.Unlock()
		told.frames = append(told.frames, string(message))
	})
	return process
}

// expectExitOne expects the fixture's exit told once: code 1.
func expectExitOne(t *testing.T, process *WorkerProcess, told *lastWordsTold) {
	t.Helper()
	<-process.Exited()
	if exit := process.Exit(); exit.Code == nil || *exit.Code != 1 || exit.Signal != nil {
		t.Errorf("exit = %+v, want {code: 1, signal: null}", exit)
	}
	if _, exits := told.seen(); exits != 1 {
		t.Errorf("exits told = %d, want 1", exits)
	}
}

// expectLastWords expects the whole of what the fixture wrote told before its exit, and its reason recorded.
func expectLastWords(t *testing.T, process *WorkerProcess, told *lastWordsTold) {
	t.Helper()
	expectExitOne(t, process, told)
	told.mu.Lock()
	defer told.mu.Unlock()
	if !reflect.DeepEqual(told.atExit, lastWordsWhole) {
		t.Errorf("told before the exit = %+v, want %+v: the holder's answer and the `fatal` are told before the exit", told.atExit, lastWordsWhole)
	}
	if told.recorded != lastWordsReason {
		t.Errorf("the exit's recorded error = %q, want the reason of its `fatal`, %q", told.recorded, lastWordsReason)
	}
}

// Unread at the exit: the child wrote its last messages and ended while the channel's reader is held on `started`, the
// answer and the `fatal` still on the connection. The exit is not told, and the connection not closed, before the reader
// took them: the holder has its answer at the exit. (What is told of the last message is the next test's.)
func TestAWorkerProcessTellsItsExitAfterItsChannelsReaderTookWhatItsChildWrote(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		told := &lastWordsTold{}
		held, release := make(chan struct{}), make(chan struct{})
		process := startLastWords(t, told, nil, func(message FromWorker) {
			if message.Kind == FromWorkerStarted {
				close(held)
				<-release
			}
		}, nil)
		<-held
		// The child wrote the rest and ended, and its exit was handled as far as it goes while the reader is held.
		synctest.Wait()
		seen, exits := told.seen()
		close(release)
		if exits != 0 {
			t.Errorf("the exit was told while the channel's reader had told %+v of what the child wrote", seen)
		}
		expectExitOne(t, process, told)
		told.mu.Lock()
		defer told.mu.Unlock()
		if !reflect.DeepEqual(told.atExit.Frames, lastWordsWhole.Frames) {
			t.Errorf("frames told before the exit = %q, want %q: what was unread at the exit is read, not dropped", told.atExit.Frames, lastWordsWhole.Frames)
		}
	})
}

// Read, not yet told, at the exit: the reader took the child's last message, its `fatal` (nothing is unread), and waits to
// hand it over, as the component's handler waits for its loop, when the child's exit is seen. The exit is told after the
// `fatal`: its error is the reason, not the fixed text of a worker that ended without one.
func TestAWorkerProcessTellsItsExitAfterWhatItsChannelsReaderTookIsTold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		told := &lastWordsTold{}
		held, release := make(chan struct{}), make(chan struct{})
		process := startLastWords(t, told, nil, func(message FromWorker) {
			if message.Kind == FromWorkerFatal {
				close(held)
				<-release
			}
		}, nil)
		<-held
		synctest.Wait()
		seen, exits := told.seen()
		close(release)
		want := lastWordsSeen{Control: []string{string(FromWorkerStarted)}, Frames: lastWordsWhole.Frames}
		if exits != 0 || !reflect.DeepEqual(seen, want) {
			t.Errorf("while the `fatal` is read and not yet told: exits told = %d, told = %+v; want no exit and %+v", exits, seen, want)
		}
		expectLastWords(t, process, told)
	})
}

// The reader first: the channel's reader took and told everything the child wrote (its channel's end too) while the exit
// is held on the child's output, which a pump is still telling. Nothing changes: the exit is told once the pump told its part.
func TestAWorkerProcessTellsItsExitAfterItsOutputWhenItsChannelsReaderEndedFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		told := &lastWordsTold{}
		held, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		process := startLastWords(t, told, nil, nil, func() {
			once.Do(func() {
				close(held)
				<-release
			})
		})
		<-held
		// The child ended and the reader reached its channel's end; the exit waits for the pump.
		synctest.Wait()
		seen, exits := told.seen()
		close(release)
		if exits != 0 || !reflect.DeepEqual(seen, lastWordsWhole) {
			t.Errorf("while a pump is held: exits told = %d, told = %+v; want no exit and %+v", exits, seen, lastWordsWhole)
		}
		expectLastWords(t, process, told)
	})
}

// This side's close does not wait for the reader: the link is closed (the heartbeat's close, a protocol error) while the
// reader is held telling a message, and Close returns with the link lost told. The exit then waits only for that message's
// handler; nothing more is read.
func TestAWorkerProcessLinkClosedByThisSideWhileItsChannelsReaderIsHeldDoesNotWaitForIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		told := &lastWordsTold{}
		held, release := make(chan struct{}), make(chan struct{})
		process := startLastWords(t, told, nil, func(message FromWorker) {
			if message.Kind == FromWorkerStarted {
				close(held)
				<-release
			}
		}, nil)
		<-held
		process.Link.Close()
		told.mu.Lock()
		lost := told.lost
		told.mu.Unlock()
		if lost != 1 || process.channel.Connected() {
			t.Errorf("after Close with the reader held: link lost told %d times, connected = %v; want 1 and false", lost, process.channel.Connected())
		}
		close(release)
		expectExitOne(t, process, told)
		if seen, _ := told.seen(); !reflect.DeepEqual(seen.Control, []string{string(FromWorkerStarted)}) || len(seen.Frames) != 0 {
			t.Errorf("told = %+v, want only the `started` that was being told at the close", seen)
		}
	})
}

// exitedWithin reports whether process's exit was told within the wait of until.
func exitedWithin(process *WorkerProcess) func() bool {
	return func() bool {
		select {
		case <-process.Exited():
			return true
		default:
			return false
		}
	}
}

// outsideTree is the fixture's process outside its tree, killed at the test's end.
func outsideTree(t *testing.T, told *lastWordsTold) int {
	t.Helper()
	until(t, func() bool { _, ok := told.grandchild(); return ok })
	pid, _ := told.grandchild()
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// A process the worker started cannot hold its channel: a worker opens it as a descriptor nothing it starts inherits
// (openProcessChannel), so a process in a session of its own, which outlives the worker's tree, does not keep the channel's
// reader, and with it the exit, waiting.
func TestAWorkerProcessExitIsNotHeldByAProcessOutsideItsTreeThatItStartedAfterItOpenedItsChannel(t *testing.T) {
	told := &lastWordsTold{}
	process := startLastWords(t, told, map[string]string{lastWordsGrandchild: "after"}, nil, nil)
	grandchild := outsideTree(t, told)
	until(t, exitedWithin(process))
	expectLastWords(t, process, told)
	if !alive(grandchild) {
		t.Error("the process outside the worker's tree is not alive: it proved nothing")
	}
}

// The wait for the channel's end ends when this side closes the link. A process outside the worker's tree that holds the
// channel (started before the worker opened it: a worker of another making) keeps the reader waiting after the worker
// ended; the link's close (the heartbeat's, in the component) ends the read, and the exit is told.
func TestAWorkerProcessExitHeldByAProcessOutsideItsTreeThatHoldsItsChannelIsToldWhenThisSideClosesTheLink(t *testing.T) {
	told := &lastWordsTold{}
	process := startLastWords(t, told, map[string]string{lastWordsGrandchild: "before"}, nil, nil)
	grandchild := outsideTree(t, told)
	until(t, func() bool { return !alive(process.Pid) })
	process.Link.Close()
	until(t, exitedWithin(process))
	expectLastWords(t, process, told)
	if !alive(grandchild) {
		t.Error("the process outside the worker's tree is not alive: it proved nothing")
	}
}
