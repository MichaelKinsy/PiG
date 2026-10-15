package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// The host side of an execution against a launcher that is not the in-process worker (packages/codemode/src/runtime/
// execution.ts: Execution, VmLauncher, VmChannel, VmEvents). Pi's host tests replace the worker with test/fixtures/
// raw-worker.ts; these replace the launcher.

type fakeChannel struct {
	mu      sync.Mutex
	posted  []callResult
	stopped int
	onPost  func(callResult)
}

func (c *fakeChannel) start() {}

func (c *fakeChannel) post(r callResult) {
	c.mu.Lock()
	c.posted = append(c.posted, r)
	onPost := c.onPost
	c.mu.Unlock()
	if onPost != nil {
		onPost(r)
	}
}

func (c *fakeChannel) stop() {
	c.mu.Lock()
	c.stopped++
	c.mu.Unlock()
}

func (c *fakeChannel) state() ([]callResult, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]callResult(nil), c.posted...), c.stopped
}

func sandboxWith(t *testing.T, launcher vmLauncher, tools ...Tool) *Sandbox {
	t.Helper()
	sandbox, err := NewSandbox(SandboxOptions{Tools: tools, TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	sandbox.launcher = launcher
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox
}

func executeWith(t *testing.T, sandbox *Sandbox) Result {
	t.Helper()
	result, err := sandbox.Execute(t.Context(), "ignored by the fake launcher", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func echoTool(name string) Tool {
	return Tool{Name: name, Execute: func(_ context.Context, args json.RawMessage) (json.RawMessage, error) { return args, nil }}
}

// execution.ts: a launcher rejection becomes a sandbox error with the rejection's message, and an execution that never got a
// channel has nothing to stop.
func TestALauncherErrorFailsTheExecutionAsASandboxError(t *testing.T) {
	result := executeWith(t, sandboxWith(t, func(context.Context, VmStartData, vmEvents) (vmChannel, error) {
		return nil, errors.New("no such isolate")
	}))
	if result.OK || result.Error.Kind != ErrorSandbox || result.Error.Message != "no such isolate" {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
}

// execution.ts: the first terminal event wins and the channel is stopped once, after the result is decided.
func TestTheChannelIsStoppedOnceAfterTheFirstTerminalEvent(t *testing.T) {
	channel := &fakeChannel{}
	result := executeWith(t, sandboxWith(t, func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
		events.message(vmMessage{kind: vmMessageDone, ok: true, value: "1", valueDefined: true, writes: "[]", writesDefined: true})
		events.message(vmMessage{kind: vmMessageCrash, message: "too late"})
		events.failure(&Error{Kind: ErrorSandbox, Message: "too late"})
		return channel, nil
	}))
	if !result.OK || string(result.Value) != "1" {
		t.Fatalf("result = %+v (%+v), want the first done", result, result.Error)
	}
	if _, stopped := channel.state(); stopped != 1 {
		t.Fatalf("stop called %d times, want once", stopped)
	}
}

// execution.ts: a message the host does not know, a crash and a launcher failure end the execution as sandbox errors.
func TestBrokenMessagesAndFailuresEndTheExecutionAsSandboxErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		send    func(vmEvents)
		message string
	}{
		{"unknown message", func(e vmEvents) { e.message(vmMessage{}) }, "Sandbox bridge broken: unknown message from the worker. The script may have modified built-ins such as a prototype's toJSON."},
		{"done without writes", func(e vmEvents) { e.message(vmMessage{kind: vmMessageDone, ok: true, value: "1", valueDefined: true}) }, "Sandbox bridge broken: unknown message from the worker. The script may have modified built-ins such as a prototype's toJSON."},
		{"crash", func(e vmEvents) { e.message(vmMessage{kind: vmMessageCrash, message: "Error: boom"}) }, "Error: boom"},
		{"failure", func(e vmEvents) {
			e.failure(&Error{Kind: ErrorSandbox, Message: "Worker exited with code 1 before the script settled"})
		}, "Worker exited with code 1 before the script settled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channel := &fakeChannel{}
			result := executeWith(t, sandboxWith(t, func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
				tc.send(events)
				return channel, nil
			}))
			if result.OK || result.Error.Kind != ErrorSandbox || result.Error.Message != tc.message {
				t.Fatalf("result = %+v (%+v), want a sandbox error %q", result, result.Error, tc.message)
			}
			if _, stopped := channel.state(); stopped != 1 {
				t.Fatalf("stop called %d times, want once", stopped)
			}
		})
	}
}

