package sdk

import "testing"

// Pi core/extensions/types.ts:1315-1338: each guard is `e.toolName === "<tool>"`.
func TestToolResultGuardsTestToolName(t *testing.T) {
	guards := map[string]func(map[string]any) bool{
		"bash": IsBashToolResult, "powershell": IsPowerShellToolResult, "read": IsReadToolResult, "edit": IsEditToolResult,
		"write": IsWriteToolResult, "grep": IsGrepToolResult, "find": IsFindToolResult, "ls": IsLsToolResult,
	}
	for tool := range guards {
		event := map[string]any{"type": "tool_result", "toolName": tool}
		for other, guard := range guards {
			if got, want := guard(event), other == tool; got != want {
				t.Errorf("guard for %q on a %q result = %v, want %v", other, tool, got, want)
			}
		}
	}
	for other, guard := range guards {
		for name, event := range map[string]map[string]any{"custom tool": {"toolName": "my-tool"}, "no toolName": {"type": "tool_result"}, "non-string toolName": {"toolName": 7}, "nil": nil} {
			if guard(event) {
				t.Errorf("guard for %q accepted a %s payload", other, name)
			}
		}
	}
}
