package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pig divergence (D111): a tool call whose context had ended when it reached the host is sent to the extension, which runs the tool with its signal already
// aborted, as Pi runs tool.execute with an aborted signal (subagent-extension.test.ts "returns aborted task details when the signal is already aborted").
// The call is written as the request frame with signal_aborted set, after the state the extension holds for it, and the signal-only cancel follows it.
// D111's own rule then applies from the abort: the tool's host calls are answered, a tool that settles within ToolAbortGrace gives its own result, and one
// that does not ends the call as "Operation aborted".

// endedCallRig is a Host with one extension whose connection is a pipe the test serves: a UI bridge, so the call pushes state, and a manual clock, so the grace period is stepped.
type endedCallRig struct {
	h       *Host
	me      *managedExt
	conn    *Conn
	peer    net.Conn
	clock   *manualLivenessClock
	frames  chan *Envelope
	execute extension.ToolExecuteFunc
}

func newEndedCallRig(t *testing.T) *endedCallRig {
	t.Helper()
	rig := &endedCallRig{clock: newManualLivenessClock(), frames: make(chan *Envelope, 256)}
	rig.h = NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { rig.h.Shutdown("test done") })
	hostSide, peer := net.Pipe()
	rig.peer = peer
	rig.conn = newConnWithOptions("ended", hostSide, connOptions{Clock: rig.clock, HeartbeatInterval: time.Hour})
	rig.conn.Start(t.Context())
	t.Cleanup(func() {
		_ = peer.Close()
		<-rig.conn.Done()
	})
	rig.me = withConn(&managedExt{config: ExtConfig{Name: "ended"}, host: rig.h}, rig.conn)
	rig.h.exts = map[string]*managedExt{"ended": rig.me}
	rig.h.SetUIBridge(NewUIBridge(func() {}))
	rig.execute = rig.h.makeToolExecuteFunc(rig.me, "probe")
	go rig.serve()
	return rig
}

// serve hands every frame the extension side receives to frames.
func (r *endedCallRig) serve() {
	for {
		env, err := readEnvelopeFrom(r.peer)
		if err != nil {
			return
		}
		r.frames <- env
	}
}

func (r *endedCallRig) next(t *testing.T, what string) *Envelope {
	t.Helper()
	select {
	case env := <-r.frames:
		return env
	case <-time.After(10 * time.Second):
		t.Fatalf("no frame: %s", what)
		return nil
	}
}

// expectNoFrame fails when a frame of the given type arrives within the wait.
func (r *endedCallRig) expectNoFrame(t *testing.T, typ string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case env := <-r.frames:
			if env.Type == typ {
				t.Fatalf("a %s frame was sent: %+v", typ, env)
			}
		case <-deadline:
			return
		}
	}
}

type endedCallOutcome struct {
	result extension.AgentToolResult
	err    error
}

func (r *endedCallRig) start(ctx context.Context, params string) <-chan endedCallOutcome {
	done := make(chan endedCallOutcome, 1)
	go func() {
		result, err := r.execute(ctx, "tc-ended", json.RawMessage(params), nil)
		done <- endedCallOutcome{result, err}
	}()
	return done
}

// enteredFrames reads the frames of a call that starts aborted, in the order they must be written: the state the call runs against, the request with its
// signal already aborted, and the signal-only cancel that follows it.
func (r *endedCallRig) enteredFrames(t *testing.T) *Envelope {
	t.Helper()
	state := r.next(t, "the state the call runs against")
	if state.Type != MsgNotify || state.Notify == nil || state.Notify.Method != "state_update" {
		t.Fatalf("first frame = %+v, want the state_update the tool runs against", state)
	}
	request := r.next(t, "the tool_call request")
	if request.Type != MsgRequest || request.Request == nil || request.Request.Method != MethodToolCall || request.Request.Tool != "probe" {
		t.Fatalf("second frame = %+v, want the tool_call request", request)
	}
	if !request.Request.SignalAborted {
		t.Fatalf("the request frame does not carry signal_aborted: %+v", request.Request)
	}
	cancel := r.next(t, "the signal-only cancel")
	if cancel.Type != MsgCancel || cancel.Cancel == nil || cancel.Cancel.RequestID != request.ID || !cancel.Cancel.SignalOnly {
		t.Fatalf("third frame = %+v, want the signal-only cancel of %s", cancel, request.ID)
	}
	return request
}