// execution.ts: replies produced before the launcher returned its channel are queued and sent, in order, when it arrives.
// The launcher starts two calls, waits until both replies are queued, and only then returns the channel.
func TestRepliesProducedBeforeTheChannelArrivesAreSentWhenItDoes(t *testing.T) {
	channel := &fakeChannel{}
	sandbox := sandboxWith(t, nil, echoTool("echo"))
	sandbox.launcher = func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
		events.message(vmMessage{kind: vmMessageCall, id: 7, isTool: true, name: "echo", args: `"a"`, argsDefined: true})
		events.message(vmMessage{kind: vmMessageCall, id: 8, isTool: true, name: "echo", args: `"b"`, argsDefined: true})
		for queuedReplies(sandbox) < 2 {
			runtime.Gosched()
		}
		return channel, nil
	}
	finishWhenBothArrive := make(chan struct{})
	channel.onPost = func(callResult) {
		if posted, _ := channel.state(); len(posted) == 2 {
			close(finishWhenBothArrive)
		}
	}
	go func() {
		<-finishWhenBothArrive
		sandboxFinish(sandbox)
	}()
	result := executeWith(t, sandbox)
	if result.OK || result.Error.Kind != ErrorAborted {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	posted, _ := channel.state()
	ids := map[int]string{}
	for _, r := range posted {
		ids[r.id] = r.payload
	}
	if len(posted) != 2 || ids[7] != `"a"` || ids[8] != `"b"` {
		t.Fatalf("posted = %+v, want the replies of calls 7 and 8", posted)
	}
	if got := result.Calls; len(got) != 2 || got[0].Status != CallOK || got[1].Status != CallOK {
		t.Fatalf("calls = %+v, want two ok calls", got)
	}
}

// queuedReplies counts the replies the running execution holds for a channel that has not arrived.
func queuedReplies(s *Sandbox) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for e := range s.running {
		e.mu.Lock()
		n += len(e.unsent)
		e.mu.Unlock()
	}
	return n
}

func sandboxFinish(s *Sandbox) {
	s.mu.Lock()
	running := make([]*execution, 0, len(s.running))
	for e := range s.running {
		running = append(running, e)
	}
	s.mu.Unlock()
	for _, e := range running {
		e.finish(&Error{Kind: ErrorAborted, Message: "test finished it"}, nil, StoreWrites{})
	}
}

// execution.ts: an execution that finished before the launcher returned has its channel stopped at once, and its
// queued replies are dropped, not sent.
func TestAChannelThatArrivesAfterTheExecutionEndedIsStoppedWithoutReplies(t *testing.T) {
	channel := &fakeChannel{}
	sandbox := sandboxWith(t, nil, echoTool("echo"))
	sandbox.launcher = func(ctx context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
		events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "echo", args: `1`, argsDefined: true})
		for queuedReplies(sandbox) < 1 {
			runtime.Gosched()
		}
		sandboxFinish(sandbox)
		return channel, nil
	}
	result := executeWith(t, sandbox)
	if result.OK || result.Error.Kind != ErrorAborted {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	posted, stopped := channel.state()
	if len(posted) != 0 || stopped != 1 {
		t.Fatalf("posted %d replies and stopped %d times, want none and one stop", len(posted), stopped)
	}
}

// vm.ts, protocol.ts: the host answers each call with the channel's post; a failing tool is an error reply, an unknown
// tool too, and a call after the execution ended is dropped.
func TestCallRepliesCarryTheToolOutcome(t *testing.T) {
	channel := &fakeChannel{}
	replies := make(chan callResult, 3)
	channel.onPost = func(r callResult) { replies <- r }
	fail := Tool{Name: "fail", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("tool exploded")
	}}
	sandbox := sandboxWith(t, func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
		go func() {
			events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "echo", args: `[1]`, argsDefined: true})
			events.message(vmMessage{kind: vmMessageCall, id: 2, isTool: true, name: "fail"})
			events.message(vmMessage{kind: vmMessageCall, id: 3, isTool: true, name: "missing"})
			for range 3 {
				<-replies
			}
			events.message(vmMessage{kind: vmMessageDone, ok: true, writes: "[]", writesDefined: true})
		}()
		return channel, nil
	}, echoTool("echo"), fail)
	result := executeWith(t, sandbox)
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	posted, _ := channel.state()
	byID := map[int]callResult{}
	for _, r := range posted {
		byID[r.id] = r
	}
	if r := byID[1]; !r.ok || r.payload != `[1]` || !r.defined {
		t.Errorf("reply 1 = %+v", r)
	}
	if r := byID[2]; r.ok || r.payload != "tool exploded" || !r.defined {
		t.Errorf("reply 2 = %+v", r)
	}
	if r := byID[3]; r.ok || r.payload != `Unknown tool "missing"` || !r.defined {
		t.Errorf("reply 3 = %+v", r)
	}
	statuses := []CallStatus{}
	for _, c := range result.Calls {
		statuses = append(statuses, c.Status)
	}
	if len(statuses) != 3 || statuses[0] != CallOK || statuses[1] != CallError || statuses[2] != CallError {
		t.Errorf("call statuses = %v, want ok, error, error", statuses)
	}
}

