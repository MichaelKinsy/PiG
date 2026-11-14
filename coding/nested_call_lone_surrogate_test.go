package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: agent-session.ts:1168-1173 sets `message.nestedCalls` on a tool result message from the record of the calls the tool made through
// ctx.executeTool() (nested-tool-calls.ts); the record's `error` is the nested tool's error text, a JavaScript string. The session file, an RPC event
// and an extension event are JSON.stringify of that message, which writes a lone UTF-16 surrogate as its escape.
func TestSessionNestedCallErrorKeepsALoneSurrogate(t *testing.T) {
	const unit = "\xed\xa0\xbd"
	inner := orchestrationTool("inner", "Fails.", extension.ToolDefinition{
		Exposure: extension.ToolExposureCodemode,
		Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return extension.AgentToolResult{}, errors.New("NE[" + unit + "]")
		},
	})
	outer := orchestrationTool("outer", "Calls inner.", extension.ToolDefinition{
		Execute: func(ctx context.Context, _ string, _ json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			_, _ = extension.ToolContextFromContext(ctx).ExecuteTool("inner", map[string]any{}, nil)
			return textResult("called"), nil
		},
	})
	session, faux := newOrchestrationSession(t, extension.Extension{Tools: map[string]extension.RegisteredTool{"inner": inner, "outer": outer}, ToolOrder: []string{"inner", "outer"}})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("outer", map[string]any{}, &ai.FauxToolCallOptions{ID: ""})}, StopReason: "toolUse"}),
		fauxText("done"),
	})
	if err := session.Prompt(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	result := toolResultOf(t, session)
	if result.NestedCalls == nil || len(result.NestedCalls.Calls) != 1 {
		t.Fatalf("nestedCalls = %+v, want the one call to inner", result.NestedCalls)
	}
	if got := result.NestedCalls.Calls[0].Error; !strings.Contains(got, "NE["+unit+"]") {
		t.Fatalf("the nested call's error = %q, want the nested tool's text with the unit", got)
	}
	var entry icodingagent.SessionEntry
	for _, candidate := range session.Inner().GetBranch() {
		if message, ok := candidate.(icodingagent.MessageEntry); ok && message.Message.ToolResult != nil {
			entry = candidate
			break
		}
	}
	if entry == nil {
		t.Fatal("the tool result is not in the session")
	}
	message := agent.AgentMessage{ToolResult: result}
	surfaces := []struct {
		name   string
		encode func() ([]byte, error)
	}{
		{"the session entry (JSON.stringify of the entry)", func() ([]byte, error) { return jsstring.MarshalJSON(entry) }},
		{"the message of an RPC event (JSON.stringify)", func() ([]byte, error) { return jsstring.MarshalJSON(message) }},
		{"the message of an extension event frame", func() ([]byte, error) { return sdkjson.Marshal(message) }},
	}
	for _, surface := range surfaces {
		text, err := surface.encode()
		if err != nil {
			t.Fatalf("%s: %v", surface.name, err)
		}
		if !bytes.Contains(text, []byte(`NE[\ud83d]`)) {
			at := max(bytes.Index(text, []byte("NE[")), 0)
			t.Errorf("%s writes the nested call's error as %q, want the escape of the unit", surface.name, text[at:min(len(text), at+16)])
		}
	}
}
