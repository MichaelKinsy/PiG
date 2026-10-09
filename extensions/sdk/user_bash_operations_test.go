package sdk

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// A user_bash handler's `{ operations }` stays in the extension: the reply names it by handle, the host's user_bash_exec runs its Exec with
// the command, cwd, timeout and env while each onData chunk travels as a tool_update before the answer, and the host's release drops the
// object (types.ts UserBashEventResult, core/tools/bash.ts BashOperations).
func TestUserBashOperationsStayInTheExtensionUntilTheHostReleasesThem(t *testing.T) {
	ext := New("user-bash")
	var seen BashExecOptions
	var signalLive bool
	ext.OnEvent("user_bash", func(Context, map[string]any) (any, error) {
		return map[string]any{"operations": BashOperations{Exec: func(command, cwd string, options BashExecOptions) (BashExecResult, error) {
			seen = options
			signalLive = options.Signal != nil && options.Signal.Err() == nil
			options.OnData([]byte("one"))
			options.OnData([]byte{0xff, 0x00})
			if command == "reject" {
				return BashExecResult{}, errors.New("rejected: " + cwd)
			}
			code := 5
			return BashExecResult{ExitCode: &code}, nil
		}}}, nil
	})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	handlerID := reg.Handlers[0].HandlerID

	_, resp := runSurfaceRequest(t, host, "u1", &requestMsg{Method: "event", Event: "user_bash", HandlerID: handlerID, Args: json.RawMessage(`{"type":"user_bash","command":"x","excludeFromContext":false,"cwd":"/w"}`)}, nil)
	if resp.Error != nil || string(resp.Result) != `{"operations":{"handle":"bash-1"}}` {
		t.Fatalf("user_bash reply = %s error=%+v, want the operations named by handle", resp.Result, resp.Error)
	}

	exec := func(id, command string) ([]string, *responseMsg) {
		t.Helper()
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: &requestMsg{Method: requestUserBashExec, Tool: "bash-1", Args: json.RawMessage(`{"command":"` + command + `","cwd":"/w","timeout":1.5,"env":{"K":"V"}}`)}})
		var updates []string
		for {
			env := host.readEnvelope(t)
			switch env.Type {
			case msgNotify:
				if env.Notify.Method == "tool_update" {
					updates = append(updates, string(env.Notify.Args))
				}
			case msgResponse:
				return updates, env.Response
			}
		}
	}
	updates, resp := exec("e1", "run")
	if resp.Error != nil || string(resp.Result) != `{"exitCode":5}` {
		t.Fatalf("exec answer = %s error=%+v, want exit code 5", resp.Result, resp.Error)
	}
	if len(updates) != 2 || updates[0] != `{"request_id":"e1","result":{"data":"b25l"}}` || updates[1] != `{"request_id":"e1","result":{"data":"/wA="}}` {
		t.Fatalf("updates = %v, want the two chunks, in order, base64 encoded", updates)
	}
	if seen.Timeout == nil || *seen.Timeout != 1.5 || seen.Env["K"] != "V" || !signalLive {
		t.Fatalf("exec options = %+v, want the host's timeout and env and a live signal", seen)
	}
	if _, resp = exec("e2", "reject"); resp.Error == nil || resp.Error.Message != "rejected: /w" {
		t.Fatalf("a rejecting exec answered %s error=%+v, want its message", resp.Result, resp.Error)
	}

	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: notifyBashOperationsRelease, Args: json.RawMessage(`{"handle":"bash-1"}`)}})
	// The release is a notification: the next request is handled after it, so the released handle is unknown to it.
	if _, resp = exec("e3", "run"); resp.Error == nil || resp.Error.Message != "unknown bash operations: bash-1" {
		t.Fatalf("exec after the release answered %s error=%+v, want unknown bash operations", resp.Result, resp.Error)
	}
}

// Pi accepts exactly one of operations and result, and an operations object needs a function exec (runner.ts isUserBashEventResult). The
// reply of a result is left as it is, and an operations value without exec names no handle, so the host rejects it.
func TestUserBashRepliesWithoutExecNameNoHandle(t *testing.T) {
	for name, value := range map[string]any{
		"result":          map[string]any{"result": map[string]any{"output": "x", "exitCode": 0, "cancelled": false, "truncated": false}},
		"operations nil":  map[string]any{"operations": nil},
		"operations zero": map[string]any{"operations": BashOperations{}},
		"both":            map[string]any{"operations": BashOperations{Exec: func(string, string, BashExecOptions) (BashExecResult, error) { return BashExecResult{}, nil }}, "result": map[string]any{"output": "x", "exitCode": 0, "cancelled": false, "truncated": false}},
	} {
		ext := New("user-bash")
		reply, err := ext.userBashEventResult(value)
		if err != nil {
			continue
		}
		encoded, _ := json.Marshal(reply)
		var fields map[string]json.RawMessage
		if json.Unmarshal(encoded, &fields) == nil {
			if raw, ok := fields["operations"]; ok && string(raw) != "null" && string(raw) != "{}" {
				t.Errorf("%s: reply %s names an operations handle", name, encoded)
			}
		}
		if len(ext.bashOperations) != 0 {
			t.Errorf("%s: %d operations objects kept", name, len(ext.bashOperations))
		}
	}
}
