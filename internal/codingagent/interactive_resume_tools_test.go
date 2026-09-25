package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestFileToolErrorsUseResultNotSuccessDetails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details any
		color   string
	}{
		{"write", &tools.WriteDetails{Path: "preview.txt", Content: "PREVIEW"}, tui.ActiveTheme().Error},
		{"read", &tools.ReadDetails{Path: "read.go"}, tui.ActiveTheme().ToolOutput},
		{"edit", &tools.EditToolDetails{Diff: "STALE_DIFF"}, tui.ActiveTheme().Error},
	} {
		t.Run(tc.name, func(t *testing.T) {
			render := toolBodyRenderer(tc.name, agent.AgentToolResult{Content: "ERROR_RESULT", Details: tc.details, IsError: true}, nil)
			got := strings.Join(render(80, true), "\n")
			if !strings.Contains(got, tc.color+"ERROR_RESULT") || strings.Contains(got, "STALE_DIFF") {
				t.Fatalf("error result rendering = %q", got)
			}
			if tc.name == "write" && !strings.Contains(got, "PREVIEW") {
				t.Fatalf("write error lost call preview: %q", got)
			}
		})
	}
}

// Pi's shell renderer shows Took only when executionStarted recorded startedAt.
// Rebuilding a retained shell call must not fabricate a measured zero duration.
func TestInteractiveMode_ResumeShellToolsHaveNoDuration(t *testing.T) {
	for _, name := range []string{"bash", "powershell"} {
		t.Run(name, func(t *testing.T) {
			call := ai.ToolCall{ID: "shell-call", Name: name, Arguments: ai.JsonObject{"command": "echo done"}}
			m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", call), agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
				Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}},
			}})
			for _, expanded := range []bool{false, true} {
				m.toolsExpanded = expanded
				m.rebuildChatFromSession()
				got := renderedChat(m)
				if !strings.Contains(got, "done") || strings.Contains(got, "Took") || strings.Contains(got, "Elapsed") {
					t.Fatalf("resumed shell expanded=%t: %q", expanded, got)
				}
			}
		})
	}
}

func TestResumeAbortedToolIgnoresOrphanedResult(t *testing.T) {
	call := ai.ToolCall{ID: "aborted-read", Name: "read", Arguments: ai.JsonObject{"path": "file.go"}}
	msg := assistantMsg("", call)
	msg.Assistant.StopReason = ai.StopReasonAborted
	m := resumeThinkingMode(t, false, userMsg("question"), msg, agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ORPHAN_RESULT"}},
	}})
	m.renderSessionEntries()
	if got := renderedChat(m); !strings.Contains(got, "Operation aborted") || strings.Contains(got, "ORPHAN_RESULT") {
		t.Fatalf("orphaned result replaced aborted card: %q", got)
	}
}

// Pi redraws write previews from toolCall.arguments, read highlighting from
// the call path, and edit diffs from persisted result.details. Runtime-only
// typed Go details must not be required after the session is decoded.
func TestInteractiveMode_ResumeFileToolPresentation(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		args                        ai.JsonObject
		output                      string
		details                     any
		collapsed, expanded, absent string
	}{
		{"write", ai.JsonObject{"path": "preview.txt", "content": "preview first\npreview second\n"}, "Successfully wrote to preview.txt", nil, "preview first\n", "preview second", "Successfully wrote"},
		{"write", ai.JsonObject{"file_path": "preview.txt", "content": strings.Repeat("preview line\n", 12)}, "Successfully wrote to preview.txt", &tools.WriteDetails{Path: "preview.txt", Content: "stale details"}, "... (2 more lines, 12 total,", "preview line", "stale details"},
		{"write", ai.JsonObject{"path": "empty.txt", "content": ""}, "Successfully wrote to empty.txt", nil, "write", "write", "Successfully wrote"},
		{"read", ai.JsonObject{"path": "read.txt"}, "READ_BODY", nil, "read", "READ_BODY", ""},
		{"read", ai.JsonObject{"file_path": "read.txt"}, "READ_BODY", &tools.ReadDetails{Path: "read.txt"}, "read", "READ_BODY", ""},
		{"edit", ai.JsonObject{"path": "edit.txt", "oldText": "before", "newText": "after"}, "Successfully replaced text in edit.txt.", map[string]any{"diff": "-1 before\n+1 after", "firstChangedLine": 1}, "before", "after", "Successfully replaced"},
	} {
		t.Run(tc.name+"/"+tc.collapsed, func(t *testing.T) {
			call := ai.ToolCall{ID: "file-call", Name: tc.name, Arguments: tc.args}
			m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", call), agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
				Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: tc.name, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: tc.output}}, Details: tc.details,
			}})
			m.toolsExpanded = false
			m.renderSessionEntries()
			got := renderedChat(m)
			if !strings.Contains(got, strings.TrimSuffix(tc.collapsed, "\n")) || tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("collapsed initial render = %q; want %q, not %q", got, tc.collapsed, tc.absent)
			}
			if tc.name == "read" && strings.Contains(got, "READ_BODY") {
				t.Errorf("collapsed successful read must hide the body: %q", got)
			}
			m.toolsExpanded = true
			m.rebuildChatFromSession()
			got = renderedChat(m)
			if !strings.Contains(got, tc.expanded) || tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("expanded redraw = %q; want %q, not %q", got, tc.expanded, tc.absent)
			}
			m.toolsExpanded = false
			m.rebuildChatFromSession()
			got = renderedChat(m)
			if !strings.Contains(got, strings.TrimSuffix(tc.collapsed, "\n")) || tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("collapsed redraw = %q; want %q, not %q", got, tc.collapsed, tc.absent)
			}
		})
	}
}
