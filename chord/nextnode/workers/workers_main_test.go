package workers

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/next-node/workers/main.test.ts: the worker's side: the start message, the lock, `started` on
// `connect`, `ready`; shutdown during startup, the parent's disconnect and SIGTERM each run the closers and exit; `fatal`
// redacted.

// lockSerial makes each started worker's lock name unique (Math.random() in Pi).
var lockSerial atomic.Int64

// startedWorker is `started(behaviour)`: the fixture worker forked and started with behaviour; its control messages and
// exit collected.
type startedWorker struct {
	child   *WorkerProcess
	mu      sync.Mutex
	control []FromWorker
	exit    *Exit
}

func (w *startedWorker) controlKinds() []FromWorkerKind {
	w.mu.Lock()
	defer w.mu.Unlock()
	kinds := make([]FromWorkerKind, len(w.control))
	for index, message := range w.control {
		kinds[index] = message.Kind
	}
	return kinds
}

func (w *startedWorker) messages() []FromWorker {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]FromWorker(nil), w.control...)
}

func (w *startedWorker) exited() *Exit {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exit
}

func (w *startedWorker) has(kind FromWorkerKind) bool {
	return slices.Contains(w.controlKinds(), kind)
}

func started(t *testing.T, behaviour workerBehaviour) *startedWorker {
	t.Helper()
	w := &startedWorker{}
	child, err := NewWorkerProcess(
		ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("worker"), OnOutput: func(string, []byte) {}},
		ProcessEvents{
			Control: func(message FromWorker) {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.control = append(w.control, message)
			},
			LinkLost:   func() {},
			LinkClosed: func() {},
			Exited: func(value Exit) {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.exit = &value
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	w.child = child
	t.Cleanup(child.Kill)
	child.Send(ToWorker{
		Kind:    ToWorkerStart,
		Address: fixtureSession.Address(jsonObject("id", "S1")),
		Start:   behaviour.json(),
		Scripts: []plugins.PluginScript{},
		Lock:    fmt.Sprintf("chord-test-main:%d:%d:%d", os.Getpid(), time.Now().UnixNano(), lockSerial.Add(1)),
	})
	return w
}

func TestTheWorkersSideSendsStartedWhenTheAppConnectsThenReadyWithItsLoadReportAndIdleState(t *testing.T) {
	worker := started(t, workerBehaviour{})
	until(t, func() bool { return worker.has(FromWorkerReady) })
	if kinds := worker.controlKinds(); !reflect.DeepEqual(kinds, []FromWorkerKind{FromWorkerStarted, FromWorkerReady}) {
		t.Fatalf("control kinds = %v, want [started ready]", kinds)
	}
	ready := worker.messages()[1]
	if !ready.Idle || ready.Report == nil || len(ready.Report.Plugins.Added) != 0 || len(ready.Report.Failed) != 0 {
		t.Fatalf("ready = %+v report=%+v, want idle with no plugin added and none failed", ready, ready.Report)
	}
	worker.child.Kill()
}

func TestTheWorkersSideShutsDownDuringItsStartupAbortsItRunsTheClosersRegisteredSoFarInReverseOrderExits0(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	worker := started(t, workerBehaviour{Hang: true, Log: log})
	until(t, func() bool { return worker.has(FromWorkerStarted) })
	worker.child.Send(ToWorker{Kind: ToWorkerShutdown})
	until(t, func() bool { return worker.exited() != nil })
	if exit := worker.exited(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v, want {code: 0, signal: null}", exit)
	}
	text, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "store\nhost\n" {
		t.Fatalf("closers ran as %q, want %q (reverse order)", text, "store\nhost\n")
	}
}

func TestTheWorkersSideShutsDownOnTheParentsDisconnect(t *testing.T) {
	gone := started(t, workerBehaviour{})
	until(t, func() bool { return gone.has(FromWorkerReady) })
	gone.child.Link.Close()
	until(t, func() bool { return gone.exited() != nil })
	if exit := gone.exited(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v, want {code: 0, signal: null}", exit)
	}
}

func TestTheWorkersSideSendsFatalWithTheRedactedErrorAndExits1(t *testing.T) {
	worker := started(t, workerBehaviour{Fatal: "no secret-key-42 here"})
	until(t, func() bool { return worker.exited() != nil })
	messages := worker.messages()
	want := []FromWorker{{Kind: FromWorkerFatal, Error: &CrossedError{Message: "no [redacted] here"}}}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("control = %+v, want %+v", messages, want)
	}
	if exit := worker.exited(); exit.Code == nil || *exit.Code != 1 {
		t.Fatalf("exit = %+v, want code 1", exit)
	}
}
