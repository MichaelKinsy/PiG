package sdk

import (
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// capturingComponent is a component whose capturesViewportInput the test flips.
type capturingComponent struct{ captures bool }

func (c *capturingComponent) Render(int) []string { return []string{"x"} }
func (c *capturingComponent) Invalidate()         {}
func (c *capturingComponent) CapturesViewportInput() bool {
	return c.captures
}
func (c *capturingComponent) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}

func TestCustomFrameCarriesCapturesViewportInputOnlyWhenTrue(t *testing.T) {
	component := &capturingComponent{}
	o := &remoteOverlay{component: component}
	if got := o.frameArgs(map[string]any{}); !reflect.DeepEqual(got, map[string]any{}) {
		t.Fatalf("frame args with capture off = %v, want none", got)
	}
	component.captures = true
	if got := o.frameArgs(map[string]any{}); !reflect.DeepEqual(got, map[string]any{"capturesViewportInput": true}) {
		t.Fatalf("frame args with capture on = %v", got)
	}
	plain := &remoteOverlay{component: plainRemote{}}
	if got := plain.frameArgs(map[string]any{}); len(got) != 0 {
		t.Fatalf("frame args of a component without the member = %v", got)
	}
}

type plainRemote struct{}

func (plainRemote) Render(int) []string { return nil }
func (plainRemote) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}

func TestTuiAltCapsAndCallbackReadTheComponentValue(t *testing.T) {
	s := identityScreen()
	component := &capturingComponent{}
	fields, err := s.componentFields(component)
	if err != nil {
		t.Fatal(err)
	}
	caps := fields["caps"].(map[string]any)
	if caps["capturesViewportInput"] != true {
		t.Fatalf("caps = %v, want capturesViewportInput true for a component with the member", caps)
	}
	plain, err := s.componentFields(&identityPtr{text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if plain["caps"].(map[string]any)["capturesViewportInput"] != false {
		t.Fatalf("caps of a component without the member = %v", plain["caps"])
	}
	ext := &Extension{}
	ext.tuiAlt.screens = map[string]*TuiAltScreen{"s1": s}
	ask := func() any {
		raw, _ := json.Marshal(map[string]any{"id": "s1", "kind": "capturesViewportInput", "key": fields["key"]})
		out, err := ext.dispatchTuiAlt(Context{ext: ext, requestID: "cb"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		return out.(map[string]any)["captures"]
	}
	if ask() != false {
		t.Fatal("callback answered true before the component captured")
	}
	component.captures = true
	if ask() != true {
		t.Fatal("callback did not read the component's current value")
	}
}

func TestExecMaxBufferWireShapeAndTruncatedResult(t *testing.T) {
	ext := New("exec-limits")
	outcome := make(chan execSignalOutcome, 1)
	limit := float64(10)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			result, err := ctx.ExecWithOptions("yes", []string{"y"}, ExecOptions{MaxBuffer: &limit})
			outcome <- execSignalOutcome{result, err}
			return "done", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-1", Args: json.RawMessage(`{}`)}})
	call := nextCall(t, host)
	if call.Call.Method != "exec" || string(call.Call.Args) != `{"args":["y"],"command":"yes","options":{"maxBuffer":10}}` {
		t.Fatalf("exec call = %s %s", call.Call.Method, call.Call.Args)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(`{"stdout":"y\ny\ny\ny\ny\n","stderr":"","code":1,"killed":true,"truncated":true}`)}})
	got := recv(t, outcome)
	if got.err != nil || got.result == nil || !got.result.Truncated || !got.result.Killed || got.result.ExitCode != 1 {
		t.Fatalf("outcome = %+v, %v", got.result, got.err)
	}
}