func (r *endedCallRig) respond(t *testing.T, request *Envelope, text string) {
	t.Helper()
	result, err := json.Marshal(map[string]any{"content": []map[string]any{{"type": "text", "text": text}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEnvelopeTo(r.peer, &Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: result}}); err != nil {
		t.Fatal(err)
	}
}

// settled fails unless the connection holds nothing of the call: no pending request, no tool request, no host call.
func (r *endedCallRig) settled(t *testing.T) {
	t.Helper()
	r.conn.pendingMu.Lock()
	pending, tools := len(r.conn.pending), len(r.conn.toolRequests)
	r.conn.pendingMu.Unlock()
	r.conn.hostCallMu.Lock()
	aborted, cancelled := len(r.conn.abortedParent), len(r.conn.cancelledParent)
	r.conn.hostCallMu.Unlock()
	if pending != 0 || tools != 0 || aborted != 0 || cancelled != 0 {
		t.Fatalf("the connection still holds the call: %d pending, %d tool requests, %d aborted parents, %d cancelled parents", pending, tools, aborted, cancelled)
	}
}

// playIncomingLoop plays the host's incoming loop for the barrier a call waits on before it continues (conn.dispatchBarrier): the rig has no loop of its own,
// and a call whose context ended gets that barrier released only when the loop does. The returned function ends it.
func (r *endedCallRig) playIncomingLoop() (stop func()) {
	quit, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		for {
			select {
			case env := <-r.conn.Incoming():
				if env.barrier != nil {
					env.barrier.release()
				}
			case <-quit:
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-exited
	}
}

// outcome waits for the call to end.
func (r *endedCallRig) outcome(t *testing.T, done <-chan endedCallOutcome) (string, error) {
	t.Helper()
	defer r.playIncomingLoop()()
	select {
	case got := <-done:
		var parts []string
		for _, block := range got.result.Content {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n"), got.err
	case <-time.After(30 * time.Second):
		t.Fatal("the call never settled")
		return "", nil
	}
}

// Every way the call's context can already be done when the call reaches the host that is not a teardown of the connection: the run's abort, a deadline that
// passed, and a cancel with a cause. The call is written as the request with signal_aborted after the state push, the signal-only cancel follows it, the tool
// answers within the grace period, and its answer is the call's result (not "context canceled").
func TestEndedToolCallReachesTheExtensionWithItsSignalAborted(t *testing.T) {
	cases := map[string]func(t *testing.T) context.Context{
		"aborted run": func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			return ctx
		},
		"deadline passed": func(t *testing.T) context.Context {
			ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			t.Cleanup(cancel)
			return ctx
		},
		"cancelled with a cause": func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancelCause(t.Context())
			cancel(errors.New("session replaced"))
			return ctx
		},
	}
	for name, ended := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newEndedCallRig(t)
			done := rig.start(ended(t), `{"nonce":"n-7"}`)
			request := rig.enteredFrames(t)
			rig.clock.expectTimer(t, ToolAbortGrace)
			rig.respond(t, request, "entered:n-7:aborted=true")
			text, err := rig.outcome(t, done)
			if err != nil || text != "entered:n-7:aborted=true" {
				t.Fatalf("the call = %q, %v; want the tool's own answer", text, err)
			}
			rig.settled(t)
		})
	}
}

