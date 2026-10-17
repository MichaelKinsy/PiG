package extension

// pi: packages/coding-agent/src/core/extensions/types.ts

import "testing"

// upstream: packages/coding-agent/src/core/extensions/types.ts:1349-1363 isToolCallEventType(toolName, event) is event.toolName === toolName for every member of the union; the Session emits CustomToolCallEvent for all of them.
func TestIsToolCallEventType(t *testing.T) {
	events := map[string]ToolCallEvent{
		"bash":     CustomToolCallEvent{ToolName: "bash"},
		"read":     CustomToolCallEvent{ToolName: "read"},
		"custom-x": CustomToolCallEvent{ToolName: "custom-x"},
	}
	for name, event := range events {
		for other := range events {
			if got, want := IsToolCallEventType(other, event), other == name; got != want {
				t.Errorf("IsToolCallEventType(%q, %T) = %v, want %v", other, event, got, want)
			}
		}
	}
	if IsToolCallEventType("bash", nil) {
		t.Error("a nil event is not a bash call")
	}
}

// upstream: packages/coding-agent/src/core/extensions/types.ts:1315-1338 isBashToolResult ... isLsToolResult are e.toolName === "<tool>" for every
// member of the ToolResultEvent union: each guard is true for its own tool only, for the typed variant and for the CustomToolResultEvent the Session
// emits for every tool, and false for any other tool, a custom tool and a nil event.
func TestToolResultGuardsTestToolName(t *testing.T) {
	guards := map[string]func(ToolResultEvent) bool{
		"bash": IsBashToolResult, "powershell": IsPowerShellToolResult, "read": IsReadToolResult, "edit": IsEditToolResult,
		"write": IsWriteToolResult, "grep": IsGrepToolResult, "find": IsFindToolResult, "ls": IsLsToolResult,
	}
	typed := map[string]ToolResultEvent{
		"bash": BashToolResultEvent{ToolName: "bash"}, "powershell": PowerShellToolResultEvent{ToolName: "powershell"}, "read": ReadToolResultEvent{ToolName: "read"},
		"edit": EditToolResultEvent{ToolName: "edit"}, "write": WriteToolResultEvent{ToolName: "write"}, "grep": GrepToolResultEvent{ToolName: "grep"},
		"find": FindToolResultEvent{ToolName: "find"}, "ls": LsToolResultEvent{ToolName: "ls"},
	}
	for tool := range guards {
		events := map[string]ToolResultEvent{"typed": typed[tool], "custom": CustomToolResultEvent{ToolName: tool}}
		for form, event := range events {
			for other, guard := range guards {
				if got, want := guard(event), other == tool; got != want {
					t.Errorf("guard for %q on the %s %q result = %v, want %v", other, form, tool, got, want)
				}
			}
		}
	}
	for other, guard := range guards {
		if guard(CustomToolResultEvent{ToolName: "my-tool"}) || guard(nil) {
			t.Errorf("guard for %q accepted a custom tool or a nil event", other)
		}
	}
}
