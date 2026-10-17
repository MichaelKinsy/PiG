package sdk

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt: in a before_agent_start handler, event.systemPrompt and
// ctx.getSystemPrompt() render the emission's shared options when read, so the handler sees its own edit at once. The
// unedited options read the prompt the host sent; an edit is rendered by the host once and then reused.
func TestBeforeAgentStartSystemPromptRendersTheCurrentOptions(t *testing.T) {
	ext := New("render-prompt")
	var seen []string
	read := func(ctx Context, data map[string]any) {
		event, err := data["systemPrompt"].(*SystemPrompt).Get()
		if err != nil {
			t.Error(err)
		}
		current, err := ctx.GetSystemPrompt()
		if err != nil {
			t.Error(err)
		}
		seen = append(seen, event+" "+current)
	}
	ext.OnEvent(EventBeforeAgentStart, func(ctx Context, data map[string]any) (any, error) {
		read(ctx, data)
		data["systemPromptOptions"].(map[string]any)["appendSystemPrompt"] = "EDITED"
		read(ctx, data)
		read(ctx, data)
		encoded, err := json.Marshal(data["systemPrompt"])
		if err != nil {
			return nil, err
		}
		seen = append(seen, string(encoded))
		return nil, nil
	})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	args := json.RawMessage(`{"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"customPrompt":"BASE","sections":{}}}`)
	calls, resp := runSurfaceRequest(t, host, "bas-1", &requestMsg{Method: "event", Event: EventBeforeAgentStart, HandlerID: reg.Handlers[0].HandlerID, Args: args}, func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(`{"prompt":"RENDERED"}`)}
	})
	if resp.Error != nil {
		t.Fatalf("response error = %+v", resp.Error)
	}
	if len(calls) != 1 || calls[0].Method != "renderSystemPrompt" {
		t.Fatalf("calls = %+v", calls)
	}
	var call struct {
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(calls[0].Args, &call); err != nil {
		t.Fatal(err)
	}
	if call.Options["customPrompt"] != "BASE" || call.Options["appendSystemPrompt"] != "EDITED" {
		t.Fatalf("rendered options = %s", calls[0].Args)
	}
	if want := []string{"RECEIVED RECEIVED", "RENDERED RENDERED", "RENDERED RENDERED", `"RENDERED"`}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen = %q, want %q", seen, want)
	}
}

// system-prompt.ts:71-75: an invalid section makes the render, and so the getter, fail.
func TestBeforeAgentStartSystemPromptRenderErrorIsTheReadersError(t *testing.T) {
	ext := New("render-prompt-error")
	ext.OnEvent(EventBeforeAgentStart, func(ctx Context, data map[string]any) (any, error) {
		data["systemPromptOptions"].(map[string]any)["sections"].(*SystemPromptSections).Set("preamble", "x")
		_, err := ctx.GetSystemPrompt()
		return nil, err
	})
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	args := json.RawMessage(`{"prompt":"hi","systemPrompt":"RECEIVED","systemPromptOptions":{"sections":{}}}`)
	_, resp := runSurfaceRequest(t, host, "bas-1", &requestMsg{Method: "event", Event: EventBeforeAgentStart, HandlerID: reg.Handlers[0].HandlerID, Args: args}, func(*callMsg) *callResultMsg {
		return &callResultMsg{Error: &errorInfo{Message: "invalid section preamble"}}
	})
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "invalid section preamble") {
		t.Fatalf("response error = %+v", resp.Error)
	}
}