// The host calls a tool that started aborted makes are answered until the grace period ends, as for a call aborted in flight: a dialog is not cancelled, a
// nested tool call runs with the aborted signal, and at the grace's end the tool is abandoned with the ordinary cancel, its calls end, and the call fails as
// "Operation aborted".
func TestEndedToolCallKeepsItsHostCallsUntilTheGraceEnds(t *testing.T) {
	rig := newEndedCallRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	done := rig.start(ctx, `{}`)
	request := rig.enteredFrames(t)
	rig.clock.expectTimer(t, ToolAbortGrace)
	openCall := func(id, method string) context.Context {
		t.Helper()
		writeLivenessEnvelope(t, rig.peer, Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, ParentRequestID: request.ID}})
		select {
		case env := <-rig.conn.Incoming():
			if env.Type != MsgCall || env.ID != id {
				t.Fatalf("incoming = %#v, want call %s", env, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("call %s was not delivered", id)
		}
		callCtx, release := rig.conn.hostCallContext(request.ID, id)
		t.Cleanup(release)
		return callCtx
	}
	dialog := openCall("c-dialog", "ui.input")
	nested := openCall("c-nested", CallExecuteTool)
	if dialog.Err() != nil {
		t.Fatal("a host call the tool made after it started aborted was cancelled")
	}
	if nested.Err() == nil {
		t.Fatal("a nested tool call of a tool that started aborted runs with a live signal")
	}
	if !rig.conn.parentAbortedOnly(request.ID) {
		t.Fatal("the request is not marked as aborted alone")
	}
	select {
	case <-done:
		t.Fatal("the call ended before the grace period did")
	default:
	}
	rig.clock.elapse(ToolAbortGrace)
	text, err := rig.outcome(t, done)
	if !errors.Is(err, ErrToolAborted) || text != "" {
		t.Fatalf("the call = %q, %v; want ErrToolAborted", text, err)
	}
	ordinary := rig.next(t, "the ordinary cancel at the grace's end")
	if ordinary.Type != MsgCancel || ordinary.Cancel == nil || ordinary.Cancel.RequestID != request.ID || ordinary.Cancel.SignalOnly {
		t.Fatalf("frame at the grace's end = %+v, want the ordinary cancel of %s", ordinary, request.ID)
	}
	if dialog.Err() == nil {
		t.Fatal("the dialog the abandoned tool awaited was not cancelled at the grace's end")
	}
	rig.settled(t)
}

// stuckPeerConn is a connection whose peer does not read: its writer holds one frame, blocked on the pipe with its batch closed, and, when fill is set, the queue behind it is
// full to its capacity. Both are reached by sends the test makes itself, so nothing depends on timing.
func stuckPeerConn(t *testing.T, rig *endedCallRig, name string, options connOptions, fill bool) (*managedExt, *Conn, net.Conn) {
	t.Helper()
	hostSide, peer := net.Pipe()
	conn := newConnWithOptions(name, hostSide, options)
	conn.Start(t.Context())
	t.Cleanup(func() {
		_ = peer.Close()
		<-conn.Done()
	})
	me := withConn(&managedExt{config: ExtConfig{Name: name}, host: rig.h}, conn)
	rig.h.exts[name] = me
	filler := &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "filler"}}
	if err := conn.Send(filler); err != nil {
		t.Fatal(err)
	}
	// The writer holds writeMu from the end of its batch gathering to the end of its write: while it is held the writer is blocked on the peer, and no frame sent now joins its batch.
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		if !conn.writeMu.TryLock() {
			break
		}
		conn.writeMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("the writer never blocked on the first frame")
		}
	}
	if fill {
		for range cap(conn.outCh) {
			if err := conn.Send(filler); err != nil {
				t.Fatal(err)
			}
		}
	}
	return me, conn, peer
}

