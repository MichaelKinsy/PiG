package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.99.1 agent-session.ts:3487-3506 (_refreshToolRegistry) and :3510-3519 (_isDeclarable, _isActivatedOnRegistration).
// With an allowlist, naming a declarable tool activates it even when it is inactive by default and an unnamed tool is not in the registry at all;
// without one only a declarable tool with defaultActive !== false starts active. The codemode and tool_search built-ins register
// `defaultActive: false` (extensions/codemode/index.ts:41, extensions/tool-search/index.ts:14) with exposure model-only.
func TestExtensionToolStartsActive(t *testing.T) {
	inactive, active := false, true
	named := map[string]struct{}{"tool": {}}
	empty := map[string]struct{}{}
	for _, tc := range []struct {
		name       string
		definition extension.ToolDefinition
		allowed    map[string]struct{}
		want       bool
	}{
		{"direct tool starts active", extension.ToolDefinition{Name: "tool"}, nil, true},
		{"explicit direct exposure", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureDirect}, nil, true},
		{"model-only tool starts active", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureModelOnly}, nil, true},
		{"defaultActive true", extension.ToolDefinition{Name: "tool", DefaultActive: &active}, nil, true},
		{"defaultActive false is registered, not active", extension.ToolDefinition{Name: "tool", DefaultActive: &inactive}, nil, false},
		{"model-only defaultActive false (codemode, tool_search)", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureModelOnly, DefaultActive: &inactive}, nil, false},
		{"codemode exposure is not declarable", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureCodemode}, nil, false},
		{"deferred exposure is not declarable", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureDeferred}, nil, false},
		{"named tool activates despite defaultActive false", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureModelOnly, DefaultActive: &inactive}, named, true},
		{"named codemode-exposure tool stays undeclared", extension.ToolDefinition{Name: "tool", Exposure: extension.ToolExposureCodemode}, named, false},
		{"unnamed tool is not activated by an allowlist", extension.ToolDefinition{Name: "other"}, named, false},
		{"an empty allowlist activates nothing", extension.ToolDefinition{Name: "tool"}, empty, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtensionToolStartsActive(tc.definition, tc.allowed); got != tc.want {
				t.Fatalf("ExtensionToolStartsActive = %v, want %v", got, tc.want)
			}
		})
	}
}
