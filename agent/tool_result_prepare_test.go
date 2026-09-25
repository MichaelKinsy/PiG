package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestPrepareToolResultRunsAfterLateExtensionHooksBeforeHistory(t *testing.T) {
	tool := &fakeTool{name: "image-tool", mode: ToolModeSequential, content: "original"}
	provider := providerFromSeqs(toolCallSeq(struct{ id, name string }{"image", "image-tool"}), textSeq("done"))
	initial := []ai.ImageContent{{MimeType: "image/png", Data: "initial"}}
	prepared := false
	a := NewAgent(AgentOptions{Model: fakeTestModel(provider), Tools: []AgentTool{tool}, MaxTurns: 5,
		AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			return AfterToolCallResult{Images: &initial}
		}},
		PrepareToolResult: func(ctx context.Context, result AgentToolResult) AgentToolResult {
			if ctx.Err() != nil || len(result.Images) != 1 || result.Images[0].Data != "extension" {
				t.Errorf("final processor received %#v, context=%v", result, ctx.Err())
			}
			result.Images = []ai.ImageContent{{MimeType: "image/png", Data: "prepared"}}
			prepared = true
			return result
		},
	})
	a.AddAfterToolCallHook(func(_ context.Context, _, _ string, _ json.RawMessage, result AgentToolResult) AfterToolCallResult {
		if len(result.Images) != 1 || result.Images[0].Data != "initial" {
			t.Errorf("late hook received %#v", result.Images)
		}
		images := []ai.ImageContent{{MimeType: "image/png", Data: "extension"}}
		return AfterToolCallResult{Images: &images}
	})
	messages, err := a.Send(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ToolResult != nil {
			images := message.ToolResult.Images()
			if !prepared || len(images) != 1 || images[0].Data != "prepared" {
				t.Fatalf("persisted images=%#v", images)
			}
			return
		}
	}
	t.Fatal("tool result missing")
}
