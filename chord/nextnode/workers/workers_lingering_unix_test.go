//go:build unix

package workers

import (
	"os"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// pi: packages/chord/src/node/workers/workers.ts
// A worker's streams are read on after its exit while a process outside its tree holds them (WorkerProcess.Exited), so its
// output and its control messages can come after the component handled its exit, forgot its address and started another
// worker for it. Pi has no test of it. Its listeners are the closures of the fork, the same before and after `exit`
// (workers.ts #fork): `onOutput(address, stream, chunk)` still runs, and `#control(routed, child, message)` still records on
// the child it was made for, which the address's record no longer has. The Go component does the same, and ends what it
// still reads when it closes, where Node's handles end with the server's process.

// outsideTree is the pid of the nth process outside its tree that a worker of f reported, killed at the test's end.
func (f *fleet) outsideTree(t *testing.T, nth int) int {
	t.Helper()
	pattern := regexp.MustCompile(`grandchild (\d+)`)
	var pids []int
	until(t, func() bool {
		pids = pids[:0]
		for _, line := range f.output.all() {
			for _, match := range pattern.FindAllStringSubmatch(line, -1) {
				pid, _ := strconv.Atoi(match[1])
				pids = append(pids, pid)
			}
		}
		return len(pids) >= nth
	})
	pid := pids[nth-1]
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// startWorker starts the worker of session id as the route does, with nothing of the server's host on its link: the frames
// it gets are collected. It returns the worker's child.
func (f *fleet) startWorker(t *testing.T, id string) (*child, *collector[string]) {
	t.Helper()
	resolved, err := f.workers.resolve(session(id), background)
	if err != nil {
		t.Fatalf("%v; what the workers reported: %s", err, f.reportedErrors())
	}
	frames := &collector[string]{}
	resolved.Link.OnMessage(func(message []byte) { frames.add(string(message)) })
	var started *child
	f.workers.loop.Run(func() { started = f.workers.addresses[session(id).Key].child })
	if started == nil {
		t.Fatal("the address has no child after its start")
	}
	return started, frames
}

// stopWorker stops the worker of session id and waits until its address is forgotten: its record is dropped.
func (f *fleet) stopWorker(t *testing.T, id string, stopped *child) {
	t.Helper()
	before := len(f.forgotten.all())
	if err := f.workers.Stop(session(id), background); err != nil {
		t.Fatal(err)
	}
	<-stopped.process.Exited()
	until(t, func() bool { return len(f.forgotten.all()) > before })
}

// What an exited worker's streams tell later is told of it, not of the worker that followed it. A process outside the
// first worker's tree holds its stdout and its channel; the worker is stopped, its address forgotten, and a second worker
// started for the same address. Then the holder writes: its output reaches the app's OnOutput with the address, as Pi's
// `onOutput` does; what it writes on the channel reaches no one: the link was closed at the exit (Pi f57cd9484, `link.end()`
// disconnects the channel), so neither a frame of the first worker's link nor a `fatal` of its child is told. The second
// worker has no cause, no frame of it, and runs: nothing is reported and the state is the second worker's.
func TestWhatAnExitedWorkersStreamsTellLaterIsToldOfItNotOfTheWorkerThatFollowedIt(t *testing.T) {
	f := newFleet(t, fleetOptions{env: map[string]string{lastWordsGrandchild: "before", outsideTreeHolds: "stdout,channel"}})
	first, firstFrames := f.startWorker(t, "S1")
	holder := f.outsideTree(t, 1)
	f.stopWorker(t, "S1", first)
	second, secondFrames := f.startWorker(t, "S1")
	f.outsideTree(t, 2)
	if second == first || second.process.Pid == first.process.Pid {
		t.Fatal("the second start gave the first worker")
	}
	if !alive(holder) || !first.process.stillRead() {
		t.Fatalf("holder alive = %v, the first worker still read = %v: it proved nothing", alive(holder), first.process.stillRead())
	}
	if err := syscall.Kill(holder, syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	late := strings.TrimSpace(holderLate)
	until(t, func() bool { return slices.Contains(f.output.all(), "session|S1 "+late) })
	var firstCause, secondCause *cause
	var current *child
	var status WorkerStatus
	f.workers.loop.Run(func() {
		firstCause, secondCause = first.cause, second.cause
		current, status = f.workers.addresses[session("S1").Key].child, second.status
	})
	if first.process.channel.Connected() || slices.Contains(firstFrames.all(), holderLate) || (firstCause != nil && firstCause.public != nil && firstCause.public.Message == holderLate) {
		t.Errorf("the first worker's channel is connected = %v, its frames = %v, its cause = %+v: what its holder writes on the channel after the exit is told to no one", first.process.channel.Connected(), firstFrames.all(), firstCause)
	}
	if secondCause != nil || current != second || status == WorkerStopping || !alive(second.process.Pid) {
		t.Errorf("the second worker: cause = %+v, the address's child = %v, status = %q, alive = %v; want none, true, not stopping, true", secondCause, current == second, status, alive(second.process.Pid))
	}
	if slices.Contains(secondFrames.all(), holderLate) {
		t.Error("the second worker's link got the frame written on the first worker's channel")
	}
	if reported := f.errors.all(); len(reported) != 0 {
		t.Errorf("reported = %s, want nothing", f.reportedErrors())
	}
	if state := f.stateOf("S1"); state == nil || state.Pid != second.process.Pid || state.Status == WorkerFailed {
		t.Errorf("state = %+v, want the second worker's (pid %d), not failed", state, second.process.Pid)
	}
}

// streamReaders is how many goroutines of this process read or write a worker process's streams.
func streamReaders() int {
	buffer := make([]byte, 1<<20)
	for {
		size := runtime.Stack(buffer, true)
		if size < len(buffer) {
			buffer = buffer[:size]
			break
		}
		buffer = make([]byte, 2*len(buffer))
	}
	count := 0
	for goroutine := range strings.SplitSeq(string(buffer), "\n\n") {
		if strings.Contains(goroutine, "workers.(*WorkerProcess).pump") || strings.Contains(goroutine, "workers.(*channel).run") || strings.Contains(goroutine, "workers.(*channel).write") {
			count++
		}
	}
	return count
}

// openDescriptors is how many descriptors this process has open.
func openDescriptors(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// Close ends what is still read of the workers that exited. A process outside a worker's tree holds its stdout, its stderr
// and its channel; the worker is stopped, its link closed at the exit (the channel is disconnected), and this side goes on
// reading the two streams (their goroutines and descriptors are kept, as Node keeps a child's handles). The component's Close closes them although the holder lives: no goroutine reads
// a worker's stream any more, and no more descriptors are open than before the worker started.
func TestCloseEndsWhatIsStillReadOfAnExitedWorkerWhoseStreamsAProcessOutsideItsTreeHolds(t *testing.T) {
	f := newFleet(t, fleetOptions{env: map[string]string{lastWordsGrandchild: "before", outsideTreeHolds: "stdout,stderr,channel"}})
	if readers := streamReaders(); readers != 0 {
		t.Fatalf("%d goroutines read a worker's streams before any worker started", readers)
	}
	descriptors := openDescriptors(t)
	worker, _ := f.startWorker(t, "S1")
	holder := f.outsideTree(t, 1)
	f.stopWorker(t, "S1", worker)
	var lingering int
	f.workers.loop.Run(func() { lingering = len(f.workers.lingering) })
	if readers := streamReaders(); !alive(holder) || !worker.process.stillRead() || readers < 2 || lingering != 1 {
		t.Fatalf("after the exit: holder alive = %v, still read = %v, %d goroutines on its streams, %d processes kept; want true, true, the two stream readers, 1: it proved nothing", alive(holder), worker.process.stillRead(), readers, lingering)
	}
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return streamReaders() == 0 && openDescriptors(t) <= descriptors })
	if !alive(holder) {
		t.Error("the process outside the worker's tree is not alive: the streams ended by themselves")
	}
	if worker.process.stillRead() || worker.process.channel.Connected() {
		t.Errorf("after Close: still read = %v, connected = %v; want false, false", worker.process.stillRead(), worker.process.channel.Connected())
	}
	for index, output := range worker.process.outputs {
		if _, err := output.Stat(); err == nil {
			t.Errorf("output %d is open after Close", index)
		}
	}
}
