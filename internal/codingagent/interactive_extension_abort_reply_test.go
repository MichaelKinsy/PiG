package codingagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's ctx.abort() aborts the active run's AbortController before it returns: AgentSession.abort() sets its flags, cancels the retry, the compaction and the branch
// summary and calls agent.abort() before its first await (agent-session.ts:2519-2530, agent.ts:350-352), and the interactive mode's abort handler is
// `void this.session.abort()` (interactive-mode.ts:4840-4857). A handler that holds ctx.signal therefore sees it aborted the moment its abort call returns, with
// nothing else on Pi's one event loop having run, and a second abort from another handler of the same turn changes nothing.
//
// In PiG the extension's call arrives on a goroutine of the extension host, the owner loop runs the mode's other work, and the run goroutine waits for the handler.
// An abort that is only queued on the owner loop returns when the loop accepts the task, so a loop busy with an earlier task leaves the signal live and the reply
// tells the handler that the run was not aborted. These rows park the loop on an earlier task, which no test sleep controls: the gate does.

// abortReplyForm is how the mode reaches its Session: the production form holds a Session handle (coding.Session), the other is a mode that owns its agent.
type abortReplyForm string

const (
	abortThroughSession abortReplyForm = "session"
	abortThroughAgent   abortReplyForm = "agent"
)

var abortReplyForms = []abortReplyForm{abortThroughSession, abortThroughAgent}

// abortingHandle is a Session handle whose RequestAbort cancels the agent's run, as coding.Session.RequestAbort does. The Session's own cancellations (retry, compaction,
// branch summary) belong to it, so a caller that went through the handle's RequestAbort need not repeat them.
type abortingHandle struct {
	recordingCompactHandle
	requested atomic.Int32
}

func (h *abortingHandle) RequestAbort() {
	h.requested.Add(1)
	h.agent.Abort()
}

// abortReplyRig is an interactive mode with the extension host's abort and getSignal actions wired, an owner loop that drains posted tasks, and an agent whose runs block in the provider until their signal is cancelled.
// A run the rig holds (holdRuns) also stays active after its signal is cancelled, as a run does while a handler it awaits is still executing.
type abortReplyRig struct {
	m       *InteractiveMode
	handle  *abortingHandle
	abort   func()
	signal  func() context.Context
	started chan struct{}
	hold    atomic.Pointer[chan struct{}]
	runs    sync.WaitGroup
}

func newAbortReplyRig(t *testing.T, form abortReplyForm) *abortReplyRig {
	t.Helper()
	m := newPendingDisplayHarness(t)
	runCtx, cancel := context.WithCancel(context.Background())
	m.runCtx = runCtx
	m.abortCtx, m.abortFn = context.WithCancel(runCtx)
	rig := &abortReplyRig{m: m, started: make(chan struct{}, 8)}
	next := mustNewAgent(agent.AgentOptions{
		Model: &ai.Model{ID: "blocking"},
		StreamFn: func(ctx context.Context, _ *ai.Model, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			rig.started <- struct{}{}
			<-ctx.Done()
			if hold := rig.hold.Load(); hold != nil {
				<-*hold
			}
			return nil, ctx.Err()
		},
	})
	m.setAgent(next)
	if form == abortThroughSession {
		rig.handle = &abortingHandle{recordingCompactHandle: recordingCompactHandle{agent: next}}
		m.opts.SessionHandle = rig.handle
	}
	loopDone := make(chan struct{})
	go m.drainLoop(runCtx, loopDone)
	bridge := &captureUIBridge{}
	m.opts.SubprocessUIBridge = bridge
	detach := m.wireSubprocessHostCallbacks()
	t.Cleanup(func() {
		detach()
		cancel()
		<-loopDone
		rig.runs.Wait()
	})
	rig.abort = bridge.actions["abort"].(func())
	rig.signal = bridge.actions["getSignal"].(func() context.Context)
	return rig
}

// holdRuns keeps a run active after its signal is cancelled, until the returned release is called: the run waits for the handler that made the abort call, which reads ctx.signal again once the call has returned.
func (r *abortReplyRig) holdRuns(t *testing.T) (release func()) {
	t.Helper()
	hold := make(chan struct{})
	r.hold.Store(&hold)
	var once sync.Once
	release = func() { once.Do(func() { close(hold) }) }
	t.Cleanup(release)
	return release
}

// startRun starts a run that blocks in its provider call until the run's signal is cancelled. It returns the run's signal as the extension host reads it, and a channel that closes when the run has ended.
func (r *abortReplyRig) startRun(t *testing.T) (context.Context, <-chan struct{}) {
	t.Helper()
	ended := make(chan struct{})
	r.runs.Go(func() {
		defer close(ended)
		_, _ = r.m.agent.Send(t.Context(), "work")
	})
	select {
	case <-r.started:
	case <-testbudget.Context(t).Done():
		t.Fatal("the run never reached its provider call")
	}
	signal := r.signal()
	if signal == nil || signal.Err() != nil {
		t.Fatalf("ctx.signal = %v during a fresh run, want a live signal", signal)
	}
	return signal, ended
}

