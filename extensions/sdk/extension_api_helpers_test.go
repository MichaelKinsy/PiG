package sdk

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// runSurfaceRequest sends one host request and answers each host call with answer until the extension responds. It returns the calls in order and the response.
func runSurfaceRequest(t *testing.T, host *mockHost, id string, req *requestMsg, answer func(call *callMsg) *callResultMsg) ([]*callMsg, *responseMsg) {
	t.Helper()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: id, Request: req})
	var calls []*callMsg
	for {
		env := host.readEnvelope(t)
		switch env.Type {
		case msgCall:
			calls = append(calls, env.Call)
			host.writeEnvelope(t, envelope{Type: msgCallResult, ID: env.ID, CallResult: answer(env.Call)})
		case msgResponse:
			if env.ID != id {
				t.Fatalf("response for %q, want %q", env.ID, id)
			}
			return calls, env.Response
		}
	}
}

// runSurfaceTool runs the tool name as call callID.
func runSurfaceTool(t *testing.T, host *mockHost, name, callID string, answer func(call *callMsg) *callResultMsg) ([]*callMsg, *responseMsg) {
	t.Helper()
	return runSurfaceRequest(t, host, "req-"+callID, &requestMsg{Method: "tool_call", Tool: name, ToolCallID: callID, Args: json.RawMessage(`{}`)}, answer)
}

func sendState(t *testing.T, host *mockHost, state string) {
	t.Helper()
	host.writeEnvelope(t, envelope{Type: msgNotify, Notify: &notifyMsg{Method: "state_update", Args: json.RawMessage(`{"state":` + state + `}`)}})
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// recv receives one value the extension's handler sent, and fails the test when the handler never sends one, for example because it failed first.
func recv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("the extension's handler did not report")
		panic("unreachable")
	}
}

// registerVirtualModel registers a virtual model before Run, where only a model without a route is refused.
func registerVirtualModel(t *testing.T, ext *Extension, model VirtualModel) {
	t.Helper()
	if err := ext.RegisterVirtualModel(model); err != nil {
		t.Fatal(err)
	}
}
