package sdk

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

type execSignalOutcome struct {
	result *ExecResult
	err    error
}

// execSignalHost starts a tool that runs `sleep` with the given signal and returns the host and the channel of its outcome. The
// caller finishes the test with the returned stop.
func execSignalHost(t *testing.T, signal context.Context) (host *mockHost, outcome chan execSignalOutcome, stop func()) {
	t.Helper()
	ext := New("exec-signal")
	outcome = make(chan execSignalOutcome, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			result, err := ctx.ExecWithOptions("sleep", []string{"30"}, ExecOptions{Timeout: 5000, Signal: signal})
			outcome <- execSignalOutcome{result, err}
			return "done", err
		},
	})
	var done chan error
	host, _, done = surfaceHost(t, ext, nil)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-1", Args: json.RawMessage(`{}`)}})
	return host, outcome, func() {
		watchdog.Stop()
		surfaceShutdown(t, host, done)
	}
}

func nextCall(t *testing.T, host *mockHost) envelope {
	t.Helper()
	for {
		if env := host.readEnvelope(t); env.Type == msgCall {
			return env
		}
	}
}

// core/exec.ts:46-52: options.signal aborting kills the command, and the call has no other signal. The SDK names the call by an exec
// id, sends it outside the calling request, and reports the abort with exec.cancel after the call's frame; the host answers the call
// with what the command wrote, killed and code 0.
func TestExecWithSignalSendsExecCancelWhenTheSignalEnds(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	host, outcome, stop := execSignalHost(t, signal)
	defer stop()
	call := nextCall(t, host)
	if call.Call.Method != "exec" || call.Call.ParentRequestID != "" {
		t.Fatalf("call = %s with parent %q, want exec without the calling request as parent", call.Call.Method, call.Call.ParentRequestID)
	}
	var args map[string]any
	if err := json.Unmarshal(call.Call.Args, &args); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"command": "sleep", "args": []any{"30"}, "options": map[string]any{"timeout": float64(5000)}, "execId": args["execId"], "ownSignal": true}
	if !reflect.DeepEqual(args, want) || args["execId"] == "" || args["execId"] == nil {
		t.Fatalf("exec args = %#v, want %#v with an exec id", args, want)
	}
	abort()
	cancel := nextCall(t, host)
	if cancel.Call.Method != "exec.cancel" || string(cancel.Call.Args) != `{"execId":"`+args["execId"].(string)+`"}` || cancel.Call.ParentRequestID != "" {
		t.Fatalf("cancel call = %s %s (parent %q)", cancel.Call.Method, cancel.Call.Args, cancel.Call.ParentRequestID)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: cancel.ID, CallResult: &callResultMsg{}})
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"stdout":"so far","stderr":"","code":0,"killed":true}`)}})
	got := recv(t, outcome)
	if got.err != nil || got.result == nil || *got.result != (ExecResult{Stdout: "so far", ExitCode: 0, Killed: true}) {
		t.Fatalf("outcome = %+v, %v", got.result, got.err)
	}
}

// A signal that is done before the call still sends the call first and the cancel behind it, so the host starts the command and then ends it, as exec.ts does.
func TestExecWithASignalThatIsDoneAlreadySendsTheCancelBehindTheCall(t *testing.T) {
	signal, abort := context.WithCancel(context.Background())
	abort()
	host, outcome, stop := execSignalHost(t, signal)
	defer stop()
	call := nextCall(t, host)
	cancel := nextCall(t, host)
	if call.Call.Method != "exec" || cancel.Call.Method != "exec.cancel" {
		t.Fatalf("calls = %s, %s; want exec then exec.cancel", call.Call.Method, cancel.Call.Method)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: cancel.ID, CallResult: &callResultMsg{}})
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"stdout":"","stderr":"","code":0,"killed":true}`)}})
	if got := recv(t, outcome); got.err != nil || got.result == nil || !got.result.Killed {
		t.Fatalf("outcome = %+v, %v", got.result, got.err)
	}
}

// The signal replaces the calling request's cancellation: cancelling the request neither ends the command nor returns from the call.
func TestExecWithSignalIgnoresTheCallingRequestCancellation(t *testing.T) {
	host, outcome, stop := execSignalHost(t, t.Context())
	defer stop()
	call := nextCall(t, host)
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-outer"})
	select {
	case got := <-outcome:
		t.Fatalf("exec ended with the calling request: %+v, %v", got.result, got.err)
	case <-time.After(200 * time.Millisecond):
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"stdout":"finished","code":0,"killed":false}`)}})
	if got := recv(t, outcome); got.err != nil || got.result == nil || got.result.Stdout != "finished" {
		t.Fatalf("outcome = %+v, %v", got.result, got.err)
	}
}

// Without a signal the call stays tied to the calling request and sends no exec id.
func TestExecWithoutASignalStaysTiedToTheRequest(t *testing.T) {
	ext := New("exec-signal")
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			_, err := ctx.ExecWithOptions("sleep", nil, ExecOptions{Cwd: "/work"})
			return "done", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-1", Args: json.RawMessage(`{}`)}})
	call := nextCall(t, host)
	var args map[string]any
	if err := json.Unmarshal(call.Call.Args, &args); err != nil {
		t.Fatal(err)
	}
	if _, has := args["execId"]; has || args["ownSignal"] != nil || call.Call.ParentRequestID != "req-outer" {
		t.Fatalf("exec = %#v (parent %q), want the request's call without an exec id", args, call.Call.ParentRequestID)
	}
}