// alreadyStarted is a channel whose VM the launcher started itself.
type alreadyStarted struct{ vmChannel }

func (alreadyStarted) start() {}

// execution.ts posts the queued replies synchronously when the channel arrives (VmChannel.post never waits for the VM),
// so a VM that computes forever after a call still meets its deadline. The launcher here is the in-process worker
// returned late, after the VM's first call was answered, as a host goroutine descheduled while the VM starts would.
func TestAQueuedReplyDoesNotKeepARunawayScriptFromTimingOut(t *testing.T) {
	sandbox := sandboxWith(t, nil, echoTool("echo"))
	var channel vmChannel
	sandbox.launcher = func(ctx context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
		worker, err := sandbox.launchWorker(ctx, data, events)
		if err != nil {
			return nil, err
		}
		// The VM runs before the host holds the channel: its start is brought forward and the host's start is a no-op.
		worker.start()
		for queuedReplies(sandbox) < 1 {
			runtime.Gosched()
		}
		channel = alreadyStarted{worker}
		return channel, nil
	}
	results := make(chan Result, 1)
	go func() {
		result, _ := sandbox.Execute(context.Background(), "tools.echo(1); while (true) {}", ExecuteOptions{TimeoutMs: 100})
		results <- result
	}()
	select {
	case result := <-results:
		if result.OK || result.Error.Kind != ErrorTimeout {
			t.Fatalf("result = %+v (%+v), want a timeout", result, result.Error)
		}
	case <-time.After(5 * time.Second):
		// Release the host so the test's cleanup can close the sandbox.
		go channel.stop()
		t.Fatal("Execute did not return: flushing the queued reply waits for a VM that never takes it")
	}
}

// execution.ts flushes the queued replies before any later reply can be posted, so the VM receives them in the order the
// host produced them. Calls 1 and 3 are answered before the channel arrives; call 2 is answered while the queued replies are
// being sent, and must follow them.
func TestRepliesQueuedBeforeTheChannelArrivesAreSentBeforeLaterReplies(t *testing.T) {
	release := make(chan struct{})
	slow := Tool{Name: "slow", Execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		<-release
		return json.RawMessage(`2`), nil
	}}
	sandbox := sandboxWith(t, nil, echoTool("echo"), slow)
	channel := &fakeChannel{}
	sandbox.launcher = func(_ context.Context, _ VmStartData, events vmEvents) (vmChannel, error) {
		events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "echo", args: `1`, argsDefined: true})
		for queuedReplies(sandbox) < 1 {
			runtime.Gosched()
		}
		events.message(vmMessage{kind: vmMessageCall, id: 2, isTool: true, name: "slow"})
		events.message(vmMessage{kind: vmMessageCall, id: 3, isTool: true, name: "echo", args: `3`, argsDefined: true})
		for queuedReplies(sandbox) < 2 {
			runtime.Gosched()
		}
		return channel, nil
	}
	posted := func(id int) bool {
		got, _ := channel.state()
		return slices.ContainsFunc(got, func(r callResult) bool { return r.id == id })
	}
	all := make(chan struct{})
	var allPosted sync.Once
	channel.onPost = func(r callResult) {
		if r.id == 1 {
			// Answer call 2 now and wait until the host has either sent or queued its reply.
			close(release)
			deadline := time.Now().Add(5 * time.Second)
			for !posted(2) && queuedReplies(sandbox) < 1 && time.Now().Before(deadline) {
				runtime.Gosched()
			}
		}
		if got, _ := channel.state(); len(got) == 3 {
			allPosted.Do(func() { close(all) })
		}
	}
	go func() {
		<-all
		sandboxFinish(sandbox)
	}()
	executeWith(t, sandbox)
	got, _ := channel.state()
	ids := make([]int, len(got))
	for i, r := range got {
		ids[i] = r.id
	}
	if !slices.Equal(ids, []int{1, 3, 2}) {
		t.Fatalf("replies sent in the order %v, want [1 3 2]: the queued replies first", ids)
	}
}
