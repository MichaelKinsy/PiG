package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The CLI resolves the `defaultTools` setting itself and hands the list to the Session as ActiveBuiltinTools, which sdk.ts:264-269 passes as
// initialActiveToolNames. The list also names extension tools: _refreshToolRegistry activates every registered tool the initial names
// include (agent-session.ts:3487-3506; default-tools-setting.test.ts:82-98, "activates an inactive extension tool with +name"). A name no
// registered tool carries activates nothing.
func TestNewSessionActiveBuiltinToolsNameExtensionTools(t *testing.T) {
	inactive := []extension.ToolDefinition{inactiveRegistryTool()}
	for _, tc := range []struct {
		name   string
		active map[string]struct{}
		want   []string
	}{
		{"a modifier adds the inactive extension tool", map[string]struct{}{"read": {}, "bash": {}, "edit": {}, "write": {}, "inactive_tool": {}}, []string{"bash", "edit", "inactive_tool", "read", "write"}},
		{"a plain list names only the extension tool", map[string]struct{}{"inactive_tool": {}}, []string{"inactive_tool"}},
		{"an unregistered name activates nothing", map[string]struct{}{"read": {}, "no_such_tool": {}}, []string{"read"}},
		{"the CLI default leaves the inactive tool off", map[string]struct{}{"read": {}, "bash": {}, "edit": {}, "write": {}}, []string{"bash", "edit", "read", "write"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, nil, SessionOptions{ActiveBuiltinTools: tc.active}, inactive, nil)
			bindRegistryPort(t, session)
			if got := sortedActiveNames(session); !slices.Equal(got, tc.want) {
				t.Fatalf("active = %q, want %q", got, tc.want)
			}
		})
	}
}

// Pi activates the initial names in list order (sdk.ts:268-270; agent-session.ts:3495-3497 copies activeToolNames, and _applyToolLoadout
// 1508-1511 maps them in that order), so the request and the prompt list the tools in `defaultTools` order: probed on Pi 0.99.2 in print
// mode, `["codemode","read"]` declares codemode, read and `["bash","read"]` declares bash, read. The CLI's ActiveBuiltinTools set is that
// resolved list, so the Session keeps its order; names the setting does not list follow in registry order, then by name, and an unregistered name activates nothing.
func TestNewSessionActiveBuiltinToolsKeepDefaultToolsOrder(t *testing.T) {
	inactive := []extension.ToolDefinition{inactiveRegistryTool()}
	set := func(names ...string) map[string]struct{} {
		result := make(map[string]struct{}, len(names))
		for _, name := range names {
			result[name] = struct{}{}
		}
		return result
	}
	for _, tc := range []struct {
		name     string
		defaults []string
		active   map[string]struct{}
		want     []string
	}{
		{"an extension tool listed first", []string{"inactive_tool", "read"}, set("inactive_tool", "read"), []string{"inactive_tool", "read"}},
		{"built-ins out of registry order", []string{"bash", "read"}, set("bash", "read"), []string{"bash", "read"}},
		{"a modifier appends", []string{"+inactive_tool", "-write"}, set("read", "bash", "edit", "inactive_tool"), []string{"read", "bash", "edit", "inactive_tool"}},
		{"names the setting does not list", []string{"write"}, set("write", "zz_tool", "inactive_tool", "bash", "read"), []string{"write", "read", "bash", "inactive_tool"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, tc.defaults, SessionOptions{ActiveBuiltinTools: tc.active}, inactive, nil)
			bindRegistryPort(t, session)
			if got := session.ActiveToolNames(); !slices.Equal(got, tc.want) {
				t.Fatalf("active = %q, want %q", got, tc.want)
			}
		})
	}
}