// A call whose frames cannot be queued within the grace period ends as aborted (D111): the writer is held by a peer that does not read and its queue is full.
func TestEndedToolCallThatCannotBeWrittenFailsAsAborted(t *testing.T) {
	rig := newEndedCallRig(t)
	me, _, _ := stuckPeerConn(t, rig, "stuck", connOptions{Clock: rig.clock, HeartbeatInterval: time.Hour}, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	_, err := rig.h.makeToolExecuteFunc(me, "probe")(ctx, "tc-stuck", json.RawMessage(`{}`), nil)
	if !errors.Is(err, ErrToolAborted) {
		t.Fatalf("the call = %v, want ErrToolAborted", err)
	}
	if elapsed := time.Since(started); elapsed < ToolAbortGrace {
		t.Fatalf("the call failed after %v, before the %v bound", elapsed, ToolAbortGrace)
	}
}

// A frame already queued behind a writer that a peer holds waits for its write as every request does, and ends with the connection: the heartbeat (D56) fails
// a peer that stops reading, and the call returns the connection's failure, not "Operation aborted". The call's place in the call order is released on the way, so the next
// reserved call ends with the connection too, and the connection holds nothing of either.
func TestEndedToolCallQueuedBehindAStuckWriterEndsWithTheConnection(t *testing.T) {
	rig := newEndedCallRig(t)
	me, conn, _ := stuckPeerConn(t, rig, "stuck-queued", connOptions{HeartbeatInterval: 20 * time.Millisecond, HeartbeatTimeout: 200 * time.Millisecond}, false)
	reserve := func() *extension.CallOrder {
		place := conn.toolOrder.Reserve()
		return &extension.CallOrder{Wait: place.Wait, Release: place.Release}
	}
	first, second := reserve(), reserve()
	execute := rig.h.makeToolExecuteFunc(me, "probe")
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	results := make(chan error, 2)
	go func() {
		_, err := execute(extension.WithCallOrder(ended, first), "tc-first", json.RawMessage(`{}`), nil)
		results <- err
	}()
	go func() {
		_, err := execute(extension.WithCallOrder(t.Context(), second), "tc-second", json.RawMessage(`{}`), nil)
		results <- err
	}()
	for range 2 {
		select {
		case err := <-results:
			// The heartbeat fails the connection (ExtensionUnresponsiveError) and closes the transport, which also ends the write the writer was in (TransportError).
			var unresponsive *ExtensionUnresponsiveError
			var transport *TransportError
			if err == nil || errors.Is(err, ErrToolAborted) || (!errors.As(err, &unresponsive) && !errors.As(err, &transport)) {
				t.Fatalf("a call behind the stuck writer = %v, want the connection's failure", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a call behind the stuck writer did not end with the connection")
		}
	}
	conn.pendingMu.Lock()
	pending, tools := len(conn.pending), len(conn.toolRequests)
	conn.pendingMu.Unlock()
	if pending != 0 || tools != 0 {
		t.Fatalf("the connection still holds the calls: %d pending, %d tool requests", pending, tools)
	}
}

// A connection that is closing takes no tool call whose context ended: a shutdown or a replacement is not an abort, the call is not sent, and it neither
// waits a grace period nor starts a tool in the process being ended.
func TestEndedToolCallOnAClosingConnectionIsNotSent(t *testing.T) {
	rig := newEndedCallRig(t)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = rig.conn.Close("shutdown")
	}()
	shutdown := rig.next(t, "the shutdown frame")
	if shutdown.Type != MsgShutdown {
		t.Fatalf("frame = %+v, want the shutdown frame", shutdown)
	}
	<-closed
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	done := rig.start(ctx, `{}`)
	select {
	case got := <-done:
		if got.err == nil || errors.Is(got.err, ErrToolAborted) {
			t.Fatalf("the call = %v, want a failure that is not the abort's", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call on a closing connection did not end at once")
	}
	if time.Since(started) >= ToolAbortGrace {
		t.Fatal("the call waited a grace period on a closing connection")
	}
	rig.expectNoFrame(t, MsgRequest, 200*time.Millisecond)
	rig.clock.mu.Lock()
	graces := rig.clock.requested[ToolAbortGrace]
	rig.clock.mu.Unlock()
	if graces != 0 {
		t.Fatalf("%d grace timers were started for a call on a closing connection", graces)
	}
}

// windowTool is an extension tool the agent's gate has already passed when the run is aborted: the abort lands between the gate and the host's write.
type windowTool struct {
	execute extension.ToolExecuteFunc
	abort   func()
}

func (windowTool) Name() string                           { return "probe" }
func (windowTool) Label() string                          { return "probe" }
func (windowTool) Schema() ai.ToolSchema                  { return ai.ToolSchema{Name: "probe"} }
func (windowTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (t windowTool) Execute(ctx context.Context, id string, params json.RawMessage, onUpdate agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	t.abort()
	return t.execute(ctx, id, params, onUpdate)
}

// The window between the agent's gate (agent.RunToolCall refuses a call whose context ended) and the host's write: the abort lands in it, deterministically,
// from the tool the gate let through. The tool is entered with its signal aborted, and the call's tool result is what Pi gives for an abort just after the
// tool's entry: the tool's own answer when it settles within the grace period, "Operation aborted" when it does not. The text never says "context canceled".
func TestAbortBetweenTheAgentGateAndTheHostWriteEntersTheTool(t *testing.T) {
	run := func(t *testing.T, settle func(rig *endedCallRig, request *Envelope)) agent.AgentToolCallOutcome {
		rig := newEndedCallRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		defer rig.playIncomingLoop()()
		var once sync.Once
		tool := windowTool{execute: rig.execute, abort: func() { once.Do(cancel) }}
		outcome := make(chan agent.AgentToolCallOutcome, 1)
		go func() {
			got, err := agent.RunToolCall(ctx, ai.ToolCall{ID: "tc-window", Name: "probe", Arguments: ai.JsonObject{}}, agent.RunToolCallOptions{Tools: []agent.AgentTool{tool}})
			if err != nil {
				t.Error(err)
			}
			outcome <- got
		}()
		request := rig.enteredFrames(t)
		rig.clock.expectTimer(t, ToolAbortGrace)
		settle(rig, request)
		select {
		case got := <-outcome:
			return got
		case <-time.After(30 * time.Second):
			t.Fatal("the call never settled")
			return agent.AgentToolCallOutcome{}
		}
	}
	t.Run("the tool settles within the grace period", func(t *testing.T) {
		got := run(t, func(rig *endedCallRig, request *Envelope) { rig.respond(t, request, "cooperative stopped") })
		if got.IsError || got.Result.Text() != "cooperative stopped" {
			t.Fatalf("outcome = %q error=%v, want the tool's own answer", got.Result.Text(), got.IsError)
		}
	})
	t.Run("the tool does not settle", func(t *testing.T) {
		got := run(t, func(rig *endedCallRig, _ *Envelope) { rig.clock.elapse(ToolAbortGrace) })
		if !got.IsError || got.Result.Text() != "Operation aborted" {
			t.Fatalf("outcome = %q error=%v, want %q as an error", got.Result.Text(), got.IsError, "Operation aborted")
		}
	})
}

// The grace period runs from the call's start, not from the time its context ended: a call whose context ended long before it began (the clock has run ten
// grace periods since) still gives the tool the whole period to answer for itself, as the tool of an abort that lands in the window has it.
func TestEndedToolCallGivesTheToolTheWholeGraceFromTheCallsStart(t *testing.T) {
	rig := newEndedCallRig(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rig.clock.elapse(10 * ToolAbortGrace)
	done := rig.start(ctx, `{}`)
	request := rig.enteredFrames(t)
	rig.clock.expectTimer(t, ToolAbortGrace)
	rig.clock.elapse(ToolAbortGrace - time.Millisecond)
	select {
	case got := <-done:
		t.Fatalf("the call ended inside the grace period: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	rig.respond(t, request, "answered late in the grace")
	text, err := rig.outcome(t, done)
	if err != nil || text != "answered late in the grace" {
		t.Fatalf("the call = %q, %v; want the tool's own answer, not %q", text, err, "Operation aborted")
	}
	rig.settled(t)
}

// Shutdown is not lengthened by a call that was sent after its context ended: when the connection goes (a Close at shutdown, a Retire of the generation a
// session replacement ends) while the tool has not settled, the call ends at once, with the error a call that was in flight when the connection went gets, and
// no grace period is waited (the test's clock never reaches it).
func TestEndedToolCallDoesNotLengthenShutdownOrReplacement(t *testing.T) {
	for _, ending := range []string{"Close", "Retire"} {
		t.Run(ending, func(t *testing.T) {
			endOf := func(t *testing.T, ended bool) (time.Duration, error) {
				t.Helper()
				rig := newEndedCallRig(t)
				ctx := t.Context()
				if ended {
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				}
				done := rig.start(ctx, `{}`)
				rig.next(t, "the state the call runs against")
				rig.next(t, "the tool_call request")
				if ended {
					rig.next(t, "the signal-only cancel")
				}
				started := time.Now()
				go func() {
					if ending == "Retire" {
						_ = rig.conn.Retire("replaced")
					} else {
						_ = rig.conn.Close("shutdown")
					}
				}()
				for rig.next(t, "the shutdown frame").Type != MsgShutdown {
				}
				if ending == "Retire" {
					// Retire leaves the peer running until the peer closes; Close closes the socket itself.
					_ = rig.peer.Close()
				}
				select {
				case got := <-done:
					return time.Since(started), got.err
				case <-time.After(10 * time.Second):
					t.Fatal("the call did not end with its connection")
					return 0, nil
				}
			}
			_, inFlight := endOf(t, false)
			took, sent := endOf(t, true)
			if inFlight == nil || sent == nil {
				t.Fatalf("a call survived its connection: in flight %v, sent after its context ended %v", inFlight, sent)
			}
			if errors.Is(sent, ErrToolAborted) || sent.Error() != inFlight.Error() {
				t.Fatalf("a call sent after its context ended ends with %q, want what a call in flight gets: %q", sent, inFlight)
			}
			if took >= ToolAbortGrace {
				t.Fatalf("the call took %v to end with its connection", took)
			}
		})
	}
}
