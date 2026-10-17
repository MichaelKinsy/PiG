package codingagent

import (
	"slices"
	"testing"
)

// A rebuild of the tools (/reload, and the /new and /resume runtime replacement through InteractiveReplacement) keeps the `--tools` list's order: `pig --tools bash,read` still lists bash before read. Pi rebuilds from the same options.tools (initialActiveToolNames).
//
// mutation-checked: dropping the orderToolsByInitialNames call in buildAgentTools gave the registry order [read bash] for `--tools bash,read`.
// upstream: packages/coding-agent/src/core/sdk.ts:272-276 (initialActiveToolNames = options.tools), agent-session.ts:3552-3554
func TestInteractiveToolRebuildKeepsTheToolsListOrder(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed map[string]struct{}
		initial []string
		want    []string
	}{
		{"caller order", map[string]struct{}{"bash": {}, "read": {}}, []string{"bash", "read"}, []string{"bash", "read"}},
		{"no list keeps registry order", map[string]struct{}{"bash": {}, "read": {}}, nil, []string{"read", "bash"}},
	} {
		m := &InteractiveMode{opts: InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), AllowedTools: tc.allowed, InitialActiveToolNames: tc.initial}}
		built, errs := m.buildAgentTools()
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		var got []string
		for _, tool := range built {
			got = append(got, tool.Name())
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: tools = %v, want %v", tc.name, got, tc.want)
		}
	}
}
