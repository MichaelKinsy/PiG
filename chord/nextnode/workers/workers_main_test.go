package workers

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// pi: packages/chord/src/next-node/workers/main.ts
// Ports packages/chord/test/next-node/workers/main.test.ts: the worker's side: the start message, the lock, `started` on
// `connect`, `ready`; shutdown during startup, the parent's disconnect and SIGTERM each run the closers and exit; `fatal`
// redacted.

// lockSerial makes each started worker's lock name unique (Math.random() in Pi).
var lockSerial atomic.Int64

// startedWorker is `started(behaviour)`: the fixture worker forked and started with behaviour; its control messages, link
// closing, output and exit collected.
type startedWorker struct {
	child   *WorkerProcess
	mu      sync.Mutex
	control []FromWorker
	// events are what the server side saw, in the order it saw it: each control message's kind, and linkClosed.
	events []string
	output strings.Builder
	exit   *Exit
}

// linkClosed is the event of the child closing its link to the server.
const linkClosed = "link closed"

func (w *startedWorker) seen() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.events)
}

func (w *startedWorker) printed(text string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Contains(w.output.String(), text)
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
		ForkOptions{Entry: fixtureEntry(t), Env: fixtureEnvironment("worker"), OnOutput: func(_ string, chunk []byte) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.output.Write(chunk)
		}},
		ProcessEvents{
			Control: func(message FromWorker) {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.control = append(w.control, message)
				w.events = append(w.events, string(message.Kind))
			},
			LinkLost: func() {},
			LinkClosed: func() {
				w.mu.Lock()
				defer w.mu.Unlock()
				w.events = append(w.events, linkClosed)
			},
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

// A shutdown that finds a reload in flight in the NodeHost, whose event loop is blocked and answers nothing, closes the worker's
// link to the server before the NodeHost ends and fails the reload (Pi's `#shutdown` runs the closers in reverse order, and the
// host closer, registered last, closes the link: packages/chord/src/next-node/workers/main.ts, ipc.ts's close frame precedes the
// `reloaded` reply). The NodeHost's close runs after the app's closers, those it registered before and after the load, so the
// server reads the reload's failure after its link closed, where it settles the reload from `ended`
// (TestTheRolloutStepListsAWorkerStoppedCleanlyDuringItAsStoppedAndOneThatCrashedWithItsError). The NodeHost stays blocked until
// the server saw the link close: no timing decides the order the test asserts. Before the NodeHost's close ran last, it ran
// first, ended the process the blocked loop left unanswered and failed the reload while the app's slow closers still ran.
func TestTheWorkersSideClosesItsLinkToTheServerBeforeItsNodeHostFailsAReloadInFlightAtShutdown(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	release := filepath.Join(dir, "release")
	worker := started(t, workerBehaviour{Log: log, CloseMs: 100, LateCloseMs: 100, BlockNode: release, ShutdownDuringReload: true})
	until(t, func() bool { return worker.has(FromWorkerReady) })
	until(t, func() bool { return worker.printed("node blocked") })
	worker.child.Send(ToWorker{Kind: ToWorkerReload, Scripts: []plugins.PluginScript{}})
	// The worker shuts itself down once the reload is in flight, and the NodeHost answers it once released: after the link
	// closed, or at its own limit of 2 s (the case of the closers in the wrong order, whose link closes last).
	until(t, func() bool { return slices.Contains(worker.seen(), linkClosed) || worker.exited() != nil }, 5_000)
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return worker.exited() != nil })
	if exit := worker.exited(); exit.Code == nil || *exit.Code != 0 || exit.Signal != nil {
		t.Fatalf("exit = %+v, want {code: 0, signal: null}", exit)
	}
	events := worker.seen()
	closed := slices.Index(events, linkClosed)
	reloaded := slices.Index(events, string(FromWorkerReloaded))
	if closed < 0 || (reloaded >= 0 && reloaded < closed) {
		t.Fatalf("the server saw %v, want its link closed before any `reloaded`", events)
	}
	text, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "late\nstore\nhost\n" {
		t.Fatalf("closers ran as %q, want %q (the app's, reverse order)", text, "late\nstore\nhost\n")
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

// The NodeHost's keep-alive counts arrive on the goroutine that reads its channel (NodeOptions.OnActivity) while the worker's
// own sections read its idle state: both go through the worker's lock, never around it. Under -race, a read of the counts off
// the lock is a data race with the reports arriving meanwhile.
func TestTheWorkersIdleStateReadsTheNodeHostsActivityThroughItsReports(t *testing.T) {
	w := &worker{}
	w.context, w.abort = chordctx.WithCancel(chordctx.Background())
	defer w.abort(errShuttingDown)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for index := range 300 {
			w.noteActivity(host.ActivityCounts{References: index % 2})
		}
	}()
	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
		}
		w.mu.Lock()
		w.isIdleLocked()
		w.mu.Unlock()
	}
	w.mu.Lock()
	idle := w.isIdleLocked()
	w.mu.Unlock()
	if idle {
		t.Fatalf("idle with a reference held (the last report: %+v)", w.activity)
	}
}
