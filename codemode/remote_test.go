package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The client side of the remote protocol against remotes that are not a real VM (packages/codemode/src/runtime/
// remote.ts: RemoteCodemodeSandbox, remoteLauncher), and the failure cases of packages/codemode/test/portable.test.ts.

type remoteFunc func(ctx context.Context, request RemoteRequest, exchange RemoteExchange) error

func (f remoteFunc) Execute(ctx context.Context, request RemoteRequest, exchange RemoteExchange) error {
	return f(ctx, request, exchange)
}

func remoteSandbox(t *testing.T, remote func() (Remote, error), tools ...Tool) *RemoteSandbox {
	t.Helper()
	sandbox, err := NewRemoteSandbox(RemoteSandboxOptions{Remote: remote, Tools: tools, TimeoutMs: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	return sandbox
}

func fixedRemote(remote Remote) func() (Remote, error) {
	return func() (Remote, error) { return remote, nil }
}

func executeRemote(t *testing.T, sandbox *RemoteSandbox, code string) Result {
	t.Helper()
	result, err := sandbox.Execute(t.Context(), code, ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// exchangeAndWait sends a batch and waits for its reply, as a blocking client would.
func exchangeAndWait(ctx context.Context, exchange RemoteExchange, messages ...RemoteMessage) (*RemoteReply, error) {
	type answer struct {
		reply *RemoteReply
		err   error
	}
	answered := make(chan answer, 1)
	exchange(ctx, messages, func(reply *RemoteReply, err error) { answered <- answer{reply, err} })
	select {
	case a := <-answered:
		return a.reply, a.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func callMessage(id int, name, args string) RemoteMessage {
	return RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCall, ID: id, Target: "tool", Name: name, Args: &args}}
}

func doneMessage(value string) RemoteMessage {
	writes := "[]"
	return RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageDone, Ok: true, Value: &value, Writes: &writes}}
}

// Same scripts and messages; each remote gets its own sandbox, as upstream's failing, silent and unreachable do.
// Pi 1.2.0 packages/codemode/test/portable.test.ts:134 describe("RemoteCodemodeSandbox") "reports a failing or vanishing remote as a sandbox error"
func TestAFailingOrVanishingRemoteIsASandboxError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		remote  func() (Remote, error)
		message string
	}{
		{"failing", fixedRemote(remoteFunc(func(context.Context, RemoteRequest, RemoteExchange) error { return errors.New("object reset") })),
			"Remote sandbox failed: object reset"},
		{"silent", fixedRemote(remoteFunc(func(context.Context, RemoteRequest, RemoteExchange) error { return nil })),
			"Remote sandbox returned before the script settled"},
		{"unreachable", func() (Remote, error) { return nil, errors.New("no binding") },
			"Failed to reach the remote sandbox: no binding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := executeRemote(t, remoteSandbox(t, tc.remote), "return 1")
			if result.OK || result.Error.Kind != ErrorSandbox || result.Error.Message != tc.message {
				t.Fatalf("result = %+v (%+v), want a sandbox error %q", result, result.Error, tc.message)
			}
		})
	}
}

