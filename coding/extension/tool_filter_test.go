package extension

import "testing"

// Ports packages/coding-agent/src/core/mcp-servers.ts createToolNameMatcher and the `_isAllowedTool` and `_isActivatable`
// rules of agent-session.ts (1.0.4).
func TestToolNameMatcher(t *testing.T) {
	if ToolNameMatcher(nil) != nil {
		t.Fatal("a nil entry set must have no matcher")
	}
	matcher := ToolNameMatcher(map[string]struct{}{"read": {}, "mcp__radius__*": {}, "*_tool": {}, "a.b+c": {}})
	for name, want := range map[string]bool{
		"read": true, "reader": false, "mcp__radius__search": true, "mcp__radius__": true, "mcp__other__search": false,
		"dynamic_tool": true, "_tool": true, "tool": false, "a.b+c": true, "aXb+c": false, "read\n": false,
	} {
		if got := matcher(name); got != want {
			t.Errorf("matcher(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestToolFilterKeepsMCPToolsUnlessTheAllowlistFiltersThem(t *testing.T) {
	tests := []struct {
		name     string
		allowed  map[string]struct{}
		excluded map[string]struct{}
		tool     string
		allows   bool
		// activates is MayBeActive for a direct tool with tool_search registered.
		activates bool
	}{
		{"no allowlist keeps everything", nil, nil, "mcp__docs__search", true, true},
		{"unnamed MCP tool stays registered", map[string]struct{}{"read": {}}, nil, "mcp__docs__search", true, false},
		{"unnamed MCP resource tool stays registered", map[string]struct{}{"read": {}}, nil, "read_mcp_resource", true, false},
		{"named MCP tool is active", map[string]struct{}{"mcp__docs__*": {}}, nil, "mcp__docs__search", true, true},
		{"an mcp__ entry filters other MCP tools", map[string]struct{}{"mcp__docs__*": {}}, nil, "mcp__other__search", false, false},
		{"an empty allowlist filters MCP tools", map[string]struct{}{}, nil, "mcp__docs__search", false, false},
		{"an unnamed non-MCP tool is filtered", map[string]struct{}{"read": {}}, nil, "bash", false, true},
		{"the denylist wins over the allowlist", map[string]struct{}{"mcp__docs__*": {}}, map[string]struct{}{"mcp__docs__f*": {}}, "mcp__docs__fail", false, true},
		{"the denylist removes unnamed MCP tools", map[string]struct{}{"read": {}}, map[string]struct{}{"mcp__*": {}}, "mcp__docs__search", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filter := NewToolFilter(tc.allowed, tc.excluded)
			if got := filter.Allows(tc.tool); got != tc.allows {
				t.Errorf("Allows(%q) = %t, want %t", tc.tool, got, tc.allows)
			}
			if got := filter.MayBeActive(tc.tool, ToolExposureDirect, true); got != tc.activates {
				t.Errorf("MayBeActive(%q, direct, tool_search) = %t, want %t", tc.tool, got, tc.activates)
			}
		})
	}
	filter := NewToolFilter(map[string]struct{}{"tool_search": {}}, nil)
	for _, tc := range []struct {
		exposure   ToolExposure
		toolSearch bool
		want       bool
	}{
		{ToolExposureDirect, true, false}, {ToolExposureDeferred, true, true}, {ToolExposureCodemode, true, true},
		{ToolExposureDeferred, false, false}, {ToolExposureCodemode, false, false},
	} {
		if got := filter.MayBeActive("mcp__docs__search", tc.exposure, tc.toolSearch); got != tc.want {
			t.Errorf("MayBeActive(exposure %q, tool_search %t) = %t, want %t", tc.exposure, tc.toolSearch, got, tc.want)
		}
	}
	if !filter.MayBeActive("bash", ToolExposureDirect, false) {
		t.Error("a non-MCP tool may always be active")
	}
}
