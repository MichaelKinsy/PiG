package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The CLI resolves the `defaultTools` setting itself and hands the list to the Session as InitialActiveToolNames, as sdk.ts:274-276 passes
// initialActiveToolNames. The list also names extension tools: _refreshToolRegistry activates every registered tool the initial names
// include (agent-session.ts:3487-3506; default-tools-setting.test.ts:82-98, "activates an inactive extension tool with +name"). A name no
// registered tool carries activates nothing.
func TestNewSessionInitialActiveToolNamesNameExtensionTools(t *testing.T) {
	inactive := []extension.ToolDefinition{inactiveRegistryTool()}
	for _, tc := range []struct {
		name   string
		active []string
		want   []string
	}{
		{"a modifier adds the inactive extension tool", []string{"read", "bash", "edit", "write", "inactive_tool"}, []string{"bash", "edit", "inactive_tool", "read", "write"}},
		{"a plain list names only the extension tool", []string{"inactive_tool"}, []string{"inactive_tool"}},
		{"an unregistered name activates nothing", []string{"read", "no_such_tool"}, []string{"read"}},
		{"the CLI default leaves the inactive tool off", []string{"read", "bash", "edit", "write"}, []string{"bash", "edit", "read", "write"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, nil, SessionOptions{InitialActiveToolNames: tc.active}, inactive, nil)
			bindRegistryPort(t, session)
			if got := sortedActiveNames(session); !slices.Equal(got, tc.want) {
				t.Fatalf("active = %q, want %q", got, tc.want)
			}
		})
	}
}

// Pi activates the initial names in list order (sdk.ts:274-276; agent-session.ts:3552-3554 copies activeToolNames, and _applyToolLoadout
// 1561-1565 maps them in that order), so the request and the prompt list the tools in the caller's order: probed on Pi 0.99.2 in print
// mode, `["codemode","read"]` declares codemode, read and `["bash","read"]` declares bash, read. The Session keeps the order of
// InitialActiveToolNames, and an unregistered name activates nothing.
func TestNewSessionInitialActiveToolNamesKeepCallerOrder(t *testing.T) {
	inactive := []extension.ToolDefinition{inactiveRegistryTool()}
	for _, tc := range []struct {
		name     string
		defaults []string
		active   []string
		want     []string
	}{
		{"an extension tool listed first", []string{"inactive_tool", "read"}, []string{"inactive_tool", "read"}, []string{"inactive_tool", "read"}},
		{"built-ins out of registry order", []string{"bash", "read"}, []string{"bash", "read"}, []string{"bash", "read"}},
		{"a modifier appends", []string{"+inactive_tool", "-write"}, []string{"read", "bash", "edit", "inactive_tool"}, []string{"read", "bash", "edit", "inactive_tool"}},
		{"the caller's order, not the setting's", []string{"write"}, []string{"write", "zz_tool", "inactive_tool", "bash", "read"}, []string{"write", "inactive_tool", "bash", "read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, tc.defaults, SessionOptions{InitialActiveToolNames: tc.active}, inactive, nil)
			bindRegistryPort(t, session)
			if got := session.ActiveToolNames(); !slices.Equal(got, tc.want) {
				t.Fatalf("active = %q, want %q", got, tc.want)
			}
		})
	}
}

// createAgentSession({ tools }) passes the list both as the allowlist and as initialActiveToolNames (sdk.ts:271-276). _refreshToolRegistry
// copies the initial names in the caller's order, then appends the registered tools the allowlist names or matches in registry order, and
// the Set keeps each name's first position (agent-session.ts:3552-3562, 3577; registry order is tools/index.ts createAllToolDefinitions). So `--tools bash,read` declares bash, read, and a pattern
// adds its matches after the named tools.
func TestNewSessionAllowedToolsActivateInCallerOrder(t *testing.T) {
	inactive := []extension.ToolDefinition{inactiveRegistryTool()}
	for _, tc := range []struct {
		name  string
		tools []string
		want  []string
	}{
		{"built-ins out of registry order", []string{"bash", "read"}, []string{"bash", "read"}},
		{"an extension tool before a built-in", []string{"inactive_tool", "ls", "read"}, []string{"inactive_tool", "ls", "read"}},
		{"a pattern's matches follow the named tools", []string{"write", "*", "bash"}, []string{"write", "bash", "read", "powershell", "edit", "grep", "find", "ls", "inactive_tool"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed := make(map[string]struct{}, len(tc.tools))
			for _, name := range tc.tools {
				allowed[name] = struct{}{}
			}
			session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: allowed, InitialActiveToolNames: tc.tools}, inactive, nil)
			bindRegistryPort(t, session)
			if got := session.ActiveToolNames(); !slices.Equal(got, tc.want) {
				t.Fatalf("active = %q, want %q", got, tc.want)
			}
		})
	}
}
