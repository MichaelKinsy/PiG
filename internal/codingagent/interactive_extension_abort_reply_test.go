package codingagent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
// The queued messages go back to the editor before the call returns, and that is loop-owned state, so the call waits for the owner loop to run its abort task (not for the run to end).
// An abort that is only posted to the loop returns when the loop accepts the task, so a loop busy with an earlier task leaves the signal live and the reply
// tells the handler that the run was not aborted. These rows park the loop on an earlier task, which no test sleep controls: the gate does.

// abortReplyForm is how the mode reaches its Session: the production form holds a Session handle (coding.Session), the other is a mode that owns its agent.
type abortReplyForm string

const (
	abortThroughSession abortReplyForm = "session"
	abortThroughAgent   abortReplyForm = "agent"
)

var abortReplyForms = []abortReplyForm{abortThroughSession, abortThroughAgent}

// abortingHandle is a Session handle whose RequestAbort cancels the agent's run, as coding.Session.RequestAbort does.
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
		rig.handle = &abortingHandle{}
		rig.handle.agent = next
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

// startRun starts a run that blocks in its provider call until the run's signal is cancelled. A raw-agent mode runs on the abort context current when the run starts, as interactive_turn.go does.
// It returns the run's signal as the extension host reads it, and a channel that closes when the run has ended.
func (r *abortReplyRig) startRun(t *testing.T) (context.Context, <-chan struct{}) {
	t.Helper()
	ended := make(chan struct{})
	runCtx := r.m.abortCtx
	r.runs.Go(func() {
		defer close(ended)
		_, _ = r.m.agent.Send(runCtx, "work")
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

// abortWhileLoopParked makes the extension's abort call with the owner loop parked in an earlier task and returns whether signal (nil: none) was still live at the moment the call returned. The call waits for the loop, so the loop is released from another
// goroutine: once the abort task is queued behind the parking task and, for a call that returns without the loop (the abort only posted), no earlier than a short rendezvous after the call returned, so that
// such a call is observed returning while the loop is still busy. The rendezvous only bounds that observation: a call that waits for the loop returns after the release however long the rendezvous is.
func (r *abortReplyRig) abortWhileLoopParked(t *testing.T, release func(), signal context.Context) (liveAtReturn bool) {
	t.Helper()
	returned := make(chan struct{})
	released := make(chan struct{})
	go func() {
		defer close(released)
		for len(r.m.uiTaskCh) == 0 {
			select {
			case <-returned:
				release()
				return
			case <-testbudget.Context(t).Done():
				release()
				return
			case <-time.After(time.Millisecond):
			}
		}
		select {
		case <-returned:
		case <-time.After(50 * time.Millisecond):
		}
		release()
	}()
	r.abort()
	liveAtReturn = signal != nil && signal.Err() == nil
	close(returned)
	<-released
	return liveAtReturn
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

// The reply to ctx.abort() reflects the cancellation: the call is made while the owner loop is busy with an earlier task, and the run's signal is aborted at the moment the call returns,
// for the signal the handler held and for a fresh read. The run is held after its signal is cancelled, as it is while the handler that made the call is still running, so the call returned
// without waiting for the run to end.
func TestExtensionAbortAbortsTheRunSignalBeforeItReturnsWhileTheOwnerLoopIsBusy(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			held, ended := rig.startRun(t)
			releaseRun := rig.holdRuns(t)
			release := rig.parkOwnerLoop(t)

			returnedLive := rig.abortWhileLoopParked(t, release, held)

			if returnedLive {
				t.Fatal("the run's signal was live when ctx.abort() returned: the abort had only been queued behind the owner loop's earlier task")
			}
			if now := rig.signal(); now != held || now.Err() == nil {
				t.Fatalf("ctx.signal read after the call = %v (aborted %t), want the held signal, aborted", now, now != nil && now.Err() != nil)
			}
			select {
			case <-ended:
				t.Fatal("the run ended while the handler that made the call still held it")
			default:
			}
			releaseRun()
			runEnds(t, ended, "the aborted run did not end once its handler returned")
		})
	}
}

// A second abort from another handler of the same turn is a no-op: both calls return with the signal aborted, each made while the loop is busy, and the run ends once.
func TestExtensionAbortTwiceInOneTurnIsIdempotent(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			held, ended := rig.startRun(t)
			releaseRun := rig.holdRuns(t)

			first := rig.abortWhileLoopParked(t, rig.parkOwnerLoop(t), held)
			if first {
				t.Fatal("the first abort left the run's signal live when it returned")
			}
			second := rig.abortWhileLoopParked(t, rig.parkOwnerLoop(t), held)
			if second {
				t.Fatal("the second abort left the run's signal live when it returned")
			}
			if now := rig.signal(); now != held || now.Err() == nil {
				t.Fatalf("ctx.signal after the second abort = %v, want the held signal, still aborted", now)
			}
			releaseRun()
			runEnds(t, ended, "the run did not end after two aborts")
			onLoop(rig.m, rig.m.runCtx, func() {})
			next, nextEnded := rig.startRun(t)
			if next.Err() != nil {
				t.Fatal("an abort of the earlier turn cancelled the next run")
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

			rig.abortWhileLoopParked(t, release, nil)

			if signal := rig.signal(); signal != nil {
				t.Fatalf("ctx.signal = %v after an abort with no run, want undefined", signal)
			}
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

// What the extension's abort does applies to the run it aborted, not to a later one: run 1 is aborted while the loop is busy, run 2 begins, and the loop then runs everything queued.
func TestExtensionAbortDoesNotCancelALaterRun(t *testing.T) {
	for _, form := range abortReplyForms {
		t.Run(string(form), func(t *testing.T) {
			rig := newAbortReplyRig(t, form)
			_, firstEnded := rig.startRun(t)
			release := rig.parkOwnerLoop(t)

			rig.abortWhileLoopParked(t, release, nil)
			runEnds(t, firstEnded, "the aborted run did not end")
			onLoop(rig.m, rig.m.runCtx, func() {})
			second, secondEnded := rig.startRun(t)
			onLoop(rig.m, rig.m.runCtx, func() {})

			if second.Err() != nil {
				t.Fatal("an abort made during run 1 cancelled run 2 when the owner loop reached it")
			}
			rig.m.agent.Abort()
			runEnds(t, secondEnded, "run 2 did not end when aborted")
		})
	}
}
