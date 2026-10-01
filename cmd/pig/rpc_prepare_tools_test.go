package main

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// rpcPrepare lists the startup loadout that Pi's sdk.ts:258-265 derives (initialActiveToolNames minus excludeTools) and AgentSession._refreshToolRegistry (agent-session.ts:3147-3230) extends with the extension tools the allowlist and denylist admit.
func TestRPCPrepareStartupToolsFollowBuildSelection(t *testing.T) {
	extensionTools := map[string]extension.RegisteredTool{}
	for _, name := range []string{"ext_a", "ext_b"} {
		extensionTools[name] = extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name}}
	}
	runner := inproc.NewRunner([]extension.Extension{{Name: "tools", Path: "/ext/tools", Tools: extensionTools, ToolOrder: []string{"ext_a", "ext_b"}}}, t.TempDir())
	toSet := func(names ...string) map[string]struct{} {
		set := map[string]struct{}{}
		for _, name := range names {
			set[name] = struct{}{}
		}
		return set
	}
	cases := []struct {
		name     string
		builtin  []string
		allowed  map[string]struct{}
		excluded map[string]struct{}
		want     []string
	}{
		{name: "defaults", builtin: []string{"read", "bash"}, want: []string{"read", "bash", "ext_a", "ext_b"}},
		{name: "excluded extension tool", builtin: []string{"read"}, excluded: toSet("ext_a"), want: []string{"read", "ext_b"}},
		{name: "allowlist admits only listed extension tools", builtin: []string{"read"}, allowed: toSet("read", "ext_b"), want: []string{"read", "ext_b"}},
		{name: "allowlist minus excluded", builtin: []string{"read"}, allowed: toSet("read", "ext_a", "ext_b"), excluded: toSet("ext_b"), want: []string{"read", "ext_a"}},
		{name: "empty allowlist admits nothing", builtin: []string{}, allowed: toSet(), want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			build := &cliBuild{
				CWD:            t.TempDir(),
				AgentToolNames: tc.builtin,
				Allowed:        tc.allowed,
				ExcludedTools:  tc.excluded,
			}
			state := &rpcSessionState{}
			var start coding.SessionStartOptions
			(&cliRuntimeBuilder{}).rpcPrepare(build, state, runner, &start)
			if !slices.Equal(state.AgentToolNames, tc.want) {
				t.Fatalf("startup tools %v, want %v", state.AgentToolNames, tc.want)
			}
			if !slices.Equal(state.PromptOptions.Tools, tc.want) {
				t.Fatalf("prompt tools %v, want %v", state.PromptOptions.Tools, tc.want)
			}
		})
	}
}
