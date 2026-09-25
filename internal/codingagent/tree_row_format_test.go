// e tests: type-aware /tree row rendering.
//
// One fixture per upstream entry-type case + one fixture per
// per-tool formatter branch + multi-line preview + long-arg
// truncation + unknown-type fallback.

package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// mustEntry constructs a SessionEntry from any value that marshals to
// valid JSON, populating both raw and Base.
func mustEntry(t *testing.T, v any) SessionEntry {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var base SessionEntryBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("unmarshal base: %v", err)
	}
	return SessionEntry{raw: raw, Base: base}
}

func TestFormatTreeRow_AllEntryTypes(t *testing.T) {
	f := newTreeRowFormatter(nil)
	th := tui.ActiveTheme()

	// Pre-seed toolCallMap so the tool_result fixtures resolve.
	f.toolCallMap["call-read-1"] = toolCallInfo{name: "read", input: map[string]any{"path": "foo.go"}}
	f.toolCallMap["call-bash-1"] = toolCallInfo{name: "bash", input: map[string]any{"command": "ls -la"}}

	cases := []struct {
		name  string
		entry SessionEntry
		want  string
	}{
		{
			name: "message_user_simple",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "u1", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"type": "text", "text": "hello world"}},
				},
			}),
			want: fg(th.Accent, "user: ") + "hello world",
		},
		{
			name: "message_user_multiline_first_line_only_no_newlines",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "u2", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":    "user",
					"content": []any{map[string]any{"type": "text", "text": "line one\nline two\nline three"}},
				},
			}),
			// normalizeText replaces \n with space and trims.
			want: fg(th.Accent, "user: ") + "line one line two line three",
		},
		{
			name: "message_assistant_with_text",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "a1", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{map[string]any{"type": "text", "text": "sure thing"}},
				},
			}),
			want: fg(th.Success, "assistant: ") + "sure thing",
		},
		{
			name: "message_assistant_no_content",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "a2", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":    "assistant",
					"content": []any{},
				},
			}),
			want: fg(th.Success, "assistant: ") + fg(th.Muted, "(no content)"),
		},
		{
			name: "message_tool_result_resolves_via_toolCallMap_read",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "tr1", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role": "toolResult", "toolCallId": "call-read-1", "toolName": "read",
					"content": []any{map[string]any{"type": "text", "text": "<file contents>"}},
					"isError": false, "timestamp": 1,
				},
			}),
			want: fg(th.Muted, "[read: foo.go]"),
		},
		{
			name: "message_tool_result_unknown_id_falls_back_to_bracketed_tool",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "tr2", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role": "toolResult", "toolCallId": "no-such-call", "toolName": "read",
					"content": []any{map[string]any{"type": "text", "text": "out"}},
					"isError": false, "timestamp": 1,
				},
			}),
			want: fg(th.Muted, "[tool]"),
		},
		{
			name: "message_unknown_role_renders_as_bracketed_role",
			entry: mustEntry(t, map[string]any{
				"type": "message", "id": "be1", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":    "bashExecution",
					"content": []any{},
				},
			}),
			// will replace this with `[bash]: <command>`.
			want: fg(th.Dim, "[bashExecution]"),
		},
		{
			name: "model_change",
			entry: mustEntry(t, map[string]any{
				"type": "model_change", "id": "m1", "parentId": nil, "timestamp": "",
				"provider": "github-copilot", "modelId": "gpt-4o-mini",
			}),
			want: fg(th.Dim, "[model: gpt-4o-mini]"),
		},
		{
			name: "thinking_level_change",
			entry: mustEntry(t, map[string]any{
				"type": "thinking_level_change", "id": "tl1", "parentId": nil, "timestamp": "",
				"thinkingLevel": "high",
			}),
			want: fg(th.Dim, "[thinking: high]"),
		},
		{
			name: "compaction_rounds_to_k",
			entry: mustEntry(t, map[string]any{
				"type": "compaction", "id": "c1", "parentId": nil, "timestamp": "",
				"summary": "...", "firstKeptEntryId": "x", "tokensBefore": 12500,
			}),
			// 12500/1000 = 12.5, math.Round → 13.
			want: fg(th.BorderAccent, "[compaction: 13k tokens]"),
		},
		{
			name: "branch_summary",
			entry: mustEntry(t, map[string]any{
				"type": "branch_summary", "id": "bs1", "parentId": nil, "timestamp": "",
				"fromId": "x", "summary": "user discussed Go formatters\nthen forked",
			}),
			want: fg(th.Warning, "[branch summary]: ") + "user discussed Go formatters then forked",
		},
		{
			name: "label_set",
			entry: mustEntry(t, map[string]any{
				"type": "label", "id": "l1", "parentId": nil, "timestamp": "",
				"targetId": "u1", "label": "milestone-1",
			}),
			want: fg(th.Dim, "[label: milestone-1]"),
		},
		{
			name: "label_cleared",
			entry: mustEntry(t, map[string]any{
				"type": "label", "id": "l2", "parentId": nil, "timestamp": "",
				"targetId": "u1", "label": nil,
			}),
			want: fg(th.Dim, "[label: (cleared)]"),
		},
		{
			name: "context_edit_omit",
			entry: mustEntry(t, map[string]any{
				"type": "context_edit", "id": "ce1", "parentId": nil, "timestamp": "",
				"targetId": "u1", "replacement": nil,
			}),
			want: fg(th.Dim, "[context omit: u1]"),
		},
		{
			name: "context_edit_replace",
			entry: mustEntry(t, map[string]any{
				"type": "context_edit", "id": "ce2", "parentId": nil, "timestamp": "",
				"targetId": "u1", "replacement": map[string]any{"content": "shorter"},
			}),
			want: fg(th.Dim, "[context replace: u1]"),
		},
		{
			name: "custom",
			entry: mustEntry(t, map[string]any{
				"type": "custom", "id": "cu1", "parentId": nil, "timestamp": "",
				"customType": "my-marker",
			}),
			want: fg(th.Dim, "[custom: my-marker]"),
		},
		{
			name: "custom_message_string_content",
			entry: mustEntry(t, map[string]any{
				"type": "custom_message", "id": "cm1", "parentId": nil, "timestamp": "",
				"customType": "note", "content": "remember this", "display": true,
			}),
			want: fg(th.CustomMessageLabel, "[note]: ") + "remember this",
		},
		{
			name: "custom_message_block_content",
			entry: mustEntry(t, map[string]any{
				"type": "custom_message", "id": "cm2", "parentId": nil, "timestamp": "",
				"customType": "note",
				"content": []any{
					map[string]any{"type": "text", "text": "hello "},
					map[string]any{"type": "text", "text": "world"},
				},
				"display": true,
			}),
			want: fg(th.CustomMessageLabel, "[note]: ") + "hello world",
		},
		{
			name: "session_info_named",
			entry: mustEntry(t, map[string]any{
				"type": "session_info", "id": "si1", "parentId": nil, "timestamp": "",
				"name": "my session",
			}),
			want: fg(th.Dim, "[title: my session]"),
		},
		{
			name: "session_info_empty",
			entry: mustEntry(t, map[string]any{
				"type": "session_info", "id": "si2", "parentId": nil, "timestamp": "",
			}),
			want: fg(th.Dim, "[title: empty]"),
		},
		{
			name: "bash_execution_normal",
			entry: mustEntry(t, map[string]any{
				"type": "bash_execution", "id": "be1", "parentId": nil, "timestamp": "",
				"role": "bashExecution", "command": "ls -la", "output": "file.txt",
			}),
			want: fg(th.Dim, "[bash: !ls -la]"),
		},
		{
			name: "bash_execution_excluded_from_context",
			entry: mustEntry(t, map[string]any{
				"type": "bash_execution", "id": "be2", "parentId": nil, "timestamp": "",
				"role": "bashExecution", "command": "cat secret.txt", "output": "...",
				"excludeFromContext": true,
			}),
			want: fg(th.Dim, "[bash: !!cat secret.txt]"),
		},
		{
			name: "bash_execution_long_command_truncated",
			entry: mustEntry(t, map[string]any{
				"type": "bash_execution", "id": "be3", "parentId": nil, "timestamp": "",
				"role":    "bashExecution",
				"command": "echo 'this is a very long command that exceeds fifty characters in total'",
			}),
			want: fg(th.Dim, "[bash: !echo 'this is a very long command that exceeds fif…]"),
		},
		{
			name: "unknown_type_is_hidden",
			entry: mustEntry(t, map[string]any{
				"type": "future_type_42", "id": "fu1", "parentId": nil, "timestamp": "",
			}),
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.FormatTreeRow(tc.entry)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatToolCall_PerToolFormatters(t *testing.T) {
	f := newTreeRowFormatter(nil)
	f.home = "/home/me" // deterministic shortenPath fixture
	muted := tui.ActiveTheme().Muted

	cases := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{name: "read_path_only", tool: "read", args: map[string]any{"path": "foo.go"}, want: "[read: foo.go]"},
		{name: "read_file_path_alias", tool: "read", args: map[string]any{"file_path": "bar.go"}, want: "[read: bar.go]"},
		{name: "read_with_offset_and_limit", tool: "read", args: map[string]any{"path": "x.go", "offset": 10.0, "limit": 5.0}, want: "[read: x.go:10-14]"},
		{name: "read_limit_only", tool: "read", args: map[string]any{"path": "x.go", "limit": 5.0}, want: "[read: x.go:1-5]"},
		{name: "read_home_shortened", tool: "read", args: map[string]any{"path": "/home/me/code/x.go"}, want: "[read: ~/code/x.go]"},
		{name: "write", tool: "write", args: map[string]any{"path": "out.txt"}, want: "[write: out.txt]"},
		{name: "edit", tool: "edit", args: map[string]any{"path": "src.go"}, want: "[edit: src.go]"},
		{name: "bash_short", tool: "bash", args: map[string]any{"command": "ls -la"}, want: "[bash: ls -la]"},
		{
			name: "bash_long_truncates_at_50_with_ellipsis",
			tool: "bash",
			// 60 chars of `a` → raw len > 50 → ellipsis.
			args: map[string]any{"command": strings.Repeat("a", 60)},
			want: "[bash: " + strings.Repeat("a", 50) + "...]",
		},
		{
			name: "bash_normalizes_newlines_and_tabs",
			tool: "bash",
			args: map[string]any{"command": "echo a\n\techo b"},
			want: "[bash: echo a  echo b]",
		},
		{name: "grep", tool: "grep", args: map[string]any{"pattern": "TODO", "path": "src"}, want: "[grep: /TODO/ in src]"},
		{name: "grep_default_path", tool: "grep", args: map[string]any{"pattern": "TODO"}, want: "[grep: /TODO/ in .]"},
		{name: "find", tool: "find", args: map[string]any{"pattern": "*.go", "path": "internal"}, want: "[find: *.go in internal]"},
		{name: "ls", tool: "ls", args: map[string]any{"path": "/tmp"}, want: "[ls: /tmp]"},
		{name: "ls_default", tool: "ls", args: nil, want: "[ls: .]"},
		{
			name: "unknown_tool_truncates_args_at_40",
			tool: "spawn",
			args: map[string]any{"foo": strings.Repeat("x", 100)},
			// Marshaled JSON: {"foo":"xxxx..."}; len > 40 → ellipsis.
			// We assert prefix + "...]" + length to keep the test
			// independent of insignificant key-ordering changes.
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.formatToolCall(tc.tool, tc.args)
			if tc.want == "" {
				// "unknown_tool_truncates_args_at_40" branch. Strip the
				// uniform muted wrapping to assert the inner structure.
				inner := stripANSI(got)
				if !strings.HasPrefix(inner, "[spawn: ") || !strings.HasSuffix(inner, "...]") {
					t.Errorf("unknown tool: got %q, want [spawn: <40chars>...]", inner)
				}
				// 40 chars of payload + bracket overhead.
				if len(inner) != len("[spawn: ")+40+len("...]") {
					t.Errorf("unknown tool truncation length wrong: %q (len=%d)", inner, len(inner))
				}
				return
			}
			// All tool-call rows are uniformly muted (upstream:
			// theme.fg("muted", ...)).
			if got != fg(muted, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatTreeRow_LiveSessionToolUsePreWalk drives the formatter
// through a real Session: append an assistant message containing a
// tool_use block, then a user message containing the matching
// tool_result. The pre-walk in newTreeRowFormatter must populate
// toolCallMap so the tool_result row renders via formatToolCall.
func TestFormatTreeRow_LiveSessionToolUsePreWalk(t *testing.T) {
	sess := NewSession("sess-tool-prewalk", "")

	// Assistant turn with a tool_use block.
	if _, err := sess.AppendMessage(agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role: "assistant",
			Content: []ai.AssistantContentBlock{
				ai.TextContent{Text: "let me read it"},
				ai.ToolCall{ID: "tu-1", Name: "read", Arguments: ai.JsonObject{"path": "AGENTS.md"}},
			},
		},
	}); err != nil {
		t.Fatalf("append asst: %v", err)
	}

	// Tool result message.
	trID, err := sess.AppendMessage(agent.AgentMessage{
		ToolResult: &agent.ToolResultMessage{
			Role: agent.RoleToolResult, ToolCallID: "tu-1", ToolName: "read",
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file body"}},
		},
	})
	if err != nil {
		t.Fatalf("append tr: %v", err)
	}

	f := newTreeRowFormatter(sess)
	if _, ok := f.toolCallMap["tu-1"]; !ok {
		t.Fatalf("toolCallMap should contain tu-1 after pre-walk; got %v", f.toolCallMap)
	}

	// Find the tool_result entry in the session and format it.
	var trEntry SessionEntry
	for _, e := range sess.entries {
		if e.Base.ID == trID {
			trEntry = e
			break
		}
	}
	if trEntry.Base.ID == "" {
		t.Fatalf("could not locate tool_result entry %q", trID)
	}
	got := f.FormatTreeRow(trEntry)
	if got != fg(tui.ActiveTheme().Muted, "[read: AGENTS.md]") {
		t.Errorf("tool_result row = %q, want %q", got, fg(tui.ActiveTheme().Muted, "[read: AGENTS.md]"))
	}
}

// TestTreeRowFormat_Assistant_StopReason verifies that assistant messages with
// stopReason "aborted" and "error" render correctly. Part of
func TestTreeRowFormat_Assistant_StopReason(t *testing.T) {
	f := newTreeRowFormatter(nil)
	th := tui.ActiveTheme()
	asst := fg(th.Success, "assistant: ")

	cases := []struct {
		name  string
		entry map[string]any
		want  string
	}{
		{
			name: "aborted_no_text",
			entry: map[string]any{
				"type": "message", "id": "a1", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":       "assistant",
					"stopReason": "aborted",
					"content":    []any{},
				},
			},
			want: asst + fg(th.Muted, "(aborted)"),
		},
		{
			name: "aborted_with_partial_text",
			entry: map[string]any{
				"type": "message", "id": "a2", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":       "assistant",
					"stopReason": "aborted",
					"content":    []any{map[string]any{"type": "text", "text": "partial"}},
				},
			},
			want: asst + fg(th.Muted, "(aborted) ") + "partial",
		},
		{
			name: "error_with_errorMessage",
			entry: map[string]any{
				"type": "message", "id": "a3", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":         "assistant",
					"stopReason":   "error",
					"errorMessage": "context length exceeded",
					"content":      []any{},
				},
			},
			want: asst + fg(th.Error, "context length exceeded"),
		},
		{
			name: "error_no_errorMessage",
			entry: map[string]any{
				"type": "message", "id": "a4", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":       "assistant",
					"stopReason": "error",
					"content":    []any{},
				},
			},
			want: asst + fg(th.Error, "(error)"),
		},
		{
			name: "stop_normal",
			entry: map[string]any{
				"type": "message", "id": "a5", "parentId": nil, "timestamp": "",
				"message": map[string]any{
					"role":       "assistant",
					"stopReason": "stop",
					"content":    []any{map[string]any{"type": "text", "text": "done"}},
				},
			},
			want: asst + "done",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := mustEntry(t, tc.entry)
			got := f.FormatTreeRow(e)
			if got != tc.want {
				t.Errorf("FormatTreeRow = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatTreeRow_ScopedFgReset locks the tree formatter to upstream's
// scoped fg reset (SGR 39). A full SGR 0 reset would clear /tree's
// selected-row background highlight mid-row. Red before the scoped-reset fix.
func TestFormatTreeRow_ScopedFgReset(t *testing.T) {
	f := newTreeRowFormatter(nil)
	row := f.FormatTreeRow(mustEntry(t, map[string]any{
		"type": "message",
		"message": map[string]any{
			"role":    "assistant",
			"content": []any{map[string]any{"type": "text", "text": "hi"}},
		},
	}))
	if strings.Contains(row, "\x1b[0m") {
		t.Errorf("tree row must not use a full SGR 0 reset (clears selected-row bg); got %q", row)
	}
	if !strings.Contains(row, "\x1b[39m") {
		t.Errorf("tree row should close colours with scoped fg reset (SGR 39); got %q", row)
	}
}
