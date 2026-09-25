package compaction

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestSummarizationSystemPromptMatchesUpstream(t *testing.T) {
	want := `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`
	if SummarizationSystemPrompt != want {
		t.Fatalf("summarization system prompt mismatch:\n got: %q\nwant: %q", SummarizationSystemPrompt, want)
	}
}

// ─── SerializeConversation ────────────────────────────────────────────────────

func TestSerializeConversation_UserAndAssistant(t *testing.T) {
	msgs := []ai.Message{
		ai.UserMessage{Content: ai.UserText("Hello")},
		ai.AssistantMessage{Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "Hi there"},
		}},
	}
	got := SerializeConversation(msgs)
	if !strings.Contains(got, "[User]: Hello") {
		t.Errorf("expected [User]: Hello, got: %q", got)
	}
	if !strings.Contains(got, "[Assistant]: Hi there") {
		t.Errorf("expected [Assistant]: Hi there, got: %q", got)
	}
}

func TestSerializeConversation_ToolResultTruncation(t *testing.T) {
	long := strings.Repeat("x", 2500)
	msgs := []ai.Message{ai.ToolResultMessage{
		ToolCallID: "1", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: long}},
	}}
	got := SerializeConversation(msgs)
	if !strings.Contains(got, "[... 500 more characters truncated]") {
		t.Errorf("expected truncation marker, got: %q", got[:100])
	}
	if strings.Contains(got, strings.Repeat("x", 2001)) {
		t.Error("result should be truncated to 2000 chars")
	}
}

func TestSerializeConversation_AssistantThinking(t *testing.T) {
	msgs := []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ThinkingContent{Thinking: "internal plan"},
		ai.TextContent{Text: "response"},
	}}}
	got := SerializeConversation(msgs)
	if !strings.Contains(got, "[Assistant thinking]: internal plan") {
		t.Errorf("missing thinking block, got: %q", got)
	}
	if !strings.Contains(got, "[Assistant]: response") {
		t.Errorf("missing text block, got: %q", got)
	}
}

func TestSerializeConversation_AssistantToolCalls(t *testing.T) {
	msgs := []ai.Message{ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ToolCall{
			ID:        "call1",
			Name:      "read",
			Arguments: ai.JsonObject{"path": "/tmp/foo.go"},
		},
	}}}
	got := SerializeConversation(msgs)
	if !strings.Contains(got, "[Assistant tool calls]:") {
		t.Errorf("expected tool calls header, got: %q", got)
	}
	if !strings.Contains(got, "read(") {
		t.Errorf("expected tool name, got: %q", got)
	}
	if !strings.Contains(got, "path=") {
		t.Errorf("expected path arg, got: %q", got)
	}
}

// ─── ComputeFileLists ─────────────────────────────────────────────────────────

func TestComputeFileLists_ReadOnlyNotInModified(t *testing.T) {
	ops := NewFileOps()
	ops.Read["/a/b.go"] = struct{}{}
	read, mod := ComputeFileLists(ops)
	if len(mod) != 0 {
		t.Errorf("expected no modified, got %v", mod)
	}
	if len(read) != 1 || read[0] != "/a/b.go" {
		t.Errorf("expected read=[/a/b.go], got %v", read)
	}
}

func TestComputeFileLists_ModifiedNotInRead(t *testing.T) {
	ops := NewFileOps()
	ops.Written["/a/b.go"] = struct{}{}
	read, mod := ComputeFileLists(ops)
	if len(read) != 0 {
		t.Errorf("expected no read-only, got %v", read)
	}
	if len(mod) != 1 || mod[0] != "/a/b.go" {
		t.Errorf("expected mod=[/a/b.go], got %v", mod)
	}
}

func TestComputeFileLists_BothSorted(t *testing.T) {
	ops := NewFileOps()
	ops.Read["c.go"] = struct{}{}
	ops.Read["a.go"] = struct{}{}
	ops.Written["z.go"] = struct{}{}
	ops.Written["m.go"] = struct{}{}
	read, mod := ComputeFileLists(ops)
	if read[0] != "a.go" || read[1] != "c.go" {
		t.Errorf("read not sorted: %v", read)
	}
	if mod[0] != "m.go" || mod[1] != "z.go" {
		t.Errorf("mod not sorted: %v", mod)
	}
}

// ─── FormatFileOperations ─────────────────────────────────────────────────────

func TestFormatFileOperations_BothEmpty(t *testing.T) {
	got := FormatFileOperations(nil, nil)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestFormatFileOperations_ReadOnly(t *testing.T) {
	got := FormatFileOperations([]string{"a.go"}, nil)
	if !strings.Contains(got, "<read-files>") {
		t.Errorf("expected read-files tag, got %q", got)
	}
	if strings.Contains(got, "<modified-files>") {
		t.Errorf("unexpected modified-files tag, got %q", got)
	}
	if !strings.HasPrefix(got, "\n\n") {
		t.Errorf("expected \\n\\n prefix, got %q", got[:4])
	}
}

func TestFormatFileOperations_Modified(t *testing.T) {
	got := FormatFileOperations(nil, []string{"b.go"})
	if strings.Contains(got, "<read-files>") {
		t.Errorf("unexpected read-files tag, got %q", got)
	}
	if !strings.Contains(got, "<modified-files>") {
		t.Errorf("expected modified-files tag, got %q", got)
	}
}

func TestFormatFileOperations_Both(t *testing.T) {
	got := FormatFileOperations([]string{"a.go"}, []string{"b.go"})
	if !strings.Contains(got, "<read-files>") || !strings.Contains(got, "<modified-files>") {
		t.Errorf("expected both tags, got %q", got)
	}
	// The two sections must be separated by \n\n
	if !strings.Contains(got, "</read-files>\n\n<modified-files>") {
		t.Errorf("expected \\n\\n between sections, got %q", got)
	}
}

// ─── ExtractFileOpsFromMessage ────────────────────────────────────────────────

func TestExtractFileOpsFromMessage_ToolCalls(t *testing.T) {
	msg := agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role: "assistant",
			Content: []ai.AssistantContentBlock{
				ai.ToolCall{ID: "1", Name: "read", Arguments: ai.JsonObject{"path": "r.go"}},
				ai.ToolCall{ID: "2", Name: "write", Arguments: ai.JsonObject{"path": "w.go"}},
				ai.ToolCall{ID: "3", Name: "edit", Arguments: ai.JsonObject{"path": "e.go"}},
			},
		},
	}
	ops := NewFileOps()
	ExtractFileOpsFromMessage(msg, &ops)
	if _, ok := ops.Read["r.go"]; !ok {
		t.Error("expected r.go in Read")
	}
	if _, ok := ops.Written["w.go"]; !ok {
		t.Error("expected w.go in Written")
	}
	if _, ok := ops.Edited["e.go"]; !ok {
		t.Error("expected e.go in Edited")
	}
}

func TestExtractFileOpsFromMessage_NonAssistant(t *testing.T) {
	msg := agent.AgentMessage{
		User: &agent.UserMessage{
			Role:    "user",
			Content: []ai.UserContentBlock{ai.TextContent{Text: "hi"}},
		},
	}
	ops := NewFileOps()
	ExtractFileOpsFromMessage(msg, &ops)
	if len(ops.Read)+len(ops.Written)+len(ops.Edited) != 0 {
		t.Error("user message should not populate file ops")
	}
}
