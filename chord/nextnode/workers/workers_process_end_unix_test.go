//go:build unix

package workers

import (
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// pi: packages/chord/src/node/workers/process.ts
// Ports packages/chord/test/node/workers/process.test.ts: "closes its link at its exit, before the exit is told, as neither a
// lost nor a closed link" (Pi main f57cd9484).
//
// The fixture child's grandchild holds the channel (it inherits descriptor 3, as Pi's child.ts has it spawned with the IPC
// channel): when only the leader ends, the channel closes by itself only once the tree was killed, at the exit. Unix only: a
// Windows worker's channel is a named pipe no grandchild inherits.

func TestAWorkerProcessClosesItsLinkAtItsExitBeforeTheExitIsToldAsNeitherALostNorAClosedLink(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	started := make(chan struct{})
	var once sync.Once
	child, err := NewWorkerProcess(
		ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("child"), OnOutput: func(string, []byte) {}},
		ProcessEvents{
			Control:    func(FromWorker) { record("started"); once.Do(func() { close(started) }) },
			LinkLost:   func() { record("link lost") },
			LinkClosed: func() { record("link closed") },
			Exited:     func(Exit) { record("exited") },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	child.Link.OnClose(func() { record("closed") })
	<-started
	// Only the leader ends: its grandchild holds the channel.
	if err := syscall.Kill(child.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	<-child.Exited()
	exit := child.Exit()
	if exit.Code != nil || exit.Signal == nil || *exit.Signal != "SIGTERM" {
		t.Fatalf("exit = %+v, want {code: null, signal: SIGTERM}", exit)
	}
	// What the tree's end still tells is told once the streams ended: nothing reads the child's any more.
	until(t, func() bool { return !child.stillRead() })
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"started", "closed", "exited"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

// Go-only: Pi has no counterpart of `ending` (process.ts:79-81: its kill of the group and its `link.end()` are one turn of the event
// loop, so nothing reaches the link between them). Go's exit waits for the output's pumps between the kill and the release, and here
// the kill is what ends the channel's last holder (the fixture's grandchild, in the tree): a close of the channel that lands in that
// wait is the exit's to tell, not a closed link told before it. The first output pump to tell a chunk is held in its handler, so the exit
// stays in the wait until the channel's reader and the other pump ended.
func TestAWorkerProcessLinkClosedByTheKillOfItsTreeWhileItsExitWaitsForItsOutputIsNeitherALostNorAClosedLink(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	started := make(chan struct{})
	var once sync.Once
	var holding atomic.Bool
	held, release := make(chan struct{}), make(chan struct{})
	letGo := sync.OnceFunc(func() { close(release) })
	t.Cleanup(letGo)
	child, err := NewWorkerProcess(
		ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("child"), OnOutput: func(string, []byte) {
			// The first chunk of either stream holds its pump; the other stream's goes on (a `sync.Once` would hold it too).
			if holding.CompareAndSwap(false, true) {
				close(held)
				<-release
			}
		}},
		ProcessEvents{
			Control:    func(FromWorker) { record("started"); once.Do(func() { close(started) }) },
			LinkLost:   func() { record("link lost") },
			LinkClosed: func() { record("link closed") },
			Exited:     func(Exit) { record("exited") },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	child.Link.OnClose(func() { record("closed") })
	<-started
	<-held
	if err := syscall.Kill(child.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// The channel's reader and the stderr pump end with the tree's kill; the stdout pump is held: nothing else reads.
	until(t, func() bool { return child.reading.Load() == 1 })
	letGo()
	<-child.Exited()
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"started", "closed", "exited"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