// remote.ts: the request carries the script's start data and the requested interrupt budget; the remote is picked once
// per execution. Pi: packages/codemode/src/runtime/remote.ts:52-55 (CodemodeRemoteRequest.data and interruptBudget), :73-74
// (CodemodeRemote.execute), :230 (the request the launcher builds), and packages/codemode/src/types.ts:109 (globals), :120
// (memoryLimitBytes) for the options RemoteCodemodeSandbox inherits.
func TestTheRequestCarriesTheStartDataAndTheRequestedBudget(t *testing.T) {
	var requests []RemoteRequest
	picked := 0
	sandbox, err := NewRemoteSandbox(RemoteSandboxOptions{
		Tools: []Tool{{Name: "my-tool", Description: "adds"}}, Globals: []Tool{{Name: "g", Spread: true}},
		MemoryLimitBytes: 4 << 20, InterruptBudget: 77,
		Remote: func() (Remote, error) {
			picked++
			return remoteFunc(func(_ context.Context, request RemoteRequest, _ RemoteExchange) error {
				requests = append(requests, request)
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	for range 2 {
		if _, err := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{Store: map[string]json.RawMessage{"k": json.RawMessage(`{"a": 1}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	if picked != 2 || len(requests) != 2 {
		t.Fatalf("picked %d remotes and sent %d requests, want 2 and 2", picked, len(requests))
	}
	want := RemoteRequest{
		Data: VmStartData{Code: "return 1", MemoryLimitBytes: 4 << 20, Store: map[string]string{"k": `{"a":1}`},
			Tools:   []VmStartTool{{Name: "my-tool", JSName: "my_tool", Description: "adds"}},
			Globals: []VmStartGlobal{{Name: "g", Spread: true}}},
		InterruptBudget: 77,
	}
	got, _ := json.Marshal(requests[0])
	expected, _ := json.Marshal(want)
	if !bytes.Equal(got, expected) {
		t.Fatalf("request = %s\nwant      %s", got, expected)
	}
}

// remote.ts: each exchange carries at most one call; the client answers it with the tool's result, and the exchange with
// no call is answered with null at once.
func TestAnExchangeIsAnsweredWithTheResultOfItsCall(t *testing.T) {
	var replies []*RemoteReply
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		reply, err := exchangeAndWait(ctx, exchange, RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageOutput, Item: &OutputItem{Type: OutputItemText, Text: "hi"}}})
		if err != nil || reply != nil {
			t.Errorf("exchange without a call answered %+v, %v, want null", reply, err)
		}
		reply, err = exchangeAndWait(ctx, exchange, callMessage(3, "echo", `[1]`))
		replies = append(replies, reply)
		if err != nil {
			t.Error(err)
		}
		_, err = exchangeAndWait(ctx, exchange, doneMessage(`"end"`))
		return err
	})
	result := executeRemote(t, remoteSandbox(t, fixedRemote(remote), echoTool("echo")), "ignored")
	if !result.OK || string(result.Value) != `"end"` {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	if len(result.Output) != 1 || result.Output[0].Text != "hi" {
		t.Fatalf("output = %+v", result.Output)
	}
	if len(replies) != 1 || replies[0] == nil || replies[0].Type != MessageResult || replies[0].ID != 3 || !replies[0].Ok || replies[0].Payload == nil || *replies[0].Payload != `[1]` {
		t.Fatalf("reply = %+v", replies)
	}
}

// remote.ts: the waiter is registered before the host sees the call's message, so a tool that answers at once is not lost.
func TestAnImmediateToolAnswerReachesTheRemote(t *testing.T) {
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		for id := range 200 {
			reply, err := exchangeAndWait(ctx, exchange, callMessage(id, "echo", `1`))
			if err != nil || reply == nil || reply.Type != MessageResult || reply.ID != id {
				t.Errorf("call %d answered %+v, %v", id, reply, err)
				return nil
			}
		}
		_, err := exchangeAndWait(ctx, exchange, doneMessage(`1`))
		return err
	})
	result := executeRemote(t, remoteSandbox(t, fixedRemote(remote), echoTool("echo")), "ignored")
	if !result.OK || len(result.Calls) != 200 {
		t.Fatalf("result = %+v (%+v), calls %d", result, result.Error, len(result.Calls))
	}
}

// remote.ts:206-209: a host that answers a call before the delivery returns still reaches the remote. A tool that awaits
// initiation and returns without calling CallInitiated is answered inside the host's delivery of the call message,
// the Go form of upstream's tool that throws before it returns a promise.
func TestASynchronousToolAnswerReachesTheRemote(t *testing.T) {
	tool := echoTool("echo")
	tool.AwaitsInitiation = true
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		reply, err := exchangeAndWait(waitCtx, exchange, callMessage(0, "echo", `1`))
		if err != nil || reply == nil || reply.Type != MessageResult || reply.ID != 0 {
			t.Errorf("call answered %+v, %v, want its result", reply, err)
			return nil
		}
		_, err = exchangeAndWait(ctx, exchange, doneMessage(`1`))
		return err
	})
	result := executeRemote(t, remoteSandbox(t, fixedRemote(remote), tool), "ignored")
	if !result.OK || len(result.Calls) != 1 {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
}

// remote.ts: messages reach the host in the order the remote sent them, also when several calls are in flight.
func TestMessagesAreDeliveredInTheOrderTheRemoteSentThem(t *testing.T) {
	var mu sync.Mutex
	var started []string
	slow := make(chan struct{})
	tool := func(name string) Tool {
		return Tool{Name: name, Execute: func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
			mu.Lock()
			started = append(started, name)
			mu.Unlock()
			if name == "a" {
				<-slow
			}
			return args, nil
		}}
	}
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		answers := make(chan *RemoteReply, 2)
		for id, name := range []string{"a", "b"} {
			exchange(ctx, []RemoteMessage{callMessage(id, name, `1`)}, func(reply *RemoteReply, _ error) { answers <- reply })
		}
		first := <-answers
		close(slow)
		<-answers
		if first.ID != 1 {
			t.Errorf("the first answer is for call %d, want call 1 (b): a was still running", first.ID)
		}
		_, err := exchangeAndWait(ctx, exchange, doneMessage(`1`))
		return err
	})
	result := executeRemote(t, remoteSandbox(t, fixedRemote(remote), tool("a"), tool("b")), "ignored")
	if !result.OK {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	if len(result.Calls) != 2 || result.Calls[0].Name != "a" || result.Calls[1].Name != "b" {
		t.Fatalf("calls = %+v, want a then b in message order", result.Calls)
	}
}

// remote.ts: a message the host does not know still reaches the host, which fails the execution as a broken bridge; a
// failure message ends it with the error it carries.
func TestMalformedAndFailureMessagesEndTheExecution(t *testing.T) {
	const broken = "Sandbox bridge broken: unknown message from the worker. The script may have modified built-ins such as a prototype's toJSON."
	for _, tc := range []struct {
		name    string
		message RemoteMessage
		want    Error
	}{
		{"unknown type", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: "bogus"}}, Error{Kind: ErrorSandbox, Message: broken}},
		{"crash", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageCrash, Message: "wasm trap"}}, Error{Kind: ErrorSandbox, Message: "wasm trap"}},
		{"failure", RemoteMessage{WorkerToHostMessage: WorkerToHostMessage{Type: MessageFailure}, Failure: &Error{Kind: ErrorTimeout, Message: "Execution exceeded its interrupt budget of 5"}},
			Error{Kind: ErrorTimeout, Message: "Execution exceeded its interrupt budget of 5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
				_, err := exchangeAndWait(ctx, exchange, tc.message)
				return err
			})
			result := executeRemote(t, remoteSandbox(t, fixedRemote(remote)), "ignored")
			if result.OK || *result.Error != tc.want {
				t.Fatalf("result = %+v (%+v), want %+v", result, result.Error, tc.want)
			}
		})
	}
}

// remote.ts: once the client finished, every pending and later exchange is answered with stop, the context of Execute is
// cancelled, and a terminal failure is not reported over the verdict. portable.test.ts "tells the remote to stop when
// the caller aborts".
func TestAbortAnswersThePendingExchangeWithStop(t *testing.T) {
	started := make(chan struct{})
	tool := Tool{Name: "hang", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	type outcome struct {
		pending, later *RemoteReply
		executeCtxErr  error
	}
	outcomes := make(chan outcome, 1)
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		var o outcome
		o.pending, _ = exchangeAndWait(ctx, exchange, callMessage(0, "hang", `1`))
		select {
		case <-ctx.Done():
			o.executeCtxErr = ctx.Err()
		case <-time.After(10 * time.Second):
		}
		o.later, _ = exchangeAndWait(context.Background(), exchange, callMessage(1, "hang", `1`))
		outcomes <- o
		return nil
	})
	sandbox := remoteSandbox(t, fixedRemote(remote), tool)
	ctx, cancel := context.WithCancelCause(t.Context())
	go func() {
		<-started
		cancel(errors.New("user abort"))
	}()
	result, err := sandbox.Execute(ctx, "ignored", ExecuteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Error.Kind != ErrorAborted || result.Error.Message != "user abort" {
		t.Fatalf("result = %+v (%+v)", result, result.Error)
	}
	o := <-outcomes
	if o.pending == nil || o.pending.Type != MessageStop || o.later == nil || o.later.Type != MessageStop || o.executeCtxErr == nil {
		t.Fatalf("pending = %+v, later = %+v, ctx error = %v, want stop replies and a cancelled context", o.pending, o.later, o.executeCtxErr)
	}
}

// remote.ts: a remote that fails after the client finished does not turn the verdict into a sandbox error.
func TestAFailureAfterTheClientFinishedIsNotReported(t *testing.T) {
	remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
		if _, err := exchangeAndWait(ctx, exchange, doneMessage(`1`)); err != nil {
			return err
		}
		return errors.New("torn down")
	})
	result := executeRemote(t, remoteSandbox(t, fixedRemote(remote)), "ignored")
	if !result.OK || string(result.Value) != "1" {
		t.Fatalf("result = %+v (%+v), want the done message's value", result, result.Error)
	}
}

// Close joins the Remote.Execute calls it started, after it told them to stop. synctest.Wait makes "Close has not returned
// yet" an observed state: every goroutine of the bubble is blocked, Close among them.
func TestCloseWaitsForTheRemoteToReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		released := make(chan struct{})
		remote := remoteFunc(func(ctx context.Context, _ RemoteRequest, exchange RemoteExchange) error {
			reply, _ := exchangeAndWait(ctx, exchange, callMessage(0, "hang", `1`))
			if reply == nil || reply.Type != MessageStop {
				t.Errorf("reply = %+v, want stop", reply)
			}
			<-released
			return nil
		})
		started := make(chan struct{})
		tool := Tool{Name: "hang", Execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		sandbox, err := NewRemoteSandbox(RemoteSandboxOptions{Remote: fixedRemote(remote), Tools: []Tool{tool}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		executed := make(chan Result, 1)
		go func() {
			result, _ := sandbox.Execute(ctx, "ignored", ExecuteOptions{})
			executed <- result
		}()
		<-started
		cancel()
		if result := <-executed; result.Error == nil || result.Error.Kind != ErrorAborted {
			t.Fatalf("result = %+v (%+v)", result, result.Error)
		}
		closed := make(chan struct{})
		go func() {
			_ = sandbox.Close()
			close(closed)
		}()
		synctest.Wait()
		select {
		case <-closed:
			t.Fatal("Close returned while the remote was still running")
		default:
		}
		close(released)
		<-closed
	})
}
