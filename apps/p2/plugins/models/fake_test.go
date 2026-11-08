package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// The fake provider's language (plugins/models/fake.ts) is the test language of Pi's suite: m1.test.ts and the e2e files
// drive it (`rename-session <title>`, `slow…`, `<tool> <json>`). Pi has no unit test of fake.ts; these pin its answers.

func ask(t *testing.T, models *ai.Models, messages ...ai.Message) *ai.AssistantMessage {
	t.Helper()
	model := models.GetModel("faux", "faux-1")
	if model == nil {
		t.Fatal("no faux model")
	}
	result, err := models.StreamSimple(context.Background(), model, ai.Context{Messages: messages}).ResultContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func answer(message *ai.AssistantMessage) (text string, calls []ai.ToolCall) {
	for _, block := range message.Content {
		switch block := block.(type) {
		case ai.TextContent:
			text += block.Text
		case ai.ToolCall:
			calls = append(calls, block)
		}
	}
	return text, calls
}

func TestFakeModelsEchoAndScriptedTools(t *testing.T) {
	models := CreateFakeModels(ai.CreateModelsOptions{})
	text, calls := answer(ask(t, models, ai.UserMessage{Content: ai.UserText("hello")}))
	if text != "Echo: hello" || len(calls) != 0 {
		t.Fatalf("echo: %q %v", text, calls)
	}
	text, calls = answer(ask(t, models, ai.UserMessage{Content: ai.UserText("rename-session Greeting the agent")}))
	if text != "Running rename_session." || len(calls) != 1 || calls[0].Name != "rename_session" || calls[0].Arguments["title"] != "Greeting the agent" {
		t.Fatalf("rename-session: %q %v", text, calls)
	}
	// The sender block in front of the text is not echoed.
	text, _ = answer(ask(t, models, ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: runtime.SenderPrefix + `kind="user"/>`}, ai.TextContent{Text: "hi"}}}))
	if text != "Echo: hi" {
		t.Fatalf("sender block: %q", text)
	}
	text, calls = answer(ask(t, models, ai.UserMessage{Content: ai.UserText(`artifact {"name":"a"}`)}))
	if len(calls) != 1 || calls[0].Name != "artifact" || calls[0].Arguments["name"] != "a" {
		t.Fatalf("<tool> <json>: %q %v", text, calls)
	}
	// After a tool result the answer echoes again.
	text, calls = answer(ask(t, models,
		ai.UserMessage{Content: ai.UserText("tool")},
		ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "1", Name: "read", Arguments: map[string]any{"path": "fixture.txt"}}}, API: "p2-faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse},
		ai.ToolResultMessage{ToolCallID: "1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}},
	))
	if text != "Echo: tool" || len(calls) != 0 {
		t.Fatalf("after the result: %q %v", text, calls)
	}
	text, _ = answer(ask(t, models, ai.UserMessage{Content: ai.UserText("slow one")}))
	if text != "Echo: slow one "+strings.Repeat("streaming ", 300) {
		t.Fatalf("slow: %q", text[:min(len(text), 40)])
	}
	text, _ = answer(ask(t, models, ai.UserMessage{Content: ai.UserText("json {\"a\":1}")}))
	if text != `{"a":1}` {
		t.Fatalf("json: %q", text)
	}
}

func TestFakeModelsOneShotSampleAndRequestLog(t *testing.T) {
	log := filepath.Join(t.TempDir(), "requests.jsonl")
	t.Setenv("P2_FAKE_REQUESTS", log)
	models := CreateFakeModels(ai.CreateModelsOptions{})
	system := ai.SystemMessage{Content: ai.SystemText("Answer " + oneShotSchemaMarker + `{"type":"object","properties":{"n":{"type":"integer","minimum":3},"s":{"enum":["a","b"]},"l":{"type":"array","items":{"type":"string","minLength":2}}}}` + "\nbye")}
	text, _ := answer(ask(t, models, system, ai.UserMessage{Content: ai.UserText("do it")}))
	if text != `{"n":3,"s":"a","l":["xx"]}` {
		t.Fatalf("sample: %q", text)
	}
	// A task starting `json ` or `slow` is answered as a test prompt, not with a sample.
	text, _ = answer(ask(t, models, system, ai.UserMessage{Content: ai.UserText("slow x")}))
	if !strings.HasPrefix(text, "Echo: slow x ") {
		t.Fatalf("slow task: %.30q", text)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], `[{"role":"system"`) {
		t.Fatalf("request log: %s", data)
	}
}