// parkOwnerLoop makes the owner loop run a task that returns only when the returned release is called, as a loop busy with an earlier task. It returns once the loop is inside that task.
func (r *abortReplyRig) parkOwnerLoop(t *testing.T) (release func()) {
	t.Helper()
	gate, entered := make(chan struct{}), make(chan struct{})
	r.m.runOnMain(r.m.runCtx, func() {
		close(entered)
		<-gate
	})
	select {
	case <-entered:
	case <-testbudget.Context(t).Done():
		t.Fatal("the owner loop never ran the parking task")
	}
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release
}

// runEnds waits for a run to end, failing the test when it does not within the budget.
func runEnds(t *testing.T, ended <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ended:
	case <-testbudget.Context(t).Done():
		t.Fatalf("%s", what)
	}
}

// The reply to ctx.abort() reflects the cancellation: while the owner loop is busy with an earlier task, the run's signal is aborted when the call returns, for
// the signal the handler held and for a fresh read, and the run ends without the loop. The run is held after its signal is cancelled, as it is while the handler that made the call is still running.
func TestExtensionAbortAbortsTheRunSignalBeforeItReturnsWhileTheOwnerLoopIsBusy(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			held, ended := rig.startRun(t)
			releaseRun := rig.holdRuns(t)
			release := rig.parkOwnerLoop(t)

			rig.abort()

			if held.Err() == nil {
				t.Fatal("the run's signal was live when ctx.abort() returned: the abort had only been queued behind the owner loop's earlier task")
			}
			if now := rig.signal(); now != held || now.Err() == nil {
				t.Fatalf("ctx.signal read after the call = %v (aborted %t), want the held signal, aborted", now, now != nil && now.Err() != nil)
			}
			releaseRun()
			runEnds(t, ended, "the aborted run did not end while the owner loop was busy: the abort waits for the loop")
			release()
		})
	}
}

// A second abort from another handler of the same turn is a no-op: both calls return with the signal aborted while the loop is busy, and the run ends once.
func TestExtensionAbortTwiceInOneTurnIsIdempotent(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			held, ended := rig.startRun(t)
			releaseRun := rig.holdRuns(t)
			release := rig.parkOwnerLoop(t)

			rig.abort()
			if held.Err() == nil {
				t.Fatal("the first abort left the run's signal live")
			}
			var second sync.WaitGroup
			second.Go(rig.abort)
			second.Wait()
			if now := rig.signal(); now != held || now.Err() == nil {
				t.Fatalf("ctx.signal after the second abort = %v, want the held signal, still aborted", now)
			}
			releaseRun()
			runEnds(t, ended, "the run did not end after two aborts")
			release()
			onLoop(rig.m, rig.m.runCtx, func() {})
			next, nextEnded := rig.startRun(t)
			if next.Err() != nil {
				t.Fatal("an abort of the earlier turn cancelled the next run when the owner loop ran its tasks")
			}
			rig.m.agent.Abort()
			runEnds(t, nextEnded, "the next run did not end when aborted")
		})
	}
}

// An abort with no run active has Pi's result: agent.abort() finds no controller, ctx.signal stays undefined, the call returns, and the abort leaves nothing behind that cancels the next run.
func TestExtensionAbortWithNoRunActiveLeavesTheNextRunLive(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			if rig.signal() != nil {
				t.Fatal("ctx.signal is set while no run is active")
			}
			release := rig.parkOwnerLoop(t)

			rig.abort()

			if signal := rig.signal(); signal != nil {
				t.Fatalf("ctx.signal = %v after an abort with no run, want undefined", signal)
			}
			release()
			onLoop(rig.m, rig.m.runCtx, func() {})
			next, ended := rig.startRun(t)
			if next.Err() != nil {
				t.Fatal("an abort made while no run was active cancelled the next run")
			}
			rig.m.agent.Abort()
			runEnds(t, ended, "the next run did not end when aborted")
		})
	}
}

// What the extension's abort leaves for the owner loop applies to the run it aborted, not to a later one: the abort ended run 1 while the loop was busy, run 2 began, and the loop then ran
// everything queued behind its earlier task.
func TestExtensionAbortDoesNotCancelALaterRun(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			_, firstEnded := rig.startRun(t)
			release := rig.parkOwnerLoop(t)

			rig.abort()
			runEnds(t, firstEnded, "the aborted run did not end while the owner loop was busy: the abort waits for the loop")
			second, secondEnded := rig.startRun(t)
			release()
			onLoop(rig.m, rig.m.runCtx, func() {})

			if second.Err() != nil {
				t.Fatal("an abort made during run 1 cancelled run 2 when the owner loop reached it")
			}
			rig.m.agent.Abort()
			runEnds(t, secondEnded, "run 2 did not end when aborted")
		})
	}
}
