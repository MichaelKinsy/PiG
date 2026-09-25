package codingagent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// writePoisonedSession writes a session JSONL whose final assistant turn errored
// while holding the only tool_use for a persisted tool result: the exact abort
// race D48 documents (the tool ran and recorded its result before the turn was
// marked errored). Returns the file path.
func writePoisonedSession(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "2025-01-01T00-00-00-000Z_poison.jsonl")
	lines := []string{
		`{"type":"session","version":` + strconv.Itoa(CurrentSessionVersion) + `,"id":"poison","timestamp":"2025-01-01T00:00:00.000Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"run it"}]}}`,
		`{"type":"message","id":"a1","parentId":"u1","timestamp":"2025-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_x","name":"bash","arguments":{"command":"expr 20 + 22"}}],"api":"openai-completions","provider":"openai","model":"gpt-test","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"error","errorMessage":"connection reset","timestamp":1735689602000}}`,
		`{"type":"message","id":"t1","parentId":"a1","timestamp":"2025-01-01T00:00:03.000Z","message":{"role":"toolResult","toolCallId":"call_x","toolName":"bash","content":[{"type":"text","text":"42\n"}],"isError":false,"timestamp":1735689603000}}`,
	}
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write poisoned session: %v", err)
	}
	return path
}

// TestResumePoisonedSessionReconstructsErroredToolUseChain proves the persistence
// half of the D48 risk-core surface: a poisoned session on disk must reconstruct
// through loadSessionFile -> BuildContext with the poison intact. If reconstruction
// silently dropped stopReason/errorMessage or failed to rebuild the tool_result,
// the errored turn would look normal and the orphan would never form: the D48
// guard (proven separately on synthetic in-memory messages) would be dead code for
// real resumed sessions. This binds the reconstruction contract to that guard.
func TestResumePoisonedSessionReconstructsErroredToolUseChain(t *testing.T) {
	sess, err := loadSessionFile(writePoisonedSession(t))
	if err != nil {
		t.Fatalf("load poisoned session: %v", err)
	}

	msgs := sess.BuildContext(nil)
	if len(msgs) != 3 {
		t.Fatalf("BuildContext = %d messages, want 3 (user, errored assistant, tool result)", len(msgs))
	}

	// Reconstruction must preserve the errored assistant turn and its tool_use.
	asst := msgs[1].Assistant
	if asst == nil {
		t.Fatalf("msgs[1] is not an assistant message: %+v", msgs[1])
	}
	if asst.StopReason != "error" {
		t.Errorf("reconstructed assistant StopReason = %q, want %q (poison lost on resume)", asst.StopReason, "error")
	}
	if asst.ErrorMessage != "connection reset" {
		t.Errorf("reconstructed assistant ErrorMessage = %q, want %q", asst.ErrorMessage, "connection reset")
	}
	if id := assistantToolUseID(asst); id != "call_x" {
		t.Errorf("reconstructed assistant tool_use id = %q, want %q", id, "call_x")
	}

	// Reconstruction must rebuild the persisted tool_result referencing that call.
	tr := msgs[2].ToolResult
	if tr == nil {
		t.Fatalf("msgs[2] is not a tool-result message: %+v", msgs[2])
	}
	if tr.ToolCallID != "call_x" {
		t.Errorf("reconstructed tool_result ToolCallID = %q, want %q", tr.ToolCallID, "call_x")
	}

	// End-to-end: the reconstructed poisoned chain, run through the same
	// normalization the resume->Send path uses, must reach D48: the errored
	// assistant is dropped and its now-orphaned tool result is stripped, leaving
	// a provider-valid chain with no dangling tool_use output.
	normalized := agent.NormalizeMessages(msgs, nil)
	for i, m := range normalized {
		if m.ToolResult != nil {
			t.Errorf("normalized[%d] still carries an orphaned tool_result: %+v", i, m.ToolResult)
		}
		if m.Assistant != nil && m.Assistant.StopReason == "error" {
			t.Errorf("normalized[%d] still carries the errored assistant turn", i)
		}
	}
	if got := len(normalized); got != 1 {
		t.Fatalf("normalized chain = %d messages, want 1 (just the user turn); D48 did not fire on the resumed chain", got)
	}
	if normalized[0].User == nil {
		t.Errorf("surviving message is not the user turn: %+v", normalized[0])
	}
}

// assistantToolUseID returns the id of the first tool_use block in an assistant
// message, or "" when there is none.
func assistantToolUseID(m *agent.AssistantMessage) string {
	for _, b := range m.Content {
		if tu, ok := b.(ai.ToolCall); ok {
			return tu.ID
		}
	}
	return ""
}
