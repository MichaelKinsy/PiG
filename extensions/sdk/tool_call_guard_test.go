package sdk

import "testing"

// Pi core/extensions/types.ts:1372-1374: isToolCallEventType(toolName, event) is `event.toolName === toolName`, for built-in and custom tools alike. A prefix, a case variant or
// an empty name does not match, and a payload without a string toolName is a call of no tool (`undefined === toolName` is false for every string toolName, "" included).
func TestIsToolCallEventTypeComparesTheEventsToolName(t *testing.T) {
	names := []string{"bash", "read", "my_tool", "bashful", "Bash", ""}
	for _, tool := range names {
		event := map[string]any{"type": "tool_call", "toolCallId": "c1", "toolName": tool, "input": map[string]any{}}
		for _, name := range names {
			if got, want := IsToolCallEventType(name, event), name == tool; got != want {
				t.Errorf("IsToolCallEventType(%q) on a %q call = %v, want %v", name, tool, got, want)
			}
		}
	}
	for _, name := range names {
		for kind, event := range map[string]map[string]any{"no toolName": {"type": "tool_call"}, "non-string toolName": {"toolName": 7}, "null toolName": {"toolName": nil}, "nil": nil} {
			if IsToolCallEventType(name, event) {
				t.Errorf("IsToolCallEventType(%q) accepted a %s payload", name, kind)
			}
		}
	}
}
