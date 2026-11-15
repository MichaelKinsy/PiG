//go:build unix

package workers

import (
	"reflect"
	"sync"
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
